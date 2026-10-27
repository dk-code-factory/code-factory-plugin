import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { cpus, platform, release } from "node:os";
import { fixtureDaemonOrigin as origin } from "../../fixture-ports";

type Sample = {
  scenario: string;
  mode: string;
  ms: number;
  usable: boolean;
  pending: number;
  readyMs: number;
  firstFrameMs: number;
  notificationMs: number;
  messageLagMs: number;
};
type RequestSample = {
  name: string;
  duration: number;
  bytes: number;
  arguments: Record<string, unknown>;
};
const samples: Sample[] = [];
const requests: RequestSample[] = [];
const renders: { name: string; ms: number }[] = [];
const longTasks: number[] = [];
let providerMetrics: unknown;

async function prepare(context: BrowserContext, scenario: string) {
  await context.addInitScript(() => {
    if (window === window.parent) return;
    const tasks: number[] = [];
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) tasks.push(entry.duration);
        window.parent.postMessage(
          { longTasks: list.getEntries().map((e) => e.duration) },
          "*",
        );
      }).observe({ type: "longtask", buffered: true });
    } catch {}
    const observer = new MutationObserver(() => {
      const nav = document.querySelector('nav[aria-label="Main navigation"]');
      if (!(nav instanceof HTMLElement) || nav.dataset.ready !== "true") return;
      const button = [...nav.querySelectorAll("button")].find(
        (b) => b.textContent?.trim() === "Messages",
      );
      if (!button || button.getBoundingClientRect().height < 1) return;
      observer.disconnect();
      button.click();
      const usable = button.getAttribute("aria-current") === "page";
      const readyAt = performance.timeOrigin + performance.now();
      // Keep the same conservative two-frame paint condition, but timestamp
      // it here so the host's message delivery queue is not part of UI paint.
      requestAnimationFrame(() => {
        const firstFrameAt = performance.timeOrigin + performance.now();
        requestAnimationFrame(() =>
          window.parent.postMessage(
            {
              shellPainted: true,
              usable,
              readyAt,
              firstFrameAt,
              paintedAt: performance.timeOrigin + performance.now(),
            },
            "*",
          ),
        );
      });
    });
    observer.observe(document, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["data-ready"],
    });
  });
  const page = await context.newPage();
  await page.exposeFunction(
    "fixtureCall",
    async (p: { name: string; arguments: Record<string, unknown> }) => {
      const start = performance.now();
      const response = await context.request.post(
        origin + "/fixture-mcp?scenario=" + scenario,
        {
          headers: {
            Authorization: "Bearer " + "a".repeat(64),
            Accept: "application/json, text/event-stream",
            "MCP-Protocol-Version": "2026-07-28",
            "Mcp-Method": "tools/call",
            "Mcp-Name": p.name,
          },
          data: {
            jsonrpc: "2.0",
            id: 1,
            method: "tools/call",
            params: {
              ...p,
              _meta: {
                "io.modelcontextprotocol/protocolVersion": "2026-07-28",
                "io.modelcontextprotocol/clientInfo": {
                  name: "fixture",
                  version: "1",
                },
                "io.modelcontextprotocol/clientCapabilities": {},
              },
            },
          },
        },
      );
      const bytes = await response.body();
      requests.push({
        name: p.name,
        duration: performance.now() - start,
        bytes: bytes.length,
        arguments: p.arguments,
      });
      const result = JSON.parse(bytes.toString());
      if (result.error) throw new Error(JSON.stringify(result.error));
      return result.result;
    },
  );
  await page.route(origin + "/fixture-host", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><button id="launch">Open Code Factory</button><script>
window.addEventListener("message",async e=>{
 const m=e.data;
 if(m.shellPainted){
 const notificationAt=performance.timeOrigin+performance.now();
 window.launchResult={ms:m.paintedAt-window.startedEpoch,readyMs:m.readyAt-window.startedEpoch,firstFrameMs:m.firstFrameAt-window.startedEpoch,notificationMs:notificationAt-window.startedEpoch,messageLagMs:notificationAt-m.paintedAt,usable:m.usable,pending:window.pending};return
 }
 if(m.longTasks){window.tasks=(window.tasks||[]).concat(m.longTasks);return}
 if(!m||m.id===undefined)return;
 try{
 let result={};
 if(m.method==="ui/initialize")result={protocolVersion:m.params.protocolVersion,hostInfo:{name:"Fixture MCP host",version:"1"},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:"light",displayMode:"fullscreen"}};
 else if(m.method==="tools/call")result=await window.fixtureCall(m.params);
 e.source.postMessage({jsonrpc:"2.0",id:m.id,result},"*");
 }catch(error){e.source.postMessage({jsonrpc:"2.0",id:m.id,error:{code:-32000,message:String(error)}},"*")}
});
document.getElementById("launch").onclick=()=>{
 window.started=performance.now();window.startedEpoch=performance.timeOrigin+window.started;window.launchResult=undefined;
 document.querySelector("iframe")?.remove();
 const frame=document.createElement("iframe");frame.title="Code Factory";frame.style="width:100%;height:90vh;border:0";frame.src=${JSON.stringify(origin + "/fixture-ui")};document.body.appendChild(frame);
};
</script>`,
    }),
  );
  await page.goto(origin + "/fixture-host");
  return page;
}
async function launch(page: Page, scenario: string, mode: string) {
  await page.evaluate(() =>
    document.querySelector<HTMLButtonElement>("#launch")!.click(),
  );
  await page.waitForFunction(
    () =>
      Boolean((window as unknown as { launchResult: unknown }).launchResult),
    undefined,
    { timeout: 10000 },
  );
  const result = await page.evaluate(
    () =>
      (
        window as unknown as {
          launchResult: Omit<Sample, "scenario" | "mode">;
        }
      ).launchResult,
  );
  const metrics = await (
    await page.request.get(origin + "/fixture-metrics")
  ).json();
  samples.push({ scenario, mode, ...result, pending: metrics.pending });
  return result;
}
async function measure(
  name: string,
  page: Page,
  operation: () => Promise<unknown>,
) {
  const start = await page.evaluate(() => performance.now());
  await operation();
  const end = await page.evaluate(
    () =>
      new Promise<number>((resolve) =>
        requestAnimationFrame(() =>
          requestAnimationFrame(() => resolve(performance.now())),
        ),
      ),
  );
  renders.push({ name, ms: end - start });
}
test("production embedded direct launch: every fresh and warm sample is at most 50 ms", async ({
  browser,
}) => {
  await mkdir("performance-results", { recursive: true });
  try {
    for (let n = 0; n < 20; n++) {
      const scenario = ["empty", "cold", "retained"][n % 3];
      const context = await browser.newContext();
      try {
        const page = await prepare(context, scenario);
        if (n === 2)
          await page.request.get(origin + "/fixture-work?scenario=retained");
        await launch(page, scenario, "fresh");
        const ui = page.frameLocator("iframe");
        await expect(
          ui.getByRole("status", { name: "Connection status" }),
        ).toHaveText("Connected");
        if (n === 2) {
          // Saved-state delivery and launch remain independent of delayed external work.
          expect(samples.at(-1)!.pending).toBe(2);
          await measure("navigation", page, async () => {
            await ui
              .getByRole("button", { name: "Issues", exact: true })
              .click();
            await expect(ui.locator(".inbox-row")).toHaveCount(50);
          });
          await measure("search", page, async () => {
            await ui.getByLabel("Search inbox").fill("Retained item 1000");
            await expect(
              ui.getByRole("button", { name: /Retained item 1000/ }),
            ).toBeVisible();
          });
          await measure("item detail", page, async () => {
            await ui
              .getByRole("button", { name: /Retained item 1000/ })
              .click();
            await expect(
              ui.getByRole("heading", {
                name: "Retained item 1000",
                exact: true,
              }),
            ).toBeVisible();
          });
          await ui.getByText("Item details", { exact: true }).click();
          await ui.getByText("Actions", { exact: true }).click();
          await measure("action acknowledgement", page, async () => {
            await ui
              .getByRole("button", { name: "Acknowledge new", exact: true })
              .click();
            await expect
              .poll(() =>
                requests.some(
                  (r) =>
                    r.name === "item_action" &&
                    r.arguments.action === "acknowledge",
                ),
              )
              .toBe(true);
          });
          await measure("Messages", page, async () => {
            await ui
              .getByRole("button", { name: "Messages", exact: true })
              .click();
            await expect(
              ui.getByRole("region", { name: "Waiting for you" }),
            ).toBeVisible();
          });
          await measure("reports", page, async () => {
            await ui
              .getByRole("button", { name: "Reported issues", exact: true })
              .click();
            await expect(
              ui.getByRole("region", { name: "Reported issues" }),
            ).toBeVisible();
          });
          await measure("settings", page, async () => {
            await page.evaluate((url) => {
              const frame = document.createElement("iframe");
              frame.title = "Settings";
              frame.src = url;
              document.body.appendChild(frame);
            }, origin + "/fixture-ui?view=settings");
            await expect(
              page
                .frameLocator('iframe[title="Settings"]')
                .getByLabel("GitHub repository"),
            ).toBeVisible();
          });
        }
        if (n === 19)
          for (let warm = 0; warm < 10; warm++) {
            await launch(page, scenario, "warm");
            await expect(
              ui.getByRole("status", { name: "Connection status" }),
            ).toHaveText("Connected");
          }
        longTasks.push(
          ...(await page.evaluate(
            () => (window as unknown as { tasks: number[] }).tasks ?? [],
          )),
        );
      } finally {
        await context.close();
      }
    }
    const metricsContext = await browser.newContext();
    const metrics = await metricsContext.request
      .get(origin + "/fixture-metrics")
      .then((r) => r.json());
    providerMetrics = metrics;
    await metricsContext.close();
    expect(metrics.githubDelayUs.length).toBeGreaterThan(0);
    expect(
      metrics.githubDelayUs.every((delay: number) => delay >= 100000),
    ).toBe(true);
    const ai = String(metrics.aiDelays).trim().split("\n").filter(Boolean);
    expect(ai.length).toBeGreaterThan(0);
    expect(ai.every((line) => Number(line.split(" ").at(-1)) >= 100000)).toBe(
      true,
    );
    expect(samples).toHaveLength(30);
    expect(samples.every((s) => s.usable)).toBe(true);
    expect(
      samples.every(
        (s) =>
          Number.isFinite(s.ms) &&
          s.ms >= 0 &&
          Number.isFinite(s.notificationMs) &&
          s.notificationMs >= 0 &&
          // Different contexts may coarsen timeOrigin independently, so this
          // diagnostic difference can be slightly negative. Keep its raw value;
          // the actual click-to-paint duration above remains nonnegative.
          Number.isFinite(s.messageLagMs),
      ),
    ).toBe(true);
    expect(
      samples.every(
        (s) =>
          s.readyMs >= 0 &&
          s.firstFrameMs >= s.readyMs &&
          s.ms >= s.firstFrameMs,
      ),
    ).toBe(true);
    expect(
      samples.filter((s) => s.ms > 50),
      JSON.stringify(samples),
    ).toEqual([]);
  } finally {
    await writeFile(
      "performance-results/timing.json",
      JSON.stringify(
        {
          samples,
          requests,
          renders,
          longTasks,
          providerMetrics,
          environment: {
            browser: browser.version(),
            platform: platform(),
            release: release(),
            cpu: cpus()[0]?.model,
            cpus: cpus().length,
            node: process.version,
            workers: 1,
            retries: 0,
          },
        },
        null,
        2,
      ),
    );
  }
});
