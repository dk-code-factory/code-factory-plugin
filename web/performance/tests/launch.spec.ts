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
  fcpMs: number;
  doubleFrameMs: number;
  navigationPaints: { id: string; label: string; atMs: number; area: number }[];
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
    const labels = [
      "Working",
      "Issues",
      "My PRs",
      "Review PRs",
      "Messages",
      "Reported issues",
    ];
    const paints = new Map<
      string,
      { id: string; label: string; at: number; area: number }
    >();
    let readyAt = 0,
      firstFrameAt = 0,
      doubleFrameAt = 0,
      fcpAt = 0,
      usable = false,
      reported = false;
    const report = () => {
      if (
        reported ||
        !readyAt ||
        !doubleFrameAt ||
        !fcpAt ||
        paints.size !== labels.length
      )
        return;
      const navigationPaints = [...paints.values()];

      reported = true;
      // Use direct paint evidence for every navigation label, corroborated by
      // FCP. Animation frames remain diagnostics, not presentation guarantees.
      window.parent.postMessage(
        {
          shellPainted: true,
          usable,
          readyAt,
          firstFrameAt,
          doubleFrameAt,
          fcpAt,
          navigationPaints,
          paintedAt: Math.max(fcpAt, ...navigationPaints.map((p) => p.at)),
        },
        "*",
      );
    };
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        if (entry.name === "first-contentful-paint")
          fcpAt = performance.timeOrigin + entry.startTime;
      }
      report();
    }).observe({ type: "paint", buffered: true });
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        const e = entry as PerformanceEntry & {
          identifier: string;
          renderTime: number;
          intersectionRect: DOMRectReadOnly;
        };
        if (!e.identifier.startsWith("factory-nav-")) continue;
        const index = Number(e.identifier.slice("factory-nav-".length));
        if (!labels[index] || e.renderTime <= 0) continue;
        paints.set(e.identifier, {
          id: e.identifier,
          label: labels[index],
          at: performance.timeOrigin + e.renderTime,
          area: e.intersectionRect.width * e.intersectionRect.height,
        });
      }
      report();
    }).observe({ type: "element", buffered: true });
    const observer = new MutationObserver(() => {
      const nav = document.querySelector('nav[aria-label="Main navigation"]');
      if (!(nav instanceof HTMLElement) || nav.dataset.ready !== "true") return;
      const buttons = [...nav.querySelectorAll("button")];
      if (
        buttons.length !== labels.length ||
        buttons.some(
          (b, i) =>
            b.textContent?.trim() !== labels[i] ||
            b.getBoundingClientRect().height < 1,
        )
      )
        return;
      buttons.forEach((b, i) =>
        b.setAttribute("elementtiming", "factory-nav-" + i),
      );
      observer.disconnect();
      buttons[4].click();
      usable = buttons[4].getAttribute("aria-current") === "page";
      readyAt = performance.timeOrigin + performance.now();
      requestAnimationFrame(() => {
        firstFrameAt = performance.timeOrigin + performance.now();
        requestAnimationFrame(() => {
          doubleFrameAt = performance.timeOrigin + performance.now();
          report();
        });
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
test("production embedded direct launch: every fresh and warm sample is at most 100 ms", async ({
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
          s.doubleFrameMs >= s.firstFrameMs &&
          s.ms >= s.readyMs &&
          Number.isFinite(s.fcpMs) &&
          s.ms >= s.fcpMs &&
          s.navigationPaints.length === 6 &&
          s.navigationPaints.every(
            (p) =>
              Number.isFinite(p.atMs) &&
              p.atMs >= s.readyMs &&
              p.atMs <= s.ms &&
              p.area > 0,
          ),
      ),
    ).toBe(true);
    expect(
      samples.filter((s) => s.ms > 100),
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
