import {
  test,
  expect,
  type Page,
  type Locator,
  type FrameLocator,
} from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import type { Snapshot, Run, Detail, JournalEvent, PRReviews, IssueReport, ReportPage } from "../src/types";
function initial(): Snapshot {
  return {
    viewer: { login: "alex", teams: [] },
    cursor: 1,
    inbox: [
      {
        item: {
          id: "owner/repo#1",
          repository: "owner/repo",
          number: 1,
          kind: "issue",
          title: "Fix reconnect regression",
          body: "Connections stop after a brief outage.",
          url: "https://github.com/owner/repo/issues/1",
          state: "open",
          author: "alex",
          assignees: [],
          labels: ["P1"],
          draft: false,
          head: "",
          branch: "",
          revision: "rev-1",
          updated: new Date().toISOString(),
          activity: [],
        },
        user: { new: true, readRevision: "", override: null, actions: [] },
        unread: true,
        priority: 1,
        prioritySource: "label",
        assessment: {
          status: "pending",
          rationale: "",
          confidence: 0,
          error: "",
        },
      },
    ],
    repositories: [],
    profiles: [
      {
        id: "implement",
        name: "Investigation",
        prompt: "Investigate {{title}}.\n{{body}}",
        skills: [],
        model: "",
        effort: "",
        sandbox: "read-only",
        approval: "on-request",
        publishing: "ask",
      },
    ],
    runs: [],
    skills: [],
    attention: [],
    config: {
      id: "settings",
      priorityLabels: { P0: 0, P1: 1, P2: 2, P3: 3 },
      assessmentPrompt: "Assess urgency.",
      policyVersion: "1",
      jiraUrl: "",
      jiraEmail: "",
      notifications: true,
    },
  };
}
async function host(
  page: Page,
  options: {
    snapshot?: Snapshot;
    initializeFailures?: number;
    memoizeReads?: boolean;
    transportFault?: (name: string, args: Record<string, unknown>) => boolean;
    toolError?: { name: string; message: string };
    readItem?: (detail: Detail) => Promise<Detail>;
    readReviews?: (id: string) => Promise<PRReviews>;
    reports?: IssueReport[];
    listReports?: (
      result: ReportPage,
      args: Record<string, unknown>,
    ) => Promise<ReportPage>;
    readReport?: (report: IssueReport) => Promise<IssueReport>;
    addEvidence?: (
      report: IssueReport,
      args: Record<string, unknown>,
    ) => Promise<ToolResult>;
    resumeReport?: (report: IssueReport) => Promise<IssueReport>;
    failPreparation?: boolean;
    failPriority?: boolean;
    failSave?: boolean;
    beforeSave?: () => Promise<void>;
    beforeMessage?: () => Promise<void>;
    queueMessages?: boolean;
    failReadRun?: boolean;
    loseStartResponse?: boolean;
    beforeStartResponse?: () => Promise<void>;
    rejectStart?: boolean;
    transcript?: JournalEvent[];
    theme?: "light" | "dark";
    denyOpenLink?: boolean;
    failOpenLink?: boolean;
    surface?: "settings";
  } = {},
) {
  const state = options.snapshot ?? initial();
  const openedLinks: string[] = [];
  await page.exposeFunction("hostOpenLink", (params: { url: string }) => {
    openedLinks.push(params.url);
    if (options.failOpenLink) throw new Error("Host link unavailable");
    return options.denyOpenLink ? { isError: true } : {};
  });
  let relationships: Detail["relationships"] = [];
  const calls: { name: string; arguments: Record<string, unknown> }[] = [];
  const pluginOperations: {
    id: string;
    action: string;
    target: string;
    status: string;
    error?: string;
  }[] = [];
  const pluginRows = [
    {
      id: "review-kit@fixture",
      name: "review-kit",
      marketplace: "fixture",
      installed: true,
      enabled: true,
      installedVersion: "1.0.0",
      availableVersion: "2.0.0",
      updateStatus: "available",
      sourceType: "local",
      authPolicy: "ON_USE",
      installPolicy: "AVAILABLE",
    },
    {
      id: "extra-kit@fixture",
      name: "extra-kit",
      marketplace: "fixture",
      installed: false,
      enabled: false,
      installedVersion: "",
      availableVersion: "1.0.0",
      updateStatus: "not-installed",
      sourceType: "local",
      authPolicy: "ON_USE",
      installPolicy: "AVAILABLE",
    },
  ];
  const reports = options.reports ?? [];
  await page.exposeFunction(
    "hostCall",
    async (p: { name: string; arguments: Record<string, unknown> }) => {
      calls.push(p);
      const a = p.arguments;
      if (options.transportFault?.(p.name, a))
        throw new Error("McpServerError: Connection failed.");
      if (options.toolError?.name === p.name)
        return {
          isError: true,
          content: [{ type: "text", text: options.toolError.message }],
        };
      let data: unknown = null;
      switch (p.name) {
        case "open_settings":
          data = {
            ...structuredClone(state),
            inbox: [],
            runs: [],
            attention: [],
          };
          break;
        case "read_snapshot":
        case "open_factory":
          data = structuredClone(state);
          break;
        case "read_changes":
          data = {
            events:
              state.cursor > Number(a.after)
                ? [
                    {
                      seq: state.cursor,
                      kind: "run.state",
                      entity: "run-1",
                      data: {},
                      at: new Date().toISOString(),
                    },
                  ]
                : [],
            cursor: state.cursor,
            hasMore: false,
          };
          break;
        case "list_issue_reports": {
          const rows = reports
            .filter(
              (r) =>
                (!a.status || r.processing === a.status) &&
                (!a.component || r.component === a.component) &&
                (!a.version || r.installedVersion === a.version) &&
                (!a.search ||
                  (r.observation.surface + r.observation.actual)
                    .toLowerCase()
                    .includes(String(a.search).toLowerCase())) &&
                (!a.cursor || r.id > String(a.cursor)),
            )
            .sort((a, b) => a.id.localeCompare(b.id));
          const limit = Number(a.limit || 30);
          const result = {
            items: structuredClone(rows.slice(0, limit)),
            nextCursor: rows.length > limit ? rows[limit - 1].id : "",
          };
          data = options.listReports
            ? await options.listReports(result, a)
            : result;
          break;
        }
        case "get_issue_report": {
          const report = structuredClone(reports.find((r) => r.id === a.id)!);
          data = options.readReport ? await options.readReport(report) : report;
          break;
        }
        case "add_issue_evidence": {
          const report = reports.find((r) => r.id === a.id)!;
          if (options.addEvidence) return options.addEvidence(report, a);
          report.additions.push(String(a.text));
          report.processing = "queued";
          state.cursor++;
          data = structuredClone(report);
          break;
        }
        case "resume_issue_report": {
          const report = reports.find((r) => r.id === a.id)!;
          report.processing = "investigating";
          report.message = "Recovery admitted";
          state.cursor++;
          const result = structuredClone(report);
          data = options.resumeReport
            ? await options.resumeReport(result)
            : result;
          break;
        }
        case "refresh_issue_reports":
          state.cursor++;
          data = null;
          break;
        case "read_pr_reviews":
          data = options.readReviews ? await options.readReviews(String(a.id)) : {itemId:String(a.id),reviews:[],syncedAt:new Date().toISOString(),lastAttempt:new Date().toISOString()};
          break;
        case "read_item": {
          const entry = state.inbox.find((entry) => entry.item.id === a.id)!;
          const detail = structuredClone({
            item: entry.item,
            user: entry.user,
            relationships: relationships.filter((link) => link.source === a.id),
            tickets: [],
          });
          data = options.readItem ? await options.readItem(detail) : detail;
          break;
        }
        case "item_action": {
          const entry = state.inbox.find((entry) => entry.item.id === a.id)!;
          if (a.action === "read") {
            entry.user.readRevision = String(a.revision);
            entry.unread = entry.item.revision !== a.revision;
          } else if (a.action === "unread") {
            entry.user.readRevision = "";
            entry.unread = true;
          } else if (a.action === "acknowledge") {
            entry.user.new = false;
          } else if (a.action === "acted") {
            entry.user.actions.push({
              id: `action-${state.cursor}`,
              kind: "plugin",
              at: new Date().toISOString(),
            });
          } else if (a.action === "priority") {
            if (options.failPriority)
              return {
                isError: true,
                content: [{ type: "text", text: "Priority save failed" }],
              };
            entry.user.override = a.priority as number | null;
            entry.priority = entry.user.override ?? 1;
            entry.prioritySource =
              entry.user.override === null ? "label" : "override";
          }
          state.cursor++;
          break;
        }
        case "set_relationship":
          relationships = relationships.filter(
            (link) => link.source !== a.source || link.target !== a.target,
          );
          if (!a.remove)
            relationships.push({
              id: `${a.source}->${a.target}`,
              source: String(a.source),
              target: String(a.target),
              manual: true,
              evidence: "",
            });
          state.cursor++;
          break;
        case "read_plugin_admin": {
          const rows = pluginRows.filter(
            (p) =>
              (a.available || p.installed) &&
              p.id.includes(String(a.query ?? "")),
          );
          data = {
            inventory: {
              checkedAt: new Date().toISOString(),
              plugins: structuredClone(rows),
              marketplaces: [{ name: "fixture", sourceType: "local" }],
            },
            operations: structuredClone(pluginOperations),
            total: rows.length,
            offset: 0,
          };
          break;
        }
        case "manage_codex_plugins": {
          const op = {
            id: `operation-${pluginOperations.length + 1}`,
            action: String(a.action),
            target: String(a.target ?? ""),
            status: "succeeded",
          };
          pluginOperations.unshift(op);
          const row = pluginRows.find((p) => p.id === a.target);
          if (row && a.action === "update") {
            row.installedVersion = row.availableVersion;
            row.updateStatus = "current";
          }
          if (row && a.action === "remove") {
            row.installed = false;
            row.enabled = false;
            row.installedVersion = "";
            row.updateStatus = "not-installed";
          }
          if (row && a.action === "install") {
            row.installed = true;
            row.enabled = true;
            row.installedVersion = row.availableVersion;
            row.updateStatus = "current";
          }
          data = op;
          state.cursor++;
          break;
        }
        case "sync_repositories":
          state.cursor++;
          break;
        case "save_repository":
          await options.beforeSave?.();
          if (options.failSave)
            return {
              isError: true,
              content: [{ type: "text", text: "Repository unavailable" }],
            };
          state.repositories = [
            {
              paused: Boolean(a.paused),
              autoReview: a.autoReview as boolean | null,
              reviewProfileId: String(a.reviewProfileId ?? ""),
              implementationProfileId: String(a.implementationProfileId ?? ""),
              reviewPrompt: String(a.reviewPrompt ?? ""),
              id: String(a.id),
              checkout: String(a.checkout),
              base: String(a.base),
              lastSync: "0001-01-01",
              error: "",
            },
          ];
          state.cursor++;
          break;
        case "prepare_launch": {
          if (options.failPreparation)
            return {
              isError: true,
              content: [{ type: "text", text: "Fixture preparation denied" }],
            };
          const run: Run = {
            id: `run-${state.runs.length + 1}`,
            itemId: String(a.itemId),
            profile: {
              ...state.profiles.find((p) => p.id === a.profileId)!,
              model: String(a.model),
              effort: String(a.effort),
            },
            prompt: String(a.prompt),
            skills: [],
            worktree: `/local/worktrees/run-${state.runs.length + 1}`,
            branch: `code-factory/run-${state.runs.length + 1}`,
            base: "HEAD",
            threadId: "",
            turnId: "",
            status: "prepared",
            error: "",
            created: new Date().toISOString(),
          };
          state.runs.unshift(run);
          data = run;
          state.cursor++;
          break;
        }
        case "read_run":
          if (options.failReadRun)
            return {
              isError: true,
              content: [{ type: "text", text: "Status unavailable" }],
            };
          data = {
            run: state.runs.find((r) => r.id === a.id),
            attention: state.attention.filter((r) => r.runId === a.id),
          };
          break;
        case "save_settings":
          state.config = a as unknown as Snapshot["config"];
          state.cursor++;
          break;
        case "start_run":
          if (options.rejectStart)
            return {
              isError: true,
              content: [{ type: "text", text: "Start not accepted" }],
            };
          state.runs[0].status = "waiting";
          state.runs[0].threadId = "thread-1";
          state.runs[0].turnId = "turn-1";
          state.attention = [
            {
              id: "attention-1",
              runId: state.runs[0].id,
              method: "item/tool/requestUserInput",
              blocking: true,
              status: "pending",
              params: {
                questions: [
                  {
                    id: "scope",
                    header: "Scope",
                    question: "Which fix scope?",
                    isOther: true,
                    options: [
                      { label: "Focused", description: "One reconnection fix" },
                      { label: "Broad", description: "All connection issues" },
                    ],
                  },
                ],
              },
            },
          ];
          data = state.runs[0];
          state.cursor++;
          await options.beforeStartResponse?.();
          if (options.loseStartResponse)
            return {
              isError: true,
              content: [{ type: "text", text: "Start response lost" }],
            };
          break;
        case "read_transcript": {
          if (options.transcript) {
            const events = options.transcript
              .filter((event) => event.seq > Number(a.after))
              .slice(0, Number(a.limit) || 100);
            data = {
              events,
              cursor: events.at(-1)?.seq ?? Number(a.after),
              hasMore: options.transcript.some(
                (event) => event.seq > (events.at(-1)?.seq ?? Number(a.after)),
              ),
            };
          } else
            data = {
              events:
                Number(a.after) === 0
                  ? [
                      {
                        seq: state.cursor,
                        kind: "run.output",
                        entity: "run-1",
                        data: {
                          method: "item/agentMessage/delta",
                          params: { delta: "Inspecting reconnect logic" },
                        },
                        at: new Date().toISOString(),
                      },
                    ]
                  : [],
              cursor: state.cursor,
              hasMore: false,
            };
          break;
        }
        case "answer_request": {
          const request = state.attention.find(
            (request) => request.id === a.id,
          )!;
          state.attention = state.attention.filter(
            (request) => request.id !== a.id,
          );
          const run = state.runs.find((run) => run.id === request.runId)!;
          run.status = state.attention.some(
            (request) => request.runId === run.id && request.blocking,
          )
            ? "waiting"
            : "idle";
          if (run.status === "idle") run.turnId = "";
          state.cursor++;
          break;
        }
        case "dismiss_question": {
          state.attention = state.attention.filter(
            (request) => request.id !== a.id,
          );
          state.cursor++;
          break;
        }
        case "save_profile":
          state.profiles = [
            ...state.profiles.filter((p) => p.id !== a.id),
            a as unknown as Snapshot["profiles"][0],
          ];
          state.cursor++;
          break;
        case "save_skill":
          state.skills = [
            {
              id: "managed-fixture",
              name: String(a.name),
              content: String(a.content),
              path: "/local/skill",
              managed: true,
            },
          ];
          state.cursor++;
          break;
        case "discover_skills":
          state.cursor++;
          break;
        case "diagnostics":
          data = {
            version: "fixture",
            at: "2026-10-21T00:00:00Z",
            checks: [
              {
                id: "fixture-check",
                state: "verified",
                detail: "Fixture connection healthy",
                at: "2026-10-21T00:00:00Z",
              },
            ],
          };
          break;
        case "set_jira_credential":
          break;
        case "send_message": {
          if (options.beforeMessage) await options.beforeMessage();
          if (options.queueMessages) {
            state.cursor++;
            options.transcript?.push({
              seq: state.cursor,
              kind: "run.output",
              entity: String(a.id),
              at: new Date().toISOString(),
              data: { method: "codefactory/messageDelivery", params: { itemId: "saved-message", state: "queued" } },
            });
            data = { delivery: "accepted", messageId: "saved-message", messageDelivery: { state: "queued" } };
            break;
          }
          const run = state.runs.find((r) => r.id === a.id);
          if (run && ["interrupted", "failed"].includes(run.status)) {
            run.status = "running";
            run.turnId = "restored-turn";
          }
          state.cursor++;
          break;
        }
        default:
          data = {};
      }
      return { content: [], structuredContent: { data } };
    },
  );
  await page.route("**/settings-ui", async (route) => {
    const response = await route.fetch({ url: "http://127.0.0.1:4173/" });
    await route.fulfill({
      response,
      body: (await response.text()).replace(
        "<html",
        '<html data-code-factory-view="settings"',
      ),
    });
  });
  await page.route("**/host", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><iframe title="Code Factory app" src="${options.surface === "settings" ? "/settings-ui" : "/"}" style="width:100%;height:95vh;border:0"></iframe><script>const readCache=new Map();window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;let result={};if(m.method==='ui/initialize'){window.initializationCount=(window.initializationCount||0)+1;if(window.initializationCount<=${options.initializeFailures ?? 0}){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:'McpServerError: Connection failed.'}},'*');return}result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Work test host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:${JSON.stringify(options.theme ?? "light")},displayMode:'inline'}}}else if(m.method==='tools/call'){try{const key=JSON.stringify(m.params);if(${options.memoizeReads ?? false}&&['read_snapshot','read_changes'].includes(m.params.name)){if(readCache.has(key))result=readCache.get(key);else{result=await window.hostCall(m.params);readCache.set(key,structuredClone(result))}}else result=await window.hostCall(m.params)}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*');return}}else if(m.method==='ui/open-link'){try{result=await window.hostOpenLink(m.params)}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*');return}}e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')});</script>`,
    }),
  );
  await page.goto("/host");
  const ui = page.frameLocator('iframe[title="Code Factory app"]');
  await expect(
    ui.getByLabel(
      options.surface === "settings" ? "GitHub repository" : "Search inbox",
    ),
  ).toBeVisible();
  return { ui, state, calls, openedLinks, openLinks: openedLinks };
}
async function showSettings(page: Page) {
  await page.locator("iframe").evaluate((frame: HTMLIFrameElement) => {
    frame.src = "/settings-ui";
  });
  await expect(
    page
      .frameLocator("iframe")
      .getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
}
async function openItem(ui: FrameLocator, name: RegExp) {
  if (!(await ui.locator(".item-list").count()))
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await ui.getByRole("button", { name }).click();
  await showItemDetails(ui);
}
async function showItemDetails(ui: FrameLocator) {
  const summary = ui.locator(".item-details > summary");
  await expect(summary).toBeVisible();
  if (await summary.count()) {
    if (
      !(await summary
        .locator("..")
        .evaluate((e) => (e as HTMLDetailsElement).open))
    )
      await summary.click();
  }
}
async function showAgents(ui: FrameLocator) {
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await ui.getByLabel("Inbox filter").selectOption("all");
  const retainedRows = ui.locator(".inbox-row").filter({
    has: ui
      .getByRole("status", { name: "Work status" })
      .filter({ hasNotText: "Not started" }),
  });
  await (
    (await retainedRows.count())
      ? retainedRows.first().getByRole("button")
      : ui.locator(".item-list button.item").first()
  ).click();
  await showItemDetails(ui);
  const summary = ui.getByText("Work history", { exact: true });
  if (
    !(await summary
      .locator("..")
      .evaluate((e) => (e as HTMLDetailsElement).open))
  )
    await summary.click();
}
async function openWaiting(ui: FrameLocator) {
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .first()
    .click();
}
async function linkTarget(ui: FrameLocator) {
  await showItemDetails(ui);
  const input = ui.getByLabel("Link target");
  if (!(await input.isVisible()))
    await ui.getByText("Relationships", { exact: true }).click();
  return input;
}
async function triage(ui: FrameLocator, name: string) {
  await showItemDetails(ui);
  const button = ui.getByRole("button", { name, exact: true });
  if (!(await button.isVisible()))
    await ui.getByText("Actions", { exact: true }).click();
  await button.click();
}
async function tabTo(page: Page, target: Locator) {
  await target.waitFor({ state: "attached" });
  if (!(await target.isVisible())) {
    const details = target.locator(
      "xpath=ancestor::details[contains(@class, 'item-details')]",
    );
    if (await details.count())
      await details.locator(":scope > summary").click();
  }
  for (let attempt = 0; attempt < 30; attempt++) {
    if (
      await target.evaluate(
        (element) => element === element.ownerDocument.activeElement,
      )
    )
      return;
    await page.keyboard.press("Tab");
  }
  await expect(target).toBeFocused();
}
async function scanOwnedApp(page: Page, state: string) {
  const results = await new AxeBuilder({ page })
    // Include the owned document through its frame path. The deliberately
    // minimal fake host is outside this scope; every widget inside remains in.
    .include({ fromFrames: ['iframe[title="Code Factory app"]', "html"] })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  const reportPath = test.info().outputPath(`axe-${state}.json`);
  await writeFile(reportPath, JSON.stringify(results, null, 2));
  await test.info().attach(`axe-${state}.json`, {
    path: reportPath,
    contentType: "application/json",
  });
  expect(results.testEngine.version).toBe("4.14.0");
  expect(
    results.passes.some(
      (rule) => rule.id === "color-contrast" && rule.nodes.length > 0,
    ),
  ).toBe(true);
  expect(
    results.passes.some((rule) =>
      rule.nodes.some(
        (node) =>
          node.target.length > 1 &&
          JSON.stringify(node.target[0]).includes("iframe"),
      ),
    ),
  ).toBe(true);
  expect(
    results.violations,
    `${state}: ${JSON.stringify(
      results.violations.map((rule) => ({
        id: rule.id,
        nodes: rule.nodes.map((node) => ({
          target: node.target,
          failureSummary: node.failureSummary,
        })),
      })),
      null,
      2,
    )}`,
  ).toEqual([]);
}

for (const theme of ["light", "dark"] as const) {
  test(`Axe WCAG A/AA scans the owned app iframe across core states in ${theme} theme`, async ({
    page,
  }) => {
    const { ui } = await host(page, { theme });
    await expect(ui.locator("html")).toHaveAttribute("data-theme", theme);
    // Work can inject this after the app stylesheet. Keep the host's transparent
    // document while painting the owned application surfaces in the host theme.
    await ui.locator("html").evaluate((html) => {
      const style = html.ownerDocument.createElement("style");
      // Force the observed host result despite Vite's higher-specificity :root
      // stylesheet; the regression is transparent html with themed text.
      style.textContent = "html { background-color: transparent !important; }";
      html.ownerDocument.head.append(style);
    });
    await expect(ui.locator("html")).toHaveCSS(
      "background-color",
      "rgba(0, 0, 0, 0)",
    );
    const background =
      theme === "dark" ? "rgb(23, 32, 28)" : "rgb(246, 248, 245)";
    for (const selector of ["body", "#root", "main"]) {
      await expect(ui.locator(selector)).toHaveCSS(
        "background-color",
        background,
      );
    }
    await scanOwnedApp(page, `${theme}-inbox`);
    await openItem(ui, /Fix reconnect regression/);
    await (await linkTarget(ui)).fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect(
      ui.locator(".detail p").filter({ hasText: "owner/repo#42" }),
    ).toBeVisible();
    await triage(ui, "Record action");
    await ui.getByText("Your action history", { exact: true }).click();
    await expect(
      ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-detail`);
    await ui.getByRole("button", { name: "New run", exact: true }).click();
    await expect(
      ui.getByRole("dialog", { name: "Launch agent" }),
    ).toBeVisible();
    await ui.getByRole("button", { name: "Edit", exact: true }).click();
    await scanOwnedApp(page, `${theme}-launch`);
    await ui
      .getByRole("button", { name: "Review launch", exact: true })
      .click();
    await expect(
      ui.getByRole("heading", { name: "Review agent launch" }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-review`);
    await ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true })
      .click();
    await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
    await scanOwnedApp(page, `${theme}-agents`);
    await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
    await ui.getByRole("radio", { name: /Focused/ }).check();
    await expect(
      ui.getByRole("button", { name: "Answer", exact: true }),
    ).toBeEnabled();
    await scanOwnedApp(page, `${theme}-question`);
    await page.keyboard.press("Escape");
    await showSettings(page);
    await expect(
      ui.getByText("Connection diagnostics", { exact: true }),
    ).toBeVisible();
    for (const section of [
      "Priorities & notifications",
      "Launch profiles",
      "Managed skills",
      "Connection diagnostics",
    ])
      await ui.getByText(section, { exact: true }).click();
    await scanOwnedApp(page, `${theme}-settings`);
  });
}
test("keyboard launch review traps focus, preserves edits and restores its opener on Escape", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  const opener = ui.getByRole("button", { name: "New run", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  const close = dialog.getByRole("button", { name: "Close launch" });
  await expect(close).toBeFocused();
  await expect(close).toHaveCSS("outline-style", "solid");
  await expect(close).toHaveCSS("outline-width", "3px");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await tabTo(page, dialog.getByRole("button", { name: "Edit", exact: true }));
  await page.keyboard.press("Enter");
  const prompt = dialog.getByLabel("Task prompt");
  await tabTo(page, prompt);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("Keep this keyboard launch draft.");
  await expect(prompt).toHaveCSS("outline-width", "3px");
  const snapshots = calls.filter(
    (call) => call.name === "read_snapshot",
  ).length;
  state.cursor++;
  await expect
    .poll(() => calls.filter((call) => call.name === "read_snapshot").length)
    .toBeGreaterThan(snapshots);
  await expect(prompt).toBeFocused();
  await expect(prompt).toHaveValue("Keep this keyboard launch draft.");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(
    ui.getByRole("heading", { name: "Review agent launch" }),
  ).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText("Ask before publishing");
  await expect(close).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  expect(calls.filter((call) => call.name === "prepare_launch")).toHaveLength(
    2,
  );
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(0);
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toContainText("Connected");
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveAttribute("aria-live", "polite");
});

test("keyboard attention stays contained through updates and Escape restores the pending opener", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  await tabTo(page, ui.getByRole("button", { name: "New run", exact: true }));
  await page.keyboard.press("Enter");

  await tabTo(
    page,
    ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true }),
  );
  await page.keyboard.press("Enter");
  const opener = ui.getByRole("button", { name: /Waiting for your answer/ });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Agent needs attention" });
  const close = dialog.getByRole("button", { name: "Close attention" });
  await expect(close).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Open agent chat" }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await page.keyboard.press("Tab");
  const focused = dialog.getByRole("radio", { name: /Focused/ });
  await expect(focused).toBeFocused();
  await page.keyboard.press("Space");
  await expect(focused).toBeChecked();
  await expect(focused).toHaveCSS("outline-width", "3px");
  state.attention[0].params.questions![0].options![0].description =
    "Updated focused guidance";
  state.cursor++;
  await expect(dialog.getByText("Updated focused guidance")).toBeVisible();
  await expect(focused).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  expect(calls.filter((call) => call.name === "answer_request")).toHaveLength(
    0,
  );
  await page.keyboard.press("Enter");
  await expect(close).toBeFocused();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Space");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Answer", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(calls.filter((call) => call.name === "answer_request")).toHaveLength(
    1,
  );
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Inactive",
  );
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveAttribute(
    "aria-live",
    "polite",
  );
});

test("launch preparation failure is announced inside the dialog without losing keyboard edits", async ({
  page,
}) => {
  const options = { failPreparation: true };
  const { ui, calls } = await host(page, options);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  const opener = ui.getByRole("button", { name: "New run", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(dialog.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  options.failPreparation = false;
  await dialog.getByRole("button", { name: "Retry preparation" }).click();
  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  options.failPreparation = true;
  await tabTo(page, dialog.getByLabel("Task prompt"));
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("Retain this failed preparation draft.");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(dialog.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  await expect(dialog.getByLabel("Task prompt")).toHaveValue(
    "Retain this failed preparation draft.",
  );
  await expect(
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  ).toBeEnabled();
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(0);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});
test("item actions refresh visible history while unread, New and drafts remain independent", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  const row = ui.getByRole("button", { name: /Fix reconnect regression/ });
  await row.click();
  await expect(ui.locator(".item-list")).toHaveCount(0);
  await expect.poll(() => state.inbox[0].unread).toBe(false);
  expect(state.inbox[0].user.new).toBe(true);
  await (await linkTarget(ui)).fill("owner/repo#draft");
  await ui.getByLabel("Priority override").selectOption("2");
  await triage(ui, "Record action");
  await expect(
    ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
  ).toHaveCount(1);
  expect(state.inbox[0].user.new).toBe(true);
  await triage(ui, "Mark unread");
  await expect.poll(() => state.inbox[0].unread).toBe(true);
  await triage(ui, "Acknowledge new");
  await expect.poll(() => state.inbox[0].user.new).toBe(false);
  expect(state.inbox[0].unread).toBe(true);
  await expect(ui.getByLabel("Link target")).toHaveValue("owner/repo#draft");
  await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  expect(
    calls.filter(
      (c) => c.name === "item_action" && c.arguments.action === "read",
    ),
  ).toHaveLength(1);
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await expect(row.locator(".dot")).toHaveCount(1);
  await expect(row.locator("..").getByText("New", { exact: true })).toHaveCount(
    0,
  );
  await expect(row.locator("..")).toContainText("Acted on");
  expect(
    calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
  ).toHaveLength(0);
});

test("unlink removes the visible relationship without clearing unrelated detail drafts", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await openItem(ui, /Fix reconnect regression/);
  await (await linkTarget(ui)).fill("owner/repo#42");
  await ui.getByRole("button", { name: "Add local link", exact: true }).click();
  const link = ui.locator(".detail p").filter({ hasText: "owner/repo#42" });
  await expect(link).toContainText("Local link");
  await (await linkTarget(ui)).fill("owner/repo#next-draft");
  await ui.getByLabel("Priority override").selectOption("3");
  await link.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(link).toHaveCount(0);
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 1 }),
  ).toBeVisible();
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#next-draft",
  );
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  expect(
    calls.filter(
      (call) => call.name === "set_relationship" && call.arguments.remove,
    ),
  ).toHaveLength(1);
});

test("a delayed relationship refresh cannot replace a later selection or its drafts", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second retained item";
  snapshot.inbox.push(second);
  let release!: () => void;
  let delayed = false;
  let completed = false;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui } = await host(page, {
    snapshot,
    readItem: async (detail) => {
      if (detail.item.id === "owner/repo#1" && detail.relationships.length) {
        delayed = true;
        await gate;
        completed = true;
      }
      return detail;
    },
  });
  try {
    await openItem(ui, /Fix reconnect regression/);
    await (await linkTarget(ui)).fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect.poll(() => delayed).toBe(true);
    await openItem(ui, /Second retained item/);
    await expect(
      ui.getByRole("heading", { name: "Second retained item", level: 1 }),
    ).toBeVisible();
    await (await linkTarget(ui)).fill("owner/repo#second-draft");
    await ui.getByLabel("Priority override").selectOption("2");
    release();
    await expect.poll(() => completed).toBe(true);
    await ui
      .locator("body")
      .evaluate(
        () =>
          new Promise<void>((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
          ),
      );
    await expect(
      ui.getByRole("heading", { name: "Second retained item", level: 1 }),
    ).toBeVisible();
    await expect(ui.getByLabel("Link target")).toHaveValue(
      "owner/repo#second-draft",
    );
    await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  } finally {
    release();
  }
});
test("triage does not launch, launch edits and attention answer survive reopening", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await openItem(ui, /Fix reconnect regression/);
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 1 }),
  ).toBeVisible();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "item_action" && c.arguments.revision === "rev-1",
      ),
    )
    .toBe(true);
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui.getByRole("button", { name: "Edit", exact: true }).click();
  await ui
    .getByLabel("Task prompt")
    .fill("Investigate the reconnect race with a focused test.");
  await ui.getByText("Advanced", { exact: true }).click();
  await ui.getByLabel("Model", { exact: true }).fill("local-model");
  await ui.getByRole("button", { name: "Review launch" }).click();
  await expect(
    ui.getByRole("heading", { name: "Review agent launch" }),
  ).toBeVisible();
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "prepare_launch").at(-1)?.arguments
          .prompt,
    )
    .toBe("Investigate the reconnect race with a focused test.");
  expect(
    calls.filter((c) => c.name === "prepare_launch").at(-1)?.arguments.model,
  ).toBe("local-model");
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toBeVisible();
  const question = ui.getByRole("group", { name: "Which fix scope?" });
  await expect(question.getByRole("radio")).toHaveCount(2);
  await expect(
    question.getByRole("radio", { name: "Focused One reconnection fix" }),
  ).toBeVisible();
  await expect(
    question.getByRole("radio", { name: "Broad All connection issues" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(0);

  // Replace the host document and iframe while the original request is pending.
  // The host's Node-side snapshot survives; no second launch seeds the new view.
  const snapshotsBeforeReopen = calls.filter(
    (c) => c.name === "read_snapshot",
  ).length;
  await page.reload();
  await expect(ui.getByLabel("Search inbox")).toBeVisible();
  expect(
    calls.filter((c) => c.name === "read_snapshot").length,
  ).toBeGreaterThan(snapshotsBeforeReopen);
  await expect(
    ui.getByRole("button", { name: /^Messages\s*1$/ }),
  ).toBeVisible();
  await showAgents(ui);
  await expect(ui.getByRole("button", { name: /Investigation ·/ })).toHaveCount(
    1,
  );
  await ui.getByRole("button", { name: /Investigation ·/ }).click();
  await ui.getByText("Details", { exact: true }).click();
  await expect(
    ui.getByText("/local/worktrees/run-2", { exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await expect(question.getByRole("radio")).toHaveCount(2);
  await expect(
    question.getByRole("radio", { name: "Focused One reconnection fix" }),
  ).toBeVisible();
  await expect(
    question.getByRole("radio", { name: "Broad All connection issues" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(2);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(0);
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "answer_request").map((c) => c.arguments),
  ).toEqual([
    {
      id: "attention-1",
      answer: { answers: { scope: { answers: ["Focused"] } } },
    },
  ]);
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await showAgents(ui);
  await ui.getByRole("button", { name: /Investigation ·/ }).click();
  await ui.getByLabel("Message agent").fill("Explain the test result.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "send_message").length)
    .toBe(1);
  expect(calls.find((c) => c.name === "send_message")?.arguments).toEqual({
    id: "run-2",
    text: "Explain the test result.",
    expectedTurn: "",
  });
  expect(calls.filter((c) => c.name === "start_run").length).toBe(1);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(2);
});
test("settings save repository and priority rows without unused integrations", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { surface: "settings" });
  await expect(ui.getByLabel("Assessment prompt")).toBeHidden();
  await ui.getByLabel("GitHub repository").fill("owner/repo");
  await ui
    .getByLabel("Existing checkout (optional)")
    .fill("/home/user/project");
  await ui
    .getByRole("button", { name: "Save repository", exact: true })
    .click();
  await expect(
    ui.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Issues", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Edit repository owner/repo", exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("Integrations", { exact: true })).toHaveCount(0);
  await expect(ui.getByLabel("Jira API token")).toHaveCount(0);
  expect(calls.filter((c) => c.name === "set_jira_credential")).toHaveLength(0);
  await ui.getByText("Priorities & notifications", { exact: true }).click();
  await ui.getByLabel("Priority label 1", { exact: true }).fill("urgent");
  await ui.getByLabel("Mapped priority 1", { exact: true }).selectOption("0");
  await ui
    .getByLabel("Question helper model (optional)")
    .fill("fixture-small-model");
  await ui.getByRole("button", { name: "Save settings", exact: true }).click();
  const form = ui
    .getByRole("button", { name: "Save settings", exact: true })
    .locator("xpath=ancestor::form");
  await expect(form.getByRole("status")).toHaveText("Saved.");
  expect(
    calls.find((c) => c.name === "save_settings")?.arguments.priorityLabels,
  ).toEqual({ urgent: 0, P1: 1, P2: 2, P3: 3 });
  expect(
    calls.find((c) => c.name === "save_settings")?.arguments
      .questionHelperModel,
  ).toBe("fixture-small-model");
});

function retained(
  snapshot: Snapshot,
  id: string,
  status = "idle",
  created = "2026-10-21T00:00:00Z",
): Run {
  return {
    id,
    itemId: snapshot.inbox[0].item.id,
    profile: structuredClone(snapshot.profiles[0]),
    prompt: "Retained task",
    skills: [],
    worktree: `/local/${id}`,
    branch: `factory/${id}`,
    base: "HEAD",
    threadId: `thread-${id}`,
    turnId: "",
    status,
    error: "",
    created,
  };
}

test("item switching restores saved priorities and keeps relationship drafts by item", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].user.override = 3;
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second item";
  second.user.override = 0;
  snapshot.inbox.push(second);
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  await (await linkTarget(ui)).fill("owner/repo#draft-one");
  await openItem(ui, /Second item/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("0");
  await expect(ui.getByLabel("Link target")).toHaveValue("");
  await (await linkTarget(ui)).fill("owner/repo#draft-two");
  await ui.getByLabel("Priority override").selectOption("2");
  await expect(ui.getByLabel("Priority override")).toBeEnabled();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-one",
  );
  await showAgents(ui);
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-one",
  );
  await openItem(ui, /Second item/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-two",
  );
  for (const priority of ["0", "1", "2", "3", ""]) {
    await ui.getByLabel("Priority override").selectOption(priority);
    await expect(ui.getByLabel("Priority override")).toBeEnabled();
  }
  expect(
    calls
      .filter(
        (c) => c.name === "item_action" && c.arguments.action === "priority",
      )
      .map((c) => c.arguments),
  ).toEqual(
    [2, 0, 1, 2, 3, null].map((priority) => ({
      id: second.item.id,
      action: "priority",
      priority,
    })),
  );
});

test("failed immediate priority saves restore the saved value with an inline error", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].user.override = 1;
  const { ui } = await host(page, { snapshot, failPriority: true });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByLabel("Priority override").selectOption("0");
  await expect(ui.getByLabel("Priority override")).toHaveValue("1");
  await expect(ui.locator(".detail").getByRole("alert")).toContainText(
    "Priority save failed",
  );
});

test("empty filtered inbox offers Clear filters and hides zero attention", async ({
  page,
}) => {
  const { ui } = await host(page);
  await expect(ui.getByRole("button", { name: /^Messages\s*1$/ })).toHaveCount(
    0,
  );
  await ui.getByLabel("Search inbox").fill("no such item");
  await expect(
    ui.getByText("No matching items", { exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Clear filters", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  ).toBeVisible();
  await expect(ui.getByLabel("Search inbox")).toHaveValue("");
});

test("Open agent targets the newest retained run and keeps message drafts across tabs and runs", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [
    retained(snapshot, "old", "idle", "2026-10-20T00:00:00Z"),
    retained(snapshot, "latest"),
    retained(snapshot, "draft", "prepared", "2026-10-22T00:00:00Z"),
    retained(snapshot, "removed", "cleaned", "2026-10-23T00:00:00Z"),
  ];
  const { ui, calls } = await host(page, { snapshot });
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Inactive",
  );
  await openItem(ui, /Fix reconnect regression/);
  await expect(
    ui.getByRole("button", { name: "New run", exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Message agent").fill("Draft for the newest run");
  await ui.getByText("Details", { exact: true }).click();
  await expect(ui.getByText("/local/latest", { exact: true })).toBeVisible();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Draft for the newest run",
  );
  await showAgents(ui);
  await ui.locator(".work-history > button.item").first().click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  await ui.getByLabel("Message agent").fill("Draft for the old run");
  await showAgents(ui);
  await ui.locator(".work-history > button.item").nth(1).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Draft for the newest run",
  );
  expect(
    calls.filter((c) =>
      ["prepare_launch", "start_run", "send_message"].includes(c.name),
    ),
  ).toHaveLength(0);
});

test("default launch opens review directly and Draft launches reopens its immutable snapshot newest first", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.profiles[0].mode = "plan";
  snapshot.runs = [
    retained(snapshot, "older-draft", "prepared", "2026-10-20T00:00:00Z"),
  ];
  const { ui, state, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await expect(dialog.getByText("plan", { exact: true })).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText("Ask before publishing");
  await expect(dialog.getByLabel("Task prompt")).toHaveCount(0);
  const prepared = structuredClone(state.runs[0]);
  state.profiles[0].name = "Changed profile";
  state.profiles[0].mode = "default";
  state.profiles[0].sandbox = "workspace-write";
  await dialog.getByRole("button", { name: "Close launch" }).click();
  await showAgents(ui);
  await expect(ui.getByText("Draft launches", { exact: true })).toBeVisible();
  const drafts = ui.getByText("Draft launches", { exact: true }).locator("..");
  await expect(drafts).not.toHaveAttribute("open", "");
  await drafts.locator("summary").click();
  await drafts.getByRole("button").first().click();
  await expect(dialog).toContainText(prepared.profile.name);
  await expect(dialog.getByText("plan", { exact: true })).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText(prepared.prompt);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await dialog
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect
    .poll(() => calls.find((c) => c.name === "start_run")?.arguments)
    .toEqual({ id: prepared.id });
});

test("global attention selects its owning run and returns to its conversation after answering", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Other task";
  snapshot.inbox.push(second);
  snapshot.runs = [
    retained(snapshot, "attention-owner", "waiting"),
    { ...retained(snapshot, "other"), itemId: second.item.id },
  ];
  snapshot.attention = [
    {
      id: "global-question",
      runId: "attention-owner",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "scope",
            header: "Scope",
            question: "Choose scope",
            options: [{ label: "Focused", description: "One fix" }],
          },
        ],
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Other task/);
  await expect(
    ui.getByRole("button", { name: "← Issues", exact: true }),
  ).toBeVisible();
  await openWaiting(ui);
  const dialog = ui.getByRole("dialog");
  await expect(dialog).toContainText(
    "Fix reconnect regression · Investigation",
  );
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "← Messages", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "← Issues", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("heading", {
      name: "Fix reconnect regression",
      exact: true,
      level: 1,
    }),
  ).toBeFocused();
  await expect(ui.getByRole("button", { name: /^Messages\s*1$/ })).toHaveCount(
    0,
  );
  await ui.getByLabel("Message agent").fill("Continue the owning run");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect
    .poll(() => calls.find((c) => c.name === "send_message")?.arguments.id)
    .toBe("attention-owner");
});

test("unverifiable Start disables retry and Edit until read_run locates accepted work", async ({
  page,
}) => {
  const options = { loseStartResponse: true, failReadRun: true };
  const { ui, calls } = await host(page, options);
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  const dialog = ui.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toContainText(
    "Start status is uncertain",
  );
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("button", { name: "Edit", exact: true }),
  ).toBeDisabled();
  options.failReadRun = false;
  await dialog
    .getByRole("button", { name: "Check start status", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
});

test("repository form shows saving and errors beside retained inputs", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui } = await host(page, {
    surface: "settings",
    failSave: true,
    beforeSave: () => gate,
  });
  await ui.getByLabel("GitHub repository").fill("owner/repo");
  const form = ui
    .getByRole("button", { name: "Save repository", exact: true })
    .locator("xpath=ancestor::form");
  try {
    await form.getByRole("button").click();
    await expect(form.getByRole("status")).toHaveText("Saving…");
    await expect(form.getByRole("button")).toBeDisabled();
    release();
    await expect(form.getByRole("alert")).toContainText(
      "Repository unavailable",
    );
    await expect(ui.getByLabel("GitHub repository")).toHaveValue("owner/repo");
    await expect(form.getByRole("button")).toBeEnabled();
  } finally {
    release();
  }
});

for (const theme of ["light", "dark"] as const) {
  test(`readable approval choices preserve payloads in a narrow ${theme} embed`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "approval-owner", "waiting")];
    const decision = {
      acceptWithExecpolicyAmendment: {
        execpolicy_amendment: ["git", "status"],
      },
    };
    const choices = [
      { decision: "accept", label: "Allow once" },
      { decision: "acceptForSession", label: "Allow for this session" },
      { decision: "decline", label: "Deny" },
      { decision: "cancel", label: "Cancel" },
      { decision, label: "Allow and update command policy" },
    ];
    const { ui, state, calls } = await host(page, { snapshot, theme });
    for (const [index, choice] of choices.entries()) {
      state.runs[0].status = "waiting";
      state.attention = [
        {
          id: `approval-${index}`,
          runId: "approval-owner",
          method: "item/commandExecution/requestApproval",
          blocking: true,
          status: "pending",
          params: {
            command: "git status",
            reason: "Inspect working tree",
            cwd: "/local/approval-owner",
            availableDecisions: [choice.decision],
          },
        },
      ];
      state.cursor++;
      await openWaiting(ui);
      const dialog = ui.getByRole("dialog");
      await expect(
        dialog.getByText("git status", { exact: true }),
      ).toBeVisible();
      await expect(
        dialog.getByText("Inspect working tree", { exact: true }),
      ).toBeVisible();
      await expect(dialog.locator("details")).not.toHaveAttribute("open", "");
      await expect(dialog.locator("pre")).toBeHidden();
      expect(
        await dialog.evaluate((el) => {
          const r = el.getBoundingClientRect();
          return r.left >= 0 && r.right <= window.innerWidth;
        }),
      ).toBe(true);
      if (index === 0)
        await page.screenshot({
          path: test.info().outputPath(`approval-${theme}.png`),
        });
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await tabTo(
        page,
        dialog.getByRole("button", { name: choice.label, exact: true }),
      );
      await page.keyboard.press("Enter");
      await expect(dialog).toHaveCount(0);
      expect(
        calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
      ).toEqual({
        id: `approval-${index}`,
        answer: { decision: choice.decision },
      });
    }
    state.runs[0].status = "waiting";
    state.attention = [
      {
        id: "permissions",
        runId: "approval-owner",
        method: "item/permissions/requestApproval",
        blocking: true,
        status: "pending",
        params: {
          reason: "Read project files",
          permissions: { fileSystem: { read: ["/local/project"], write: [] } },
        },
      },
    ];
    state.cursor++;
    await openWaiting(ui);
    await expect(
      ui.getByRole("dialog").getByText("/local/project", { exact: true }),
    ).toBeVisible();
    await ui.getByRole("button", { name: "Allow once", exact: true }).click();
    await expect(ui.getByRole("dialog")).toHaveCount(0);
    expect(
      calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
    ).toEqual({
      id: "permissions",
      answer: {
        permissions: { fileSystem: { read: ["/local/project"], write: [] } },
        scope: "turn",
      },
    });
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await openItem(ui, /Fix reconnect regression/);
    await ui.getByRole("button", { name: "New run", exact: true }).click();
    const launch = ui.getByRole("dialog", { name: "Launch agent" });
    await expect(
      launch.getByRole("button", { name: "Start agent", exact: true }),
    ).toBeEnabled();
    expect(
      await launch.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return r.left >= 0 && r.right <= window.innerWidth;
      }),
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`launch-${theme}.png`),
    });
    await launch.getByRole("button", { name: "Edit", exact: true }).click();
    await launch.getByText("Advanced", { exact: true }).click();
    expect(
      await launch.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return r.left >= 0 && r.right <= window.innerWidth;
      }),
    ).toBe(true);
    await page.keyboard.press("Escape");
    await showSettings(page);
    await ui.getByText("Priorities & notifications", { exact: true }).click();
    expect(
      await ui.locator("body").evaluate((b) => b.scrollWidth <= b.clientWidth),
    ).toBe(true);
  });
}

test("confirmed unaccepted Start allows an explicit retry of the same snapshot", async ({
  page,
}) => {
  const options = { rejectStart: true };
  const { ui, calls } = await host(page, options);
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  const dialog = ui.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toContainText("still prepared");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  options.rejectStart = false;
  await dialog
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments),
  ).toEqual([{ id: "run-1" }, { id: "run-1" }]);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
});

test("collapsed settings editors preserve capabilities and show independent save results", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { surface: "settings" });
  await expect(ui.getByLabel("GitHub repository")).toBeFocused();
  for (const title of [
    "Launch profiles",
    "Managed skills",
    "Connection diagnostics",
  ])
    await ui.getByText(title, { exact: true }).click();
  const profileForm = ui
    .getByRole("button", { name: "Save profile", exact: true })
    .locator("xpath=ancestor::form");
  await profileForm.getByLabel("Name", { exact: true }).fill("Focused review");
  await profileForm
    .getByRole("combobox", { name: /Agent mode/ })
    .selectOption("plan");
  await profileForm.getByRole("button").click();
  await expect(profileForm.getByRole("status")).toHaveText("Saved.");
  expect(calls.find((c) => c.name === "save_profile")?.arguments).toMatchObject(
    {
      name: "Focused review",
      mode: "plan",
      sandbox: "read-only",
      publishing: "ask",
    },
  );
  const skillForm = ui
    .getByRole("button", { name: "Save skill", exact: true })
    .locator("xpath=ancestor::form");
  await skillForm
    .getByLabel("Skill name", { exact: true })
    .fill("focused-review");
  await skillForm.getByRole("button").click();
  await expect(skillForm.getByRole("status")).toHaveText("Saved.");
  expect(calls.find((c) => c.name === "save_skill")?.arguments.name).toBe(
    "focused-review",
  );
  const discovery = ui
    .getByRole("button", { name: "Discover local skills", exact: true })
    .locator("xpath=ancestor::form");
  await discovery.getByRole("button").click();
  await expect(discovery.getByRole("status")).toHaveText("Saved.");
  await ui
    .getByRole("button", { name: "Check connection", exact: true })
    .click();
  await expect(
    ui.getByText("verified · fixture-check: Fixture connection healthy"),
  ).toBeVisible();
  await expect(profileForm.getByRole("status")).toHaveText("Saved.");
});

for (const theme of ["light", "dark"] as const) {
  test(`agent conversation shows human messages and keeps technical activity under Details in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "transcript", "waiting")];
    const params = [
      {
        method: "item/completed",
        params: {
          item: {
            id: "user",
            type: "userMessage",
            content: [{ text: "Please investigate the reconnect failure." }],
          },
        },
      },
      {
        method: "item/agentMessage/delta",
        params: { itemId: "progress", delta: "Checking " },
      },
      {
        method: "item/agentMessage/delta",
        params: { itemId: "progress", delta: "the reconnect path." },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "progress",
            type: "agentMessage",
            phase: "commentary",
            text: "Checking the reconnect path.",
          },
        },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "command",
            type: "commandExecution",
            command: "git status",
            aggregatedOutput: "Command output detail",
          },
        },
      },
      {
        method: "turn/plan/updated",
        params: {
          plan: [{ step: "Inspect reconnect", status: "in_progress" }],
        },
      },
      { method: "turn/diff/updated", params: { diff: "Raw change detail" } },
      {
        method: "item/reasoning/summaryTextDelta",
        params: { itemId: "reasoning", delta: "Internal reasoning summary" },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "files",
            type: "fileChange",
            changes: [{ path: "main.go", diff: "Raw file patch" }],
          },
        },
      },
      {
        method: "provider/chunk",
        params: { delta: "Unstructured output detail" },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "final",
            type: "agentMessage",
            phase: "final_answer",
            text: "The retry condition is fixed and verified.",
          },
        },
      },
      {
        method: "error",
        params: {
          error: {
            message: "Visible failure",
            code: "providerFailure",
            diagnostic: "Diagnostic error detail",
          },
        },
      },
    ];
    const transcript = params.map((data, i) => ({
      seq: i + 1,
      kind: "run.output",
      entity: "transcript",
      data,
      at: "2026-10-21T00:00:00Z",
    })) as JournalEvent[];
    snapshot.attention = [
      {
        id: "visible-question",
        runId: "transcript",
        method: "item/tool/requestUserInput",
        blocking: true,
        status: "pending",
        params: {
          questions: [
            {
              id: "scope",
              header: "Scope",
              question: "Which scope should I use?",
              options: [{ label: "Focused", description: "One fix" }],
            },
          ],
        },
      },
    ];
    const { ui, state, calls } = await host(page, {
      snapshot,
      transcript,
      theme,
    });
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    const conversation = ui.locator(".transcript");
    for (const message of [
      "Please investigate the reconnect failure.",
      "Checking the reconnect path.",
      "The retry condition is fixed and verified.",
      "Visible failure",
    ])
      await expect(
        conversation.getByText(message, { exact: true }),
      ).toBeVisible();
    await expect(
      conversation.getByText("Checking the reconnect path.", { exact: true }),
    ).toHaveCount(1);
    const details = ui.locator(".agent-details");
    await expect(details).not.toHaveAttribute("open", "");
    for (const message of [
      "in_progress: Inspect reconnect",
      "Raw change detail",
      "Internal reasoning summary",
      "main.go\nRaw file patch",
      "Unstructured output detail",
      "git status\nCommand output detail",
    ]) {
      await expect(
        conversation.getByText(message, { exact: true }),
      ).toHaveCount(0);
      await expect(ui.getByText(message, { exact: true })).toBeHidden();
    }
    await expect(
      ui.getByRole("button", { name: /Waiting for your answer/ }),
    ).toBeVisible();
    await page.screenshot({
      path: test.info().outputPath(`concise-conversation-${theme}.png`),
    });
    await scanOwnedApp(page, `concise-conversation-${theme}`);
    await details.locator("summary").focus();
    await page.keyboard.press("Enter");
    await expect(
      ui.getByText("git status\nCommand output detail", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText("Internal reasoning summary", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText("in_progress: Inspect reconnect", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText(/"diagnostic": "Diagnostic error detail"/),
    ).toBeVisible();
    await details.locator("summary").click();
    transcript.push({
      seq: 13,
      kind: "run.output",
      entity: "transcript",
      data: {
        method: "item/agentMessage/delta",
        params: {
          itemId: "next-progress",
          delta: "Verification is still running.",
        },
      },
      at: "2026-10-21T00:00:01Z",
    });
    transcript.push({
      seq: 14,
      kind: "run.output",
      entity: "transcript",
      data: {
        method: "commandExecution/outputDelta",
        params: { itemId: "next-command", delta: "New command noise" },
      },
      at: "2026-10-21T00:00:01Z",
    });
    state.cursor++;
    await expect(
      conversation.getByText("Verification is still running.", { exact: true }),
    ).toBeVisible();
    await expect(details).not.toHaveAttribute("open", "");
    await expect(
      ui.getByText("New command noise", { exact: true }),
    ).toBeHidden();
    await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
    await expect(ui.getByRole("dialog")).toContainText(
      "Which scope should I use?",
    );
    await page.keyboard.press("Escape");
    expect(
      calls.filter((c) =>
        ["start_run", "send_message", "answer_request"].includes(c.name),
      ),
    ).toHaveLength(0);
  });
}

test("technical-only activity leaves a concise waiting state in the conversation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "technical-only", "running")];
  const { ui } = await host(page, {
    snapshot,
    transcript: [
      {
        seq: 1,
        kind: "run.output",
        entity: "technical-only",
        data: {
          method: "item/completed",
          params: {
            item: {
              id: "cmd",
              type: "commandExecution",
              command: "go test ./...",
              aggregatedOutput: "Raw test output",
            },
          },
        },
        at: "2026-10-21T00:00:00Z",
      },
    ],
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.locator(".transcript")).toHaveText(
    "Waiting for an agent update…",
  );
  await expect(
    ui.getByText("go test ./...\nRaw test output", { exact: true }),
  ).toBeHidden();
});

for (const theme of ["light", "dark"] as const) {
  for (const width of [1280, 390]) {
    test(`single-page navigation preserves list context and drafts at ${width}px in ${theme}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 820 });
      const snapshot = initial();
      snapshot.inbox = Array.from({ length: 40 }, (_, index) => {
        const entry = structuredClone(snapshot.inbox[0]);
        entry.item.id = `owner/repo#${index + 1}`;
        entry.item.title = `Item ${index + 1}: investigation`;
        entry.item.body = `Description for item ${index + 1}`;
        return entry;
      });
      snapshot.inbox[0].item.title +=
        ": investigate a lengthy failure description that wraps in a narrow inbox while keeping priority beside the first line";
      snapshot.runs = [
        { ...retained(snapshot, "page-run"), itemId: "owner/repo#30" },
      ];
      const { ui, calls } = await host(page, { snapshot, theme });
      await expect(
        ui.locator("header, footer, .brand, .mark, .split, .detail"),
      ).toHaveCount(0);
      await expect(
        ui.getByRole("status", { name: "Connection status" }),
      ).toHaveClass("sr-only");
      await expect(ui.getByText("Sync details", { exact: true })).toHaveCount(
        0,
      );
      await ui.getByLabel("Search inbox").fill("Item");
      await ui.getByLabel("Inbox filter").selectOption("all");
      const row = ui.getByRole("button", { name: /Item 30: investigation/ });
      const listWidth = await ui
        .locator(".item-list")
        .evaluate((el) => el.getBoundingClientRect().width);
      const frameWidth = await ui
        .locator("body")
        .evaluate(() => window.innerWidth);
      expect(listWidth).toBeGreaterThan(frameWidth * 0.9);
      const firstItem = ui.locator(".inbox-row > .item").first();
      await expect(firstItem.locator(".priority")).toHaveText("P1");
      await expect(ui.locator(".item-meta .priority")).toHaveCount(0);
      const badgeBox = await firstItem.locator(".priority").boundingBox();
      const titleBox = await firstItem.locator(".item-title").boundingBox();
      expect(badgeBox!.x + badgeBox!.width).toBeLessThanOrEqual(titleBox!.x);
      expect(Math.abs(badgeBox!.y - titleBox!.y)).toBeLessThan(6);
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await page.screenshot({
        path: test.info().outputPath(`inbox-top-${width}-${theme}.png`),
      });
      await row.scrollIntoViewIfNeeded();
      await row.focus();
      const scrollBefore = await ui
        .locator("body")
        .evaluate(() => window.scrollY);
      expect(scrollBefore).toBeGreaterThan(0);
      await page.keyboard.press("Enter");
      await expect(
        ui.getByRole("heading", { name: "Item 30: investigation", level: 1 }),
      ).toBeVisible();
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await expect(ui.getByLabel("Search inbox")).toHaveCount(0);
      await expect(
        ui.getByText("Description for item 30", { exact: true }),
      ).toBeVisible();
      await expect(
        ui.getByText("Item 29: investigation", { exact: true }),
      ).toHaveCount(0);
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await page.screenshot({
        path: test.info().outputPath(`item-${width}-${theme}.png`),
      });
      await (await linkTarget(ui)).fill("owner/repo#retained-draft");
      await expect(ui.getByLabel("Message agent")).toBeVisible();
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await ui.getByLabel("Message agent").fill("Run-specific message draft");
      await showItemDetails(ui);
      await expect(ui.getByLabel("Link target")).toHaveValue(
        "owner/repo#retained-draft",
      );
      await ui.getByRole("button", { name: "← Issues", exact: true }).click();
      await expect(ui.locator(".detail")).toHaveCount(0);
      await expect(ui.getByLabel("Search inbox")).toHaveValue("Item");
      await expect(ui.getByLabel("Inbox filter")).toHaveValue("all");
      await expect(row).toBeFocused();
      expect(
        await ui.locator("body").evaluate(() => window.scrollY),
      ).toBeCloseTo(scrollBefore, 0);
      await page.screenshot({
        path: test.info().outputPath(`inbox-${width}-${theme}.png`),
      });
      await showAgents(ui);
      const runRow = ui.locator(".work-history > .item").first();
      await runRow.click();
      await expect(ui.getByLabel("Message agent")).toHaveValue(
        "Run-specific message draft",
      );
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await ui.getByRole("button", { name: "← Issues", exact: true }).click();
      await expect(ui.getByLabel("Search inbox")).toHaveValue("Item");
      await expect(ui.getByLabel("Message agent")).toHaveCount(0);
      expect(
        calls.filter((c) =>
          [
            "prepare_launch",
            "start_run",
            "send_message",
            "set_relationship",
          ].includes(c.name),
        ),
      ).toHaveLength(0);
    });
  }
}

test("page changes keep keyboard focus usable when an unread row leaves the list", async ({
  page,
}) => {
  const { ui } = await host(page);
  await ui.getByLabel("Inbox filter").selectOption("unread");
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await expect(
    ui.getByText("No matching items", { exact: true }),
  ).toBeVisible();
  await expect(ui.locator(".detail")).toHaveCount(0);
  await ui.getByRole("button", { name: "Clear filters", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  ).toBeVisible();
});

for (const theme of ["light", "dark"] as const) {
  test(`small inbox GitHub links open the original issue and PR without selecting either in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.inbox[0].item.id = "dkropachev/code-factory-pilot-20261019#1";
    snapshot.inbox[0].item.url =
      "https://github.com/dkropachev/code-factory-pilot-20261019/issues/1";
    const pr = structuredClone(snapshot.inbox[0]);
    pr.item.id = "dkropachev/code-factory-pilot-20261019#2";
    pr.item.kind = "pr";
    pr.item.title = "Original pull request";
    pr.item.url =
      "https://github.com/dkropachev/code-factory-pilot-20261019/pull/2";
    snapshot.inbox.push(pr);
    const { ui, state, calls, openedLinks } = await host(page, {
      snapshot,
      theme,
    });
    const issue = ui.getByRole("link", {
      name: "Open issue dkropachev/code-factory-pilot-20261019#1 on GitHub",
      exact: true,
    });
    const pull = ui.getByRole("link", {
      name: "Open pull request dkropachev/code-factory-pilot-20261019#2 on GitHub",
      exact: true,
    });
    await expect(issue).toHaveText("dkropachev/code-factory-pilot-20261019#1");

    await expect(
      ui.getByText("dkropachev/code-factory-pilot-20261019#1", { exact: true }),
    ).toHaveCount(1);
    await expect(ui.getByText("GitHub", { exact: true })).toHaveCount(0);
    await expect(issue.locator("..")).toHaveClass("item-meta");
    await expect(issue).toHaveAttribute("href", snapshot.inbox[0].item.url);

    await expect(issue).toHaveAttribute("rel", "noopener noreferrer");
    await expect(ui.locator("button a")).toHaveCount(0);
    await issue.click();
    await expect.poll(() => openedLinks.length).toBe(1);
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(pull).toHaveText("dkropachev/code-factory-pilot-20261019#2");
    await expect(pull).toHaveAttribute("href", pr.item.url);
    await ui.getByRole("button", { name: /Original pull request/ }).focus();
    await page.keyboard.press("Tab");
    await expect(pull).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => openedLinks.length).toBe(2);
    expect(openedLinks).toEqual([snapshot.inbox[0].item.url, pr.item.url]);
    expect(state.inbox.every((e) => e.unread && e.user.new)).toBe(true);
    expect(
      calls.filter((c) =>
        ["read_item", "item_action", "prepare_launch", "start_run"].includes(
          c.name,
        ),
      ),
    ).toHaveLength(0);
    await expect(ui.locator(".detail")).toHaveCount(0);
    await expect(ui.getByLabel("Search inbox")).toBeVisible();
    expect(
      await ui.locator("body").evaluate((b) => b.scrollWidth <= b.clientWidth),
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`github-links-${theme}.png`),
    });
    await scanOwnedApp(page, `github-links-${theme}`);
    await ui.getByRole("button", { name: /Original pull request/ }).click();
    await expect(
      ui.getByRole("heading", { name: pr.item.title, level: 1 }),
    ).toBeVisible();
    await expect(ui.locator(".item-list")).toHaveCount(0);
  });
}
for (const failure of ["deny", "rpc"] as const) {
  test(`inbox GitHub link ${failure} failure stays in the list with visible feedback`, async ({
    page,
  }) => {
    const { ui, calls, openedLinks } = await host(page, {
      denyOpenLink: failure === "deny",
      failOpenLink: failure === "rpc",
    });
    await ui
      .getByRole("link", {
        name: "Open issue owner/repo#1 on GitHub",
        exact: true,
      })
      .click();
    await expect(ui.getByRole("alert")).toContainText(
      failure === "deny"
        ? "The host could not open GitHub."
        : "Host link unavailable",
    );
    expect(openedLinks).toHaveLength(1);
    await expect(ui.getByLabel("Search inbox")).toBeVisible();
    expect(
      calls.filter((c) => c.name === "read_item" || c.name === "item_action"),
    ).toHaveLength(0);
  });
}

for (const theme of ["light", "dark"] as const) {
  test(`settings has a dedicated ${theme} view without inbox or agent navigation`, async ({
    page,
  }) => {
    const { ui, calls } = await host(page, { surface: "settings", theme });
    await expect(
      ui.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    await expect(ui.getByRole("navigation")).toHaveCount(0);
    await expect(
      ui.getByRole("button", { name: "Issues", exact: true }),
    ).toHaveCount(0);
    await expect(
      ui.getByRole("button", { name: "Agents", exact: true }),
    ).toHaveCount(0);
    await expect(ui.getByLabel("Search inbox")).toHaveCount(0);
    await expect(ui.getByLabel("Message agent")).toHaveCount(0);
    await ui.getByLabel("GitHub repository").fill("owner/repo");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    const form = ui
      .getByRole("button", { name: "Save repository", exact: true })
      .locator("xpath=ancestor::form");
    await expect(form.getByRole("status")).toHaveText("Saved.");
    await expect(
      ui.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    expect(
      calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
    ).toHaveLength(0);
  });
}
test("inbox and agent views have no settings entry or configuration controls", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "no-settings")];
  const { ui } = await host(page, { snapshot });
  for (const tab of ["Issues", "My PRs", "Review PRs", "Messages"]) {
    await ui.getByRole("button", { name: tab, exact: true }).click();
    await expect(
      ui.getByRole("button", { name: "Settings", exact: true }),
    ).toHaveCount(0);
    await expect(ui.getByLabel("GitHub repository")).toHaveCount(0);
  }
  await showAgents(ui);
  await ui.locator(".work-history > .item").first().click();
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Settings", exact: true }),
  ).toHaveCount(0);
});

test("separate settings can save while the inbox view retains its item and agent drafts", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "settings-concurrent")];
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await (await linkTarget(ui)).fill("owner/repo#kept");
  await ui.getByLabel("Message agent").fill("Keep this agent draft");
  await page.evaluate(() => {
    const frame = document.createElement("iframe");
    frame.title = "Code Factory settings";
    frame.src = "/settings-ui";
    frame.style.cssText = "width:100%;height:95vh;border:0";
    document.body.appendChild(frame);
  });
  const settings = page.frameLocator('iframe[title="Code Factory settings"]');
  await settings.getByLabel("GitHub repository").fill("owner/repo");
  await settings
    .getByRole("button", { name: "Save repository", exact: true })
    .click();
  await expect(
    settings
      .getByRole("button", { name: "Save repository", exact: true })
      .locator("xpath=ancestor::form")
      .getByRole("status"),
  ).toHaveText("Saved.");
  await expect(
    settings.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this agent draft",
  );
  await expect(
    ui.getByRole("button", { name: "Settings", exact: true }),
  ).toHaveCount(0);
  await showItemDetails(ui);
  await expect(ui.getByLabel("Link target")).toHaveValue("owner/repo#kept");
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "set_relationship"].includes(c.name),
    ),
  ).toHaveLength(0);
});

type ToolCall = { name: string; arguments: Record<string, unknown> };
type ToolResult = {
  isError?: boolean;
  content?: { type: string; text?: string }[];
  structuredContent?: { data: unknown };
};
async function assembledHost(
  page: Page,
  options: {
    loseFirstStartResponse?: boolean;
    beforeFirstStartResponse?: () => Promise<void>;
  } = {},
) {
  const calls: ToolCall[] = [];
  const acceptedStarts: Run[] = [];
  const daemonOrigin = new URL(
    `http://127.0.0.1:${process.env.CODE_FACTORY_TEST_HOST_PORT ?? "4174"}`,
  ).origin;
  async function request(p: ToolCall): Promise<ToolResult> {
    calls.push(p);
    const response = await fetch(`${daemonOrigin}/mcp`, {
      method: "POST",
      headers: {
        Authorization:
          "Bearer aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        "MCP-Protocol-Version": "2026-07-28",
        "Mcp-Method": "tools/call",
        "Mcp-Name": p.name,
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: {
          ...p,
          _meta: {
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientInfo": {
              name: "browser-host",
              version: "1",
            },
            "io.modelcontextprotocol/clientCapabilities": {},
          },
        },
      }),
    });
    const result = (await response.json()) as {
      result?: ToolResult;
      error?: unknown;
    };
    if (!response.ok || result.error || !result.result)
      throw new Error("Daemon MCP request failed");
    return result.result;
  }
  await page.exposeFunction("daemonCall", async (p: ToolCall) => {
    const result = await request(p);
    if (p.name === "start_run" && !p.arguments.resume && !result.isError) {
      acceptedStarts.push(result.structuredContent!.data as Run);
      if (acceptedStarts.length === 1) {
        await options.beforeFirstStartResponse?.();
        if (options.loseFirstStartResponse)
          throw new Error(
            "Start response lost after the fixture daemon accepted it.",
          );
      }
    }
    return result;
  });
  await page.route("**/assembled-host", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><iframe title="Code Factory app" src="/" style="width:100%;height:95vh;border:0"></iframe><script>window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;try{let result={};if(m.method==='ui/initialize'){result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Assembled test host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:'light',displayMode:'inline'}}}else if(m.method==='tools/call'){result=await window.daemonCall(m.params)}e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*')}});</script>`,
    }),
  );
  const forbidden: string[] = [];
  page.on("request", (r) => {
    if (
      r.frame().parentFrame() &&
      (new URL(r.url()).origin === daemonOrigin || /localhost/.test(r.url()))
    )
      forbidden.push(r.url());
  });
  await page.goto("/assembled-host");
  const ui = page.frameLocator("iframe");
  await expect(ui.getByLabel("Search inbox")).toBeVisible();
  async function daemon<T>(
    name: string,
    args: Record<string, unknown> = {},
  ): Promise<T> {
    const result = await request({ name, arguments: args });
    if (result.isError)
      throw new Error(
        result.content?.map((item) => item.text ?? "").join("\n") ||
          "Fixture tool failed",
      );
    return result.structuredContent!.data as T;
  }
  return { ui, calls, acceptedStarts, forbidden, daemon };
}

test("lost Start response locates the accepted daemon run without another Start", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, acceptedStarts, forbidden, daemon } = await assembledHost(
    page,
    {
      loseFirstStartResponse: true,
      beforeFirstStartResponse: () => gate,
    },
  );
  const before = await daemon<Run[]>("list_runs");
  try {
    await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
    await showItemDetails(ui);
    await ui.getByRole("button", { name: /^New run$/, exact: true }).click();
    await expect(
      ui
        .getByRole("dialog", { name: "Launch agent" })
        .getByRole("button", { name: "Start agent", exact: true }),
    ).toBeEnabled();
    const dialog = ui.getByRole("dialog", { name: "Launch agent" });
    const start = ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true });
    await start.dblclick();
    await expect(start).toBeDisabled();
    await expect
      .poll(() => calls.filter((call) => call.name === "start_run").length)
      .toBe(1);
    release();
    await expect(dialog).toHaveCount(0);
    expect(acceptedStarts).toHaveLength(1);
    const created = (await daemon<Run[]>("list_runs")).filter(
      (run) => !before.some((old) => old.id === run.id),
    );
    expect(created).toHaveLength(1);
    expect(created[0].id).toBe(acceptedStarts[0].id);
    expect(
      calls
        .filter((call) => call.name === "start_run")
        .map((call) => call.arguments),
    ).toEqual([{ id: created[0].id }]);
    expect(
      calls.some(
        (call) =>
          call.name === "read_run" && call.arguments.id === created[0].id,
      ),
    ).toBe(true);
    await expect(
      ui.getByRole("button", { name: /Waiting for your answer/ }),
    ).toBeVisible();
    expect(calls.filter((call) => call.name === "prepare_launch")).toHaveLength(
      1,
    );
    expect(forbidden).toEqual([]);
  } finally {
    release();
  }
});

test("sending a message restores the dirty retained daemon session without a Resume action", async ({
  page,
}) => {
  const { ui, calls, acceptedStarts, forbidden, daemon } =
    await assembledHost(page);
  await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
  await showItemDetails(ui);
  await ui.getByRole("button", { name: /^New run$/, exact: true }).click();
  await expect(
    ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await daemon<Run[]>("list_runs")).find(
          (r) => r.id === acceptedStarts[0].id,
        )?.status,
    )
    .toBe("idle");
  const run = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === acceptedStarts[0].id,
  )!;
  expect(relative(tmpdir(), run.worktree)).toMatch(
    /^factory-browser-[^/]+\/worktrees\/[a-f0-9]+$/,
  );
  const sentinel = join(run.worktree, "browser-dirty-fixture.txt");
  await writeFile(sentinel, "retain this browser fixture change\n");
  await ui.getByLabel("Message agent").fill("Retain this follow-up draft.");
  await ui.getByText("Details", { exact: true }).click();
  await ui
    .getByRole("button", { name: "Clean up worktree", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    /modified or untracked files|worktree/,
  );
  await expect
    .poll(
      async () =>
        (await daemon<Run[]>("list_runs")).find((r) => r.id === run.id)?.status,
    )
    .toBe("interrupted");
  await expect(
    ui.getByRole("button", { name: "Resume agent", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeEnabled();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain this follow-up draft.",
  );
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  expect(await readFile(sentinel, "utf8")).toBe(
    "retain this browser fixture change\n",
  );
  const interrupted = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === run.id,
  )!;
  expect(interrupted.status).toBe("interrupted");
  expect(interrupted.worktree).toBe(run.worktree);
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(1);
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Waiting for your answer/ }),
  ).toBeVisible();
  await expect(ui.locator(".detail .status")).toHaveText("Waiting for you");
  const resumed = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === run.id,
  )!;
  expect(resumed.worktree).toBe(run.worktree);
  expect(resumed.threadId).toBe(run.threadId);
  expect(await readFile(sentinel, "utf8")).toBe(
    "retain this browser fixture change\n",
  );
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  expect(
    calls
      .filter((call) => call.name === "send_message")
      .map((call) => call.arguments),
  ).toEqual([
    { id: run.id, text: "Retain this follow-up draft.", expectedTurn: "" },
  ]);
  expect(
    calls
      .filter((call) => call.name === "start_run")
      .map((call) => call.arguments),
  ).toEqual([{ id: run.id }]);
  expect(forbidden).toEqual([]);
});

test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up", async ({
  page,
}) => {
  const { ui, calls, forbidden } = await assembledHost(page);
  await ui.getByRole("button", { name: /Fresh first message fixture/ }).click();
  expect(calls.map((call) => call.name)).not.toContain("start_run");
  await expect(ui.getByLabel("Message agent")).not.toHaveValue("");
  await ui
    .getByLabel("Message agent")
    .fill("Inspect the local fixture and ask which scope to investigate.");
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(ui.getByRole("dialog")).toHaveCount(0);
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  await ui
    .getByLabel("Message agent")
    .fill("Follow-up on the isolated fixture.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((call) => call.name === "send_message").length)
    .toBe(1);
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(1);
  expect(forbidden).toEqual([]);
});

for (const theme of ["light", "dark"] as const) {
  test(`focused work queues use GitHub ownership review requests and pending priority in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    const make = (
      number: number,
      kind: string,
      priority: number | null,
      options: Partial<Snapshot["inbox"][number]["item"]> = {},
    ) => {
      const row = structuredClone(snapshot.inbox[0]);
      row.item = {
        ...row.item,
        id: `owner/repo#${number}`,
        number,
        kind,
        title: `Work ${number}`,
        url: `https://github.com/owner/repo/${kind === "pr" ? "pull" : "issues"}/${number}`,
        ...options,
      };
      row.priority = priority;
      return row;
    };
    const linked = make(4, "issue", 0);
    linked.hasPullRequest = true;
    snapshot.inbox = [
      snapshot.inbox[0],
      make(2, "pr", 1),
      make(3, "pr", 2, { author: "other", reviewRequests: ["alex"] }),
      linked,
      make(5, "issue", 0, { state: "closed" }),
      make(6, "issue", null),
      make(7, "issue", 0),
      make(8, "pr", 0, { author: "other", reviewTeams: ["org/core"] }),
      make(9, "pr", 1, { author: "other" }),
      make(10, "pr", 0, { state: "closed" }),
    ];
    snapshot.viewer!.teams = ["org/core"];
    const { ui, calls } = await host(page, { snapshot, theme });
    const rows = ui.getByRole("region", { name: "Items" }).getByRole("button");
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(rows).toHaveCount(4);
    await ui.getByLabel("Inbox filter").selectOption("pending");
    await expect(rows).toHaveText([
      /Work 7/,
      /Fix reconnect regression/,
      /Work 6/,
    ]);
    await ui.getByLabel("Inbox filter").selectOption("open");
    await expect(rows).toHaveCount(4);
    await ui.getByLabel("Search inbox").fill("Work 4");
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(rows).toHaveText([/Work 2/]);
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(ui.getByLabel("Search inbox")).toHaveValue("");
    await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
    await expect(rows).toHaveText([/Work 8/, /Work 3/]);
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await expect(ui.getByLabel("Search inbox")).toHaveValue("Work 4");
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(rows).toHaveText([/Work 4/]);
    await expect(ui.locator("body")).toHaveJSProperty(
      "scrollWidth",
      await ui.locator("body").evaluate((e) => e.clientWidth),
    );
    expect(
      calls.some((call) =>
        ["read_item", "start_run", "prepare_launch"].includes(call.name),
      ),
    ).toBe(false);
    await page.screenshot({
      path: test.info().outputPath(`work-queues-${theme}.png`),
    });
    await scanOwnedApp(page, `work-queues-${theme}`);
  });
}

test("item opens a prefilled conversation and first-message Start stays explicit", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 820 });
  const { ui, calls, state } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("region", { name: "Agent", exact: true }),
  ).toBeVisible();
  await expect(ui.getByRole("region", { name: "Item summary" })).toContainText(
    "Connections stop after a brief outage.",
  );
  await expect(
    ui.getByRole("region", { name: "Item", exact: true }),
  ).toHaveCount(0);
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Investigate Fix reconnect regression.\nConnections stop after a brief outage.",
  );
  await expect(ui.locator(".item-details")).not.toHaveAttribute("open", "");
  await expect(
    ui.getByRole("button", { name: "Open agent", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({ path: test.info().outputPath("first-message.png") });
  await scanOwnedApp(page, "first-message");
  await showItemDetails(ui);
  await ui.getByText("Work history", { exact: true }).click();
  await expect(ui.getByText("No work history.", { exact: true })).toBeVisible();
  expect(
    calls.some((call) => ["prepare_launch", "start_run"].includes(call.name)),
  ).toBe(false);
  await chat
    .getByLabel("Message agent")
    .fill("Explain the retry failure and suggest a safe fix.");
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Explain the retry failure and suggest a safe fix.",
  );
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.find((c) => c.name === "prepare_launch")!.arguments.prompt).toBe(
    "Explain the retry failure and suggest a safe fix.",
  );
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments.id),
  ).toEqual([state.runs[0].id]);
  await expect(ui.getByRole("dialog")).toHaveCount(0);
});

test("first-message launch uncertainty survives leaving and reopening its item", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    rejectStart: true,
    failReadRun: true,
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    "Start status is uncertain",
  );
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeDisabled();
  await ui
    .getByRole("button", { name: "Check start status", exact: true })
    .click();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
});

test("Messages lists waiting agents and answering removes only its owning request", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second task";
  snapshot.inbox.push(second);
  snapshot.runs = [
    retained(snapshot, "one", "waiting"),
    { ...retained(snapshot, "two", "waiting"), itemId: second.item.id },
  ];
  snapshot.attention = snapshot.runs.map((run, i) => ({
    id: `question-${i}`,
    runId: run.id,
    method: "item/tool/requestUserInput",
    blocking: true,
    status: "pending",
    params: {
      questions: [
        {
          id: "scope",
          header: "Scope",
          question: `Scope for task ${i}?`,
          options: [{ label: "Focused", description: "Small fix" }],
        },
      ],
    },
  }));
  const request = snapshot.attention[0];
  snapshot.runs.push({
    ...retained(snapshot, "disconnected", "interrupted"),
    itemId: second.item.id,
  });
  snapshot.attention.push(
    { ...structuredClone(request), id: "nonblocking", blocking: false },
    { ...structuredClone(request), id: "answered", status: "answered" },
    { ...structuredClone(request), id: "stale", status: "stale" },
    { ...structuredClone(request), id: "orphaned", runId: "missing-run" },
    {
      ...structuredClone(request),
      id: "disconnected-request",
      runId: "disconnected",
    },
    {
      ...structuredClone(request),
      id: "approval",
      method: "item/commandExecution/requestApproval",
      params: {
        command: "git status",
        reason: "Inspect changes",
        availableDecisions: ["decline"],
      },
    },
  );
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  const waiting = ui.getByRole("region", { name: "Waiting for you" });
  await expect(waiting.getByRole("button")).toHaveCount(4);
  await expect(
    ui.getByRole("button", { name: /^Messages\s*4$/ }),
  ).toBeVisible();
  await expect(ui.getByText("All agents", { exact: true })).toHaveCount(0);
  await expect(ui.getByText("Work history", { exact: true })).toHaveCount(0);
  await waiting.getByRole("button", { name: /Second task/ }).click();
  await expect(ui.getByRole("dialog")).toContainText(
    "Second task · Investigation",
  );
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "Second task", exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(3);
  await expect(waiting).toContainText("Fix reconnect regression");
  expect(calls.find((c) => c.name === "answer_request")!.arguments.id).toBe(
    "question-1",
  );
  await waiting.getByRole("button", { name: /Approval needed/ }).click();
  await expect(ui.getByRole("dialog")).toContainText("git status");
  await ui.getByRole("button", { name: "Deny", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(2);
  expect(
    calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
  ).toEqual({ id: "approval", answer: { decision: "decline" } });
  await waiting.getByRole("button", { name: /Reply needed/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(1);
  await waiting.getByRole("button", { name: /agent continues/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(0);
  await expect(waiting).toContainText("No messages waiting for you.");
  await expect(
    ui.getByRole("button", { name: "Messages", exact: true }),
  ).toBeVisible();
});

test("first-message launch reviews changed authority before starting its prepared snapshot", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await expect(chat).toContainText("read-only");
  state.profiles[0].sandbox = "workspace-write";
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(chat.getByRole("alert")).toContainText(
    "Launch settings changed",
  );
  await expect(chat).toContainText("workspace-write");
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments.id),
  ).toEqual([state.runs[0].id]);
});

test("first-message lost Start response locates accepted work without another launch", async ({
  page,
}) => {
  const { ui, calls, state } = await host(page, { loseStartResponse: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.find((c) => c.name === "read_run")!.arguments.id).toBe(
    state.runs[0].id,
  );
});

test("a late first-message Start cannot replace another item's draft", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second task";
  snapshot.inbox.push(second);
  const { ui, calls } = await host(page, {
    snapshot,
    beforeStartResponse: () => gate,
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await ui
      .getByRole("region", { name: "New agent conversation" })
      .getByRole("button", { name: "Start agent", exact: true })
      .click();
    await expect
      .poll(() => calls.filter((c) => c.name === "start_run").length)
      .toBe(1);
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await ui.getByRole("button", { name: /Second task/ }).click();
    await ui
      .getByLabel("Message agent")
      .fill("Keep the second task's unsubmitted message.");
    release();
    await expect(
      ui.getByRole("heading", { name: "Second task", exact: true }),
    ).toBeVisible();
    await expect(ui.getByLabel("Message agent")).toHaveValue(
      "Keep the second task's unsubmitted message.",
    );
    await expect(
      ui.getByRole("region", { name: "New agent conversation" }),
    ).toBeVisible();
    expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
    expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  } finally {
    release();
  }
});

test("first-message preparation errors retain the editable draft without starting", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { failPreparation: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await chat
    .getByLabel("Message agent")
    .fill("Keep this first-message draft after failure.");
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(chat.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Keep this first-message draft after failure.",
  );
  await expect(chat.getByLabel("Message agent")).toBeEditable();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`own PR review rejection preserves the first message without starting in ${theme}`, async ({ page }) => {
    const snapshot = initial();
    snapshot.inbox[0].item.kind = "pr";
    snapshot.inbox[0].queues = ["mine"];
    snapshot.profiles[0].id = "review";
    snapshot.profiles[0].name = "PR review";
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      toolError: { name: "prepare_launch", message: "you cannot review your own pull request (owner/repo#1)" },
    });
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    const chat = ui.getByRole("region", { name: "New agent conversation" });
    await chat.getByLabel("Agent pipeline").selectOption("review");
    await chat.getByLabel("Message agent").fill("Review this pull request.");
    await chat.getByRole("button", { name: "Start agent", exact: true }).click();
    await expect(chat.getByRole("alert")).toContainText("you cannot review your own pull request");
    await expect(chat.getByLabel("Message agent")).toHaveValue("Review this pull request.");
    expect(calls.filter(c => c.name === "prepare_launch")).toHaveLength(1);
    expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
    expect(snapshot.runs).toHaveLength(0);
  });
}

test("initial bridge failures retry a fresh handshake without reloading or losing state", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { initializeFailures: 3 });
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected");
  expect(
    await page.evaluate(
      () =>
        (window as unknown as { initializationCount: number })
          .initializationCount,
    ),
  ).toBe(4);
  expect(calls.filter((c) => c.name === "read_snapshot")).toHaveLength(1);
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

test("transient polling outage recovers without a stale error banner or lost first message", async ({
  page,
}) => {
  let disconnected = false;
  const { ui, calls } = await host(page, {
    transportFault: (name) => disconnected && name === "read_changes",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByLabel("Message agent")
    .fill("Keep this draft through reconnect.");
  disconnected = true;
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Reconnecting…", { timeout: 7000 });
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this draft through reconnect.",
  );
  disconnected = false;
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected", { timeout: 7000 });
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this draft through reconnect.",
  );
  expect(
    calls.some((c) =>
      ["prepare_launch", "start_run", "send_message"].includes(c.name),
    ),
  ).toBe(false);
});

test("transient item read failure retries and marks only the successfully displayed revision read", async ({
  page,
}) => {
  let itemUnavailable = true;
  const { ui, calls } = await host(page, {
    transportFault: (name) => itemUnavailable && name === "read_item",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying item" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "item_action")).toHaveLength(0);
  itemUnavailable = false;
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  expect(calls.filter((c) => c.name === "read_item")).toHaveLength(4);
  await expect
    .poll(
      () =>
        calls.filter(
          (c) => c.name === "item_action" && c.arguments.action === "read",
        ).length,
    )
    .toBe(1);
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

test("transcript recovery keeps output and draft while removing only its own read error", async ({
  page,
}) => {
  let transcriptUnavailable = false;
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "recover-output")];
  const { ui } = await host(page, {
    snapshot,
    transportFault: (name) =>
      transcriptUnavailable && name === "read_transcript",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Message agent").fill("Keep the follow-up draft.");
  transcriptUnavailable = true;
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying conversation" }),
  ).toBeVisible({ timeout: 7000 });
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  transcriptUnavailable = false;
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying conversation" }),
  ).toHaveCount(0, { timeout: 7000 });
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep the follow-up draft.",
  );
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

test("mutation connection failure is not replayed and later polling does not dismiss it", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "send-once")];
  const { ui, calls } = await host(page, {
    snapshot,
    transportFault: (name) => name === "send_message",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByLabel("Message agent")
    .fill("Send only once, retaining the uncertain draft.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
  const polls = calls.filter((c) => c.name === "read_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "read_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Send only once, retaining the uncertain draft.",
  );
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
});

test("read tool errors are not mistaken for retryable transport errors", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    toolError: {
      name: "read_item",
      message: "Connection failed while validating this item.",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection failed while validating this item",
  );
  await expect(
    ui.getByRole("button", { name: "Retry loading item", exact: true }),
  ).toBeVisible();
  const polls = calls.filter((c) => c.name === "read_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "read_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "read_item")).toHaveLength(1);
});

test("exact host-wrapped MCP connection error retries a read without exposing its framework text", async ({
  page,
}) => {
  const failure = {
    name: "unused",
    message: "McpServerError: Connection failed.",
  };
  const { ui, calls } = await host(page, { toolError: failure });
  await ui.locator("main").evaluate((root) => {
    root.dataset.frameworkErrorSeen = "false";
    new MutationObserver(() => {
      if (
        [...root.querySelectorAll('[role="alert"]')].some((e) =>
          e.textContent?.includes("McpServerError"),
        )
      )
        root.dataset.frameworkErrorSeen = "true";
    }).observe(root, { subtree: true, childList: true, characterData: true });
  });
  failure.name = "read_changes";
  await expect
    .poll(() => calls.filter((c) => c.name === "read_changes").length)
    .toBeGreaterThan(0);
  failure.name = "unused";
  await expect
    .poll(() => calls.filter((c) => c.name === "read_changes").length)
    .toBeGreaterThan(1);
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected");
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect(ui.locator("main")).toHaveAttribute(
    "data-framework-error-seen",
    "false",
  );
  expect(
    calls.some((c) =>
      ["start_run", "send_message", "answer_request", "save_settings"].includes(
        c.name,
      ),
    ),
  ).toBe(false);
});

test("exact host-wrapped connection failure never replays a mutation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "wrapped-send")];
  const { ui, calls } = await host(page, {
    snapshot,
    toolError: {
      name: "send_message",
      message: "McpServerError: Connection failed.",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Retain this uncertain message.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
  const polls = calls.filter((c) => c.name === "read_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "read_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain this uncertain message.",
  );
});

for (const theme of ["light", "dark"] as const) {
  test(`terminated work retains history and cleanup without allowing continuation in ${theme}`, async ({ page }) => {
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "closed-source", "terminated")];
    snapshot.runs[0].error = "PR is merged; work terminated. History and workspace are retained.";
    const { ui, calls } = await host(page, { snapshot, theme });
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText("Stopped");
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByRole("alert")).toContainText("PR is merged; work terminated");
    await ui.getByLabel("Message agent").fill("Continue anyway");
    await expect(ui.getByRole("button", { name: "Send message", exact: true })).toBeDisabled();
    await ui.getByText("Details", { exact: true }).click();
    await expect(ui.getByRole("button", { name: "Clean up worktree", exact: true })).toBeVisible();
    await expect(ui.getByRole("button", { name: "Interrupt", exact: true })).toHaveCount(0);
    expect(calls.filter(c => ["start_run", "send_message"].includes(c.name))).toHaveLength(0);
  });
  test(`inactive work status and sending a message restore the retained session in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "wake-session", "interrupted")];
    // A disconnected turn identity must not be sent as an active steering target.
    snapshot.runs[0].turnId = "old-turn";
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      beforeMessage: () => gate,
    });
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
      "Inactive",
    );
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
      "Inactive",
    );
    await expect(ui.getByRole("status", { name: "Run status" })).toHaveCount(0);
    await expect(ui.getByRole("button", { name: /Resume agent/ })).toHaveCount(
      0,
    );
    await ui
      .getByLabel("Message agent")
      .fill("Continue this issue with my clarification.");
    const send = ui.getByRole("button", { name: "Send message", exact: true });
    try {
      await send.dblclick();
      await expect(send).toBeDisabled();
      await expect
        .poll(() => calls.filter((c) => c.name === "send_message").length)
        .toBe(1);
      await expect(ui.getByLabel("Message agent")).toHaveValue(
        "Continue this issue with my clarification.",
      );
      release();
      await expect(ui.getByLabel("Message agent")).toHaveValue("");
      await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
        "Active",
      );
      await page.screenshot({
        path: test.info().outputPath(`message-wake-${theme}.png`),
      });
      expect(
        calls.filter((c) => c.name === "send_message")[0].arguments,
      ).toEqual({
        id: "wake-session",
        text: "Continue this issue with my clarification.",
        expectedTurn: "",
      });
      expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
      expect(
        await ui
          .locator("body")
          .evaluate((el) => el.scrollWidth <= el.clientWidth),
      ).toBe(true);
    } finally {
      release();
    }
  });
}
test("saved message clears its draft before agent delivery and shows uncertainty without replay", async ({ page }) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "queued-session", "interrupted")];
  const transcript: JournalEvent[] = [];
  const { ui, calls } = await host(page, { snapshot, transcript, queueMessages: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Save my follow-up before contacting the agent.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  await expect(ui.locator(".transcript")).toContainText("Message saved. Awaiting agent delivery.");
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText("Inactive");
  transcript.push({ seq: snapshot.cursor + 1, kind: "run.output", entity: "queued-session", at: new Date().toISOString(), data: { method: "codefactory/messageDelivery", params: { itemId: "saved-message", state: "uncertain", error: "Agent delivery could not be confirmed. Inspect this session before sending again." } } });
  await expect(ui.locator(".transcript")).toContainText("Agent delivery could not be confirmed.");
  await expect(ui.locator(".transcript")).not.toContainText("Awaiting agent delivery");
  await expect(ui.locator(".transcript article")).toHaveCount(1);
  expect(calls.filter(c => c.name === "send_message")).toHaveLength(1);
  expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
});

test("failed inactive message keeps its draft and never calls Resume or replays the send", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "failed-session", "failed")];
  const { ui, calls } = await host(page, {
    snapshot,
    toolError: {
      name: "send_message",
      message: "Retained worktree is unavailable",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Keep this clarification.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Retained worktree is unavailable",
  );
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this clarification.",
  );
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this clarification.",
  );
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

test("cleaned work keeps its Finished item status while offering an explicit fresh conversation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "cleaned-history", "cleaned")];
  const { ui, calls } = await host(page, { snapshot });
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Finished",
  );
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Finished",
  );
  await expect(
    ui.getByRole("region", { name: "New agent conversation" }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "prepare_launch"].includes(c.name),
    ),
  ).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`Working and linked PR monitoring follow server projections in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const state = initial();
    state.inbox[0].queues = ["issues", "working"];
    state.inbox[0].work = {
      tracked: true,
      complete: false,
      status: "Waiting for you",
      runIds: ["older", "newer"],
      attentionCount: 1,
    };
    const pr = {
      id: "owner/repo#9",
      title: "Reconnect fix",
      url: "https://github.com/owner/repo/pull/9",
      state: "open",
      draft: false,
      head: "current",
      reviewDecision: "approved",
      ci: "pending",
      checks: [
        {
          name: "build",
          status: "in_progress",
          conclusion: "",
          url: "https://github.com/owner/repo/actions/runs/9",
        },
      ],
      syncedAt: new Date().toISOString(),
      implementation: true,
    };
    state.inbox[0].linkedPullRequests = [pr];
    state.runs = [
      retained(state, "older", "waiting"),
      retained(state, "newer", "idle", "2026-10-22T00:00:00Z"),
    ];
    const other = structuredClone(state.inbox[0]);
    other.item.id = "owner/repo#2";
    other.item.title = "Not started";
    other.queues = ["issues"];
    other.work = {
      tracked: false,
      complete: false,
      status: "Not started",
      runIds: [],
      attentionCount: 0,
    };
    other.linkedPullRequests = [];
    state.inbox.push(other);
    const { ui, calls, openedLinks } = await host(page, {
      snapshot: state,
      theme,
    });
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(
      ui.getByRole("region", { name: "Items" }).getByRole("button"),
    ).toHaveCount(2);
    await ui.getByRole("button", { name: "Working", exact: true }).click();
    await expect(
      ui.getByRole("region", { name: "Items" }).getByRole("button"),
    ).toHaveCount(1);
    await expect(ui.getByLabel("Work status")).toHaveText("Waiting for you");
    await expect(ui.getByLabel("Pull request status")).toContainText(
      "open · approved · CI pending",
    );
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByLabel("Session status")).toContainText("idle");
    await expect(ui.getByLabel("Work status")).toHaveText("Waiting for you");
    await showItemDetails(ui);
    await ui.getByText("Work history", { exact: true }).click();
    await expect(
      ui.locator(".work-history").getByRole("button", { name: /waiting$/ }),
    ).toBeVisible();
    await expect(
      ui.locator(".work-history").getByRole("button", { name: /idle$/ }),
    ).toBeVisible();
    const linked = ui.getByRole("region", { name: "Linked pull requests" });
    await expect(linked).toContainText("Reconnect fix");
    await linked.getByRole("link", { name: "owner/repo#9" }).click();
    await expect.poll(() => openedLinks).toEqual([pr.url]);
    await linked.getByText("CI checks", { exact: true }).click();
    await linked.getByRole("link", { name: "build", exact: true }).click();
    await expect.poll(() => openedLinks).toEqual([pr.url, pr.checks[0].url]);
    pr.state = "merged";
    pr.ci = "success";
    state.inbox[0].queues = ["issues"];
    state.inbox[0].work!.complete = true;
    state.cursor++;
    await expect(linked).toContainText("merged · approved · CI success");
    await ui.getByRole("button", { name: "← Working", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Fix reconnect regression/ }),
    ).toHaveCount(0);
    expect(
      calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
    ).toHaveLength(0);
    await scanOwnedApp(page, `working-monitoring-${theme}`);
  });
}

test("first-message pipeline selection preserves an edited prompt and launches the selected preset", async ({
  page,
}) => {
  const state = initial();
  state.profiles.push({
    ...state.profiles[0],
    id: "review",
    name: "PR review",
    prompt: "Review {{title}}",
  });
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Agent pipeline")).toHaveValue("implement");
  await ui
    .getByLabel("Message agent")
    .fill("Inspect the regression without publishing.");
  await ui.getByLabel("Agent pipeline").selectOption("review");
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Inspect the regression without publishing.",
  );
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(0);
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "start_run").length)
    .toBe(1);
  expect(
    calls.find((c) => c.name === "prepare_launch")!.arguments.profileId,
  ).toBe("review");
});

test("text fallback opens the same session console and preserves its reply draft", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "retained", "interrupted");
  state.runs = [run];
  state.attention = [
    {
      id: "text-question",
      runId: run.id,
      origin: "text",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "reply",
            header: "Reply",
            question: "Which target should I use?",
            isOther: true,
          },
        ],
      },
    },
  ];
  const transcript: JournalEvent[] = [
    {
      seq: 1,
      kind: "run.output",
      entity: run.id,
      at: new Date().toISOString(),
      data: {
        method: "item/completed",
        params: {
          item: {
            id: "cmd",
            type: "commandExecution",
            command: "git status",
            aggregatedOutput: "fixture command output",
          },
        },
      },
    },
    {
      seq: 2,
      kind: "run.output",
      entity: run.id,
      at: new Date().toISOString(),
      data: {
        method: "item/completed",
        params: {
          codeFactorySource: "question-recovery",
          item: {
            id: "automatic-input",
            type: "userMessage",
            content: [
              {
                type: "text",
                text: "Internal question form repair instructions",
              },
            ],
          },
        },
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot: state, transcript });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .click();
  await ui
    .getByRole("button", { name: "Open session console", exact: true })
    .click();
  await expect(ui.getByLabel("Session console")).toContainText(
    "fixture command output",
  );
  await expect(ui.getByLabel("Session console")).toContainText(
    "Question recovery",
  );
  await expect(ui.getByLabel("Session console")).toContainText(
    "Internal question form repair instructions",
  );
  await ui.getByLabel("Message agent").fill("Console draft");
  await ui
    .getByRole("button", { name: "View conversation", exact: true })
    .click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("Console draft");
  await expect(
    ui.getByText("fixture command output", { exact: false }),
  ).not.toBeVisible();
  await expect(
    ui.getByText("Internal question form repair instructions", { exact: true }),
  ).not.toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui
    .getByRole("dialog")
    .getByLabel("Which target should I use?")
    .fill("Target one");
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "answer_request").length)
    .toBe(1);
  expect(
    calls.filter((c) => c.name === "answer_request").map((c) => c.arguments),
  ).toEqual([
    {
      id: "text-question",
      answer: { answers: { reply: { answers: ["Target one"] } } },
    },
  ]);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

test("question recovery progress and keyboard takeover retain the session draft", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "repairing", "running");
  run.turnId = "repair-turn";
  state.runs = [run];
  state.questionRecoveries = [
    { id: "recovery", runId: run.id, stage: "requesting-form", attempts: 2 },
  ];
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("region", { name: "Question recovery" }),
  ).toContainText("attempt 2 of 2");
  await ui.getByLabel("Message agent").fill("Retain takeover context");
  const takeover = ui.getByRole("button", {
    name: "Take over session",
    exact: true,
  });
  await takeover.focus();
  await takeover.press("Enter");
  await expect(ui.getByLabel("Session console")).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain takeover context",
  );
  await expect
    .poll(() =>
      calls.filter((c) => c.name === "interrupt_run").map((c) => c.arguments),
    )
    .toEqual([{ id: run.id }]);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

test("an uncertain textual reply can be dismissed without advancing its session", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "retained", "idle");
  state.runs = [run];
  state.attention = [
    {
      id: "uncertain",
      runId: run.id,
      origin: "text",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "reply",
            header: "Reply",
            question: "The agent's final reply",
            isOther: true,
          },
        ],
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .click();
  await ui
    .getByRole("button", { name: "No reply needed", exact: true })
    .click();
  await expect(ui.getByRole("dialog")).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "dismiss_question").map((c) => c.arguments),
  ).toEqual([{ id: "uncertain" }]);
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "answer_request"].includes(c.name),
    ),
  ).toHaveLength(0);
  expect(run.status).toBe("idle");
});

for (const theme of ["light", "dark"] as const) {
  test(`admin system and repository review policies retain inheritance and extra prompts in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.profiles.push({
      ...snapshot.profiles[0],
      id: "team-review",
      name: "Team review",
    });
    const { ui, calls, state } = await host(page, {
      snapshot,
      surface: "settings",
      theme,
    });
    await ui.getByText("System defaults", { exact: true }).click();
    await expect(
      ui.getByLabel("System default for review requests"),
    ).toHaveValue("false");
    await ui
      .getByLabel("System default for review requests")
      .selectOption("true");
    await ui.getByLabel("System review pipeline").selectOption("team-review");
    await ui.getByLabel("Plugin update check interval").selectOption("60");
    await ui
      .getByRole("button", { name: "Save system defaults", exact: true })
      .click();
    await expect.poll(() => state.config.autoReview).toBe(true);
    await ui
      .getByLabel("GitHub repository", { exact: true })
      .fill("owner/repo");
    await expect(ui.getByLabel("Repository review requests")).toHaveValue(
      "inherit",
    );
    await ui.getByLabel("Repository review requests").selectOption("false");
    await ui
      .getByLabel("Repository review pipeline")
      .selectOption("team-review");
    await ui
      .getByLabel("Additional repository review prompt")
      .fill("Review transaction boundaries and preserve compatibility.");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    await expect.poll(() => state.repositories[0]?.autoReview).toBe(false);
    expect(
      calls.filter((c) => c.name === "save_repository").at(-1)?.arguments,
    ).toMatchObject({
      autoReview: false,
      reviewProfileId: "team-review",
      reviewPrompt: "Review transaction boundaries and preserve compatibility.",
    });
    await ui
      .getByRole("button", { name: "Edit repository owner/repo", exact: true })
      .click();
    await expect(
      ui.getByLabel("Additional repository review prompt"),
    ).toHaveValue("Review transaction boundaries and preserve compatibility.");
    await ui.getByLabel("Repository review requests").selectOption("inherit");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    await expect.poll(() => state.repositories[0]?.autoReview).toBeNull();
    await ui
      .getByRole("button", { name: "Poll repositories now", exact: true })
      .click();
    await expect
      .poll(() => calls.filter((c) => c.name === "sync_repositories").length)
      .toBe(1);
    expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
    await scanOwnedApp(page, `admin-repository-${theme}`);
  });
  test(`plugin admin tracks installs, versions and explicit updates in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const { ui, calls } = await host(page, { surface: "settings", theme });
    await ui.getByText("Plugins & skills", { exact: true }).click();
    const plugin = ui.locator(".plugin-row").filter({
      has: ui.getByRole("heading", { name: "review-kit", exact: true }),
    });
    await expect(plugin).toContainText("Installed · enabled");
    await expect(plugin).toContainText("Update available");
    expect(calls.filter((c) => c.name === "manage_codex_plugins")).toHaveLength(
      0,
    );
    await ui
      .getByRole("button", { name: "Check for plugin updates", exact: true })
      .click();
    await expect(
      ui.getByRole("button", { name: "Update review-kit", exact: true }),
    ).toBeEnabled();
    await ui
      .getByRole("textbox", { name: "Message agent" })
      .fill("Retain recovery draft");
    await expect(
      ui.getByRole("button", { name: "Send message", exact: true }),
    ).toBeDisabled();
    await ui
      .getByRole("button", { name: "Waiting for your answer — Answer" })
      .hover();
    await scanOwnedApp(page, `${theme}-recovery-narrow`);
    await ui.getByRole("button", { name: "Take over", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(
      ui.getByRole("button", { name: "Take over", exact: true }),
    ).toHaveCount(0);
    expect(
      calls
        .filter((c) => c.name === "manage_codex_plugins")
        .map((c) => c.arguments.action),
    ).toEqual(["check-updates"]);
    await ui
      .getByRole("button", { name: "Update review-kit", exact: true })
      .focus();
    await page.keyboard.press("Enter");
    await expect(plugin).toContainText("Installed version: 2.0.0");
    await expect(plugin).toContainText("Matches checked marketplace version");
    await ui.getByLabel("Include available plugins").check();
    await ui
      .getByRole("button", { name: "Install extra-kit", exact: true })
      .click();
    const extra = ui.locator(".plugin-row").filter({
      has: ui.getByRole("heading", { name: "extra-kit", exact: true }),
    });
    await expect(extra).toContainText("Installed · enabled");
    await ui
      .getByRole("button", { name: "Remove extra-kit", exact: true })
      .click();
    await expect(extra).toContainText("Not installed");
    await ui
      .getByLabel("Marketplace source", { exact: true })
      .fill("owner/custom-plugins");
    await ui.getByLabel("Marketplace Git ref (optional)").fill("stable");
    await ui
      .getByRole("button", { name: "Add marketplace", exact: true })
      .click();
    await expect
      .poll(
        () =>
          calls.filter((c) => c.name === "manage_codex_plugins").at(-1)
            ?.arguments,
      )
      .toMatchObject({
        action: "add-marketplace",
        target: "owner/custom-plugins",
        ref: "stable",
      });
    await expect(
      ui.getByRole("button", { name: "Discover pipeline skills", exact: true }),
    ).toBeEnabled();
    await scanOwnedApp(page, `admin-plugins-${theme}`);
    expect(
      await ui.locator("body").evaluate((e) => e.scrollWidth <= e.clientWidth),
    ).toBe(true);
  });
}

test("uncertain plugin mutations preserve drafts and never replay automatically", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    surface: "settings",
    toolError: {
      name: "manage_codex_plugins",
      message: "Delivery could not be confirmed",
    },
  });
  await ui.getByText("Plugins & skills", { exact: true }).click();
  await ui
    .getByLabel("Plugin selector", { exact: true })
    .fill("my-review@my-market");
  await ui
    .getByRole("button", { name: "Install selected plugin", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    "Delivery could not be confirmed",
  );
  await expect(ui.getByLabel("Plugin selector", { exact: true })).toHaveValue(
    "my-review@my-market",
  );
  await page.waitForTimeout(1500);
  expect(calls.filter((c) => c.name === "manage_codex_plugins")).toHaveLength(
    1,
  );
});

test("clarification progress has no attention badge and stops when unresolved", async ({
  page,
}) => {
  const snap = questionSnapshot();
  snap.attention = [];
  snap.runs = [snap.runs[0]];
  snap.runs[0].status = "idle";
  snap.runs[0].turnId = "";
  snap.questionRecoveries = [
    { id: "clarify-1", runId: "run-1", stage: "clarifying", attempts: 1 },
  ];
  const { ui, state } = await host(page, {
    snapshot: snap,
    recoveryInput: true,
  });
  await ui.getByRole("button", { name: /^Agents/ }).click();
  await ui.getByRole("button").filter({ hasText: "Agent 1 · idle" }).click();
  await expect(
    ui.getByText("Clarifying whether input is needed · attempt 1 of 2"),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Take over", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: /Waiting for your answer/ }),
  ).toHaveCount(0);
  await expect(
    ui.locator(".badge").filter({ hasText: "Needs attention" }),
  ).toHaveCount(0);
  await expect(ui.locator(".attention b")).toHaveText("0");
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeDisabled();
  state.questionRecoveries![0].stage = "unresolved";
  state.questionRecoveries![0].attempts = 2;
  state.cursor++;
  await expect(ui.locator(".recovery-progress")).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Take over", exact: true }),
  ).toHaveCount(0);
  await expect(ui.locator(".attention b")).toHaveText("0");
  await ui
    .getByRole("textbox", { name: "Message agent" })
    .fill("Owner direction");
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeEnabled();
});

test("nonblocking recovered text question keeps its continuing label", async ({
  page,
}) => {
  const snap = questionSnapshot();
  snap.attention = [snap.attention[0]];
  snap.attention[0].origin = "text";
  snap.attention[0].blocking = false;
  const { ui } = await host(page, { snapshot: snap });
  await ui.getByRole("button", { name: /^Agents/ }).click();
  await ui.getByRole("button").filter({ hasText: "Agent 1 · waiting" }).click();
  await expect(
    ui.getByRole("button", { name: "Question · agent continues — Answer" }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: /Waiting for your answer/ }),
  ).toHaveCount(0);
});

function reportFixture(id = "report-1"): IssueReport {
  const at = "2026-10-25T12:00:00Z";
  const fresh = { lastSuccess: "", lastAttempt: "", retryAt: "", error: "" };
  return {
    id,
    observation: {
      surface: "Agents",
      expected: "Answer delivered",
      actual: "Stale response",
      reproductionUnknown: "Observed once",
      observedAt: at,
    },
    processing: "needs_information",
    outcome: "",
    component: "code-factory",
    message: "Awaiting host observation",
    additions: [],
    workflowId: "workflow-1",
    versions: { plugin: "0.2.2" },
    installedVersion: "",
    findings: {
      classification: "insufficient_information",
      summary: "The runtime lacks the host response",
      missingInformation: "What exact response did Work display?",
      repository: "dk-code-factory/code-factory-plugin",
    },
    fixes: [],
    releases: [],
    evidence: [
      {
        origin: "runtime:manifest",
        sha256: "abc",
        bytes: 200,
        at,
        truncated: false,
      },
    ],
    history: [
      {
        status: "needs_information",
        detail: "Host display unavailable locally",
        at,
      },
    ],
    issueSync: { ...fresh },
    releaseSync: { ...fresh },
  };
}
test("reported issues shares ledger search, information requests and evidence recovery", async ({
  page,
}) => {
  const report = reportFixture();
  const { ui, calls } = await host(page, { reports: [report] });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText("What exact response did Work display?");
  await expect(detail).toContainText("Stable availability: Unknown");
  await expect(detail).toContainText(
    "Observed installed Code Factory version: Unknown",
  );
  await ui
    .getByLabel("What did the host display?")
    .fill("Work displayed an expired request error");
  await ui.getByRole("button", { name: "Add evidence", exact: true }).click();
  await expect(detail).toContainText("Work displayed an expired request error");
  await expect(detail.getByRole("definition").first()).toHaveText("queued");
  expect(
    calls.find((c) => c.name === "add_issue_evidence")?.arguments,
  ).toMatchObject({
    id: "report-1",
    text: "Work displayed an expired request error",
  });
  expect(
    calls.find((c) => c.name === "add_issue_evidence")?.arguments
      .idempotencyKey,
  ).toBeTruthy();
  await ui.getByLabel("Search reports").fill("missing");
  await expect(ui.getByLabel("Report list")).toContainText("No reports match");
  await ui.getByLabel("Search reports").fill("stale");
  await expect(
    ui.getByRole("button", { name: "Agents: Stale response" }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Refresh issues and releases" }).click();
  await expect
    .poll(() => calls.some((c) => c.name === "refresh_issue_reports"))
    .toBeTruthy();
  expect(calls.some((c) => c.name === "start_run")).toBeFalsy();
});
test("reported issues retries uncertain evidence without duplicating it and accepts edited evidence", async ({
  page,
}) => {
  const report = reportFixture();
  const accepted = new Map<string, string>();
  let attempts = 0;
  const { ui, state, calls } = await host(page, {
    reports: [report],
    addEvidence: async (result, args) => {
      attempts++;
      const key = String(args.idempotencyKey);
      const text = String(args.text);
      if (accepted.has(key) && accepted.get(key) !== text)
        return {
          isError: true,
          content: [
            { type: "text", text: "Evidence key conflicts with accepted text" },
          ],
        };
      if (!accepted.has(key)) {
        accepted.set(key, text);
        result.additions.push(text);
        result.processing = "queued";
        state.cursor++;
      }
      // The daemon accepted the first payload, but both acknowledgements are lost.
      if (attempts <= 2)
        return {
          isError: true,
          content: [{ type: "text", text: "Evidence acknowledgement lost" }],
        };
      return {
        content: [],
        structuredContent: { data: structuredClone(result) },
      };
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  const evidence = detail.getByLabel("What did the host display?");
  const add = detail.getByRole("button", { name: "Add evidence", exact: true });
  const original = "Work displayed an expired request error";
  await evidence.fill(original);
  await add.click();
  await expect(ui.getByRole("alert")).toContainText(
    "Evidence acknowledgement lost",
  );
  await expect(evidence).toHaveValue(original);
  await expect(detail.getByText(original, { exact: true })).toHaveCount(1);
  await add.click();
  await expect.poll(() => attempts).toBe(2);
  await expect(add).toBeEnabled();
  await expect(evidence).toHaveValue(original);
  expect(report.additions).toEqual([original]);
  const additions = () => calls.filter((c) => c.name === "add_issue_evidence");
  expect(additions()[1].arguments.idempotencyKey).toBe(
    additions()[0].arguments.idempotencyKey,
  );
  const edited = "Work displayed a newer permission error";
  await evidence.fill(edited);
  await add.click();
  await expect(evidence).toHaveValue("");
  await expect(detail.getByText(edited, { exact: true })).toBeVisible();
  expect(report.additions).toEqual([original, edited]);
  expect(additions()[2].arguments.idempotencyKey).not.toBe(
    additions()[0].arguments.idempotencyKey,
  );
});

test("reported issues displays independent closure, prerelease delivery and explicit workflow recovery", async ({
  page,
}) => {
  const report = reportFixture();
  report.processing = "needs_attention";
  report.outcome = "filed";
  report.issue = {
    repository: "dk-code-factory/code-factory-plugin",
    number: 17,
    url: "https://github.com/dk-code-factory/code-factory-plugin/issues/17",
    state: "closed",
    closureReason: "completed",
  };
  report.releases = [
    {
      repository: "dk-code-factory/agent-workflow",
      version: "v2.0.0-rc1",
      componentVersion: "2.0.0-rc1",
      publishedAt: "2026-10-25T12:00:00Z",
      url: "https://github.com/dk-code-factory/agent-workflow/releases/tag/v2.0.0-rc1",
      commit: "abc",
      prerelease: true,
    },
  ];
  report.releases[0].prerelease = true;
  report.releaseSync.error = "GitHub rate limit";
  const { ui, calls, openLinks } = await host(page, { reports: [report] });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText(
    "State: closed · Closure reason: completed",
  );
  await expect(detail).toContainText("Linked fix: Unknown");
  await expect(detail).toContainText("Stable availability: Unknown");
  await expect(detail).toContainText(
    "Prerelease availability: dk-code-factory/agent-workflow v2.0.0-rc1",
  );
  await expect(detail).toContainText("GitHub rate limit");
  const resume = ui.getByRole("button", { name: "Resume or reconcile report" });
  await expect(resume).toBeDisabled();
  await ui
    .getByLabel("Recovery note")
    .fill("Inspected stopped workflow attempts and verified no pending writes");
  await ui
    .getByRole("checkbox", { name: "I inspected the retained workflow" })
    .check();
  await resume.click();
  await expect(detail).toContainText("Recovery admitted");
  expect(
    calls.find((c) => c.name === "resume_issue_report")?.arguments,
  ).toMatchObject({ id: "report-1", reconciled: true });
  await ui.getByRole("button", { name: "Open GitHub issue #17" }).click();
  await expect.poll(() => openLinks).toEqual([report.issue.url]);
});

test("reported issues makes detail progress while journal updates continue", async ({
  page,
}) => {
  const report = reportFixture();
  report.processing = "needs_attention";
  report.findings!.summary = "Slow detail findings";
  const pendingReads: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports: [report],
    readReport: async (result) => {
      await new Promise<void>((resolve) => {
        pendingReads.push(resolve);
      });
      return result;
    },
  });
  async function journalUpdate() {
    const lists = calls.filter((c) => c.name === "list_issue_reports").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "list_issue_reports").length)
      .toBeGreaterThan(lists);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  await expect.poll(() => pendingReads.length).toBe(1);
  // Each response is slower than multiple actual journal polling cycles.
  await journalUpdate();
  await journalUpdate();
  pendingReads[0]();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText("Slow detail findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  await expect.poll(() => pendingReads.length).toBe(2);
  await journalUpdate();
  await journalUpdate();
  await expect(detail).toContainText("Slow detail findings");
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  pendingReads[1]();
  await expect.poll(() => pendingReads.length).toBe(3);
  pendingReads[2]();
});

test("reported issues keeps newer journal details after a delayed mutation read", async ({
  page,
}) => {
  const report = reportFixture();
  const state = initial();
  report.processing = "blocked";
  let releaseRead!: () => void;
  const pendingRead = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  let delayNext = false;
  let delayed = false;
  const { ui } = await host(page, {
    snapshot: state,
    reports: [report],
    resumeReport: async (result) => {
      // Deliver the journal event only after the mutation read is pending.
      state.cursor--;
      return result;
    },
    readReport: async (result) => {
      if (delayNext) {
        delayNext = false;
        delayed = true;
        await pendingRead;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail.getByRole("definition").first()).toHaveText("blocked");
  delayNext = true;
  await detail
    .getByRole("button", { name: "Resume or reconcile report" })
    .click();
  await expect.poll(() => delayed).toBe(true);
  report.processing = "needs_attention";
  report.findings!.summary = "Newer journal findings";
  state.cursor++;
  await expect(detail).toContainText("Newer journal findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  releaseRead();
  await expect(
    ui.getByRole("button", { name: "Refresh issues and releases" }),
  ).toBeEnabled();
  await expect(detail).toContainText("Newer journal findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
});

test("reported issues keeps a pending selection read when an older selection mutation finishes", async ({
  page,
}) => {
  const oldReport = reportFixture();
  oldReport.processing = "blocked";
  const selectedReport = reportFixture("report-2");
  selectedReport.observation.actual = "Selected report response";
  selectedReport.findings!.summary = "Selected report findings";
  let releaseMutation!: () => void;
  let releaseRead!: () => void;
  const pendingMutation = new Promise<void>((resolve) => {
    releaseMutation = resolve;
  });
  const pendingRead = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  let mutationPending = false;
  let readPending = false;
  const { ui, calls } = await host(page, {
    reports: [oldReport, selectedReport],
    resumeReport: async (result) => {
      mutationPending = true;
      await pendingMutation;
      return result;
    },
    readReport: async (result) => {
      if (result.id === selectedReport.id) {
        readPending = true;
        await pendingRead;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail.getByRole("definition").first()).toHaveText("blocked");
  const reads = calls.filter((c) => c.name === "get_issue_report").length;
  await detail
    .getByRole("button", { name: "Resume or reconcile report" })
    .click();
  await expect.poll(() => mutationPending).toBe(true);
  // Consume the mutation journal event before starting the new selection read.
  await expect
    .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
    .toBeGreaterThan(reads);
  await ui
    .getByRole("button", { name: "Agents: Selected report response" })
    .click();
  await expect.poll(() => readPending).toBe(true);
  releaseMutation();
  await expect(
    ui.getByRole("button", { name: "Refresh issues and releases" }),
  ).toBeEnabled();
  releaseRead();
  await expect(detail).toContainText("Report report-2");
  await expect(detail).toContainText("Selected report findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs information",
  );
  await expect(
    detail.getByRole("button", { name: "Resume or reconcile report" }),
  ).toBeEnabled();
});

test("reported issues releases mutation controls during continuing slow journal refreshes", async ({
  page,
}) => {
  const report = reportFixture();
  let slow = false;
  const pending: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports: [report],
    listReports: async (result) => {
      if (slow)
        await new Promise<void>((resolve) => {
          pending.push(resolve);
        });
      return result;
    },
  });
  async function journalUpdate() {
    const reads = calls.filter((c) => c.name === "get_issue_report").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
      .toBeGreaterThan(reads);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  const evidence = detail.getByLabel("What did the host display?");
  const refresh = ui.getByRole("button", {
    name: "Refresh issues and releases",
  });
  await expect(evidence).toBeEnabled();
  slow = true;
  await refresh.click();
  await expect.poll(() => pending.length).toBe(1);
  await expect(refresh).toBeDisabled();
  await expect(evidence).toBeDisabled();
  await journalUpdate();
  await journalUpdate();
  pending[0]();
  await expect.poll(() => pending.length).toBe(2);
  // The mutation refresh rendered; a queued journal refresh is still pending.
  await journalUpdate();
  await journalUpdate();
  await expect(refresh).toBeEnabled();
  await expect(evidence).toBeEnabled();
  await expect(
    detail.getByRole("button", { name: "Resume or reconcile report" }),
  ).toBeEnabled();
  await evidence.fill("Evidence supplied while journal refresh is pending");
  await detail.getByRole("button", { name: "Add evidence" }).click();
  await expect(detail).toContainText(
    "Evidence supplied while journal refresh is pending",
  );
  await expect(evidence).toBeEnabled();
  expect(pending).toHaveLength(2);
  slow = false;
  pending[1]();
});

test("reported issues allows pagination during continuing slow journal refreshes", async ({
  page,
}) => {
  const reports = Array.from({ length: 65 }, (_, i) =>
    reportFixture(`report-${String(i).padStart(2, "0")}`),
  );
  reports[59].observation.actual = "Second page report";
  reports[64].observation.actual = "Third page report";
  let slow = false;
  const pending: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports,
    listReports: async (result) => {
      const response = structuredClone(result);
      if (slow)
        await new Promise<void>((resolve) => {
          pending.push(resolve);
        });
      return response;
    },
  });
  async function journalUpdate() {
    const reads = calls.filter((c) => c.name === "get_issue_report").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
      .toBeGreaterThan(reads);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  const list = ui.getByLabel("Report list", { exact: true });
  await expect(list.getByRole("article")).toHaveCount(30);
  await list
    .getByRole("button", { name: "Agents: Stale response" })
    .first()
    .click();
  await expect(
    ui.getByRole("article", { name: "Report details" }),
  ).toBeVisible();
  const more = list.getByRole("button", { name: "Load more reports" });
  slow = true;
  await journalUpdate();
  await expect.poll(() => pending.length).toBe(1);
  await journalUpdate();
  await journalUpdate();
  await expect(more).toBeEnabled();
  await more.dblclick();
  await expect(more).toBeDisabled();
  expect(pending).toHaveLength(1);
  pending[0]();
  await expect.poll(() => pending.length).toBe(2);
  await journalUpdate();
  pending[1]();
  await expect(list.getByRole("article")).toHaveCount(60);
  await expect(list).toContainText("Second page report");
  await expect(list).not.toContainText("Third page report");
  await expect.poll(() => pending.length).toBe(3);
  await expect(more).toBeEnabled();
  // Background refresh remains active after the requested page was rendered.
  await journalUpdate();
  await journalUpdate();
  await expect(more).toBeEnabled();
  expect(pending).toHaveLength(3);
  expect(
    await list.getByRole("article").locator("p").allTextContents(),
  ).toEqual(
    reports
      .slice(0, 60)
      .map((r) => `needs information · ${r.component} · ${r.id}`),
  );
  pending[2]();
  await expect.poll(() => pending.length).toBe(4);
  slow = false;
  pending[3]();
});

test("reported issues paginates and filters processing component and version", async ({
  page,
}) => {
  const reports = Array.from({ length: 32 }, (_, i) =>
    reportFixture(`report-${String(i).padStart(2, "0")}`),
  );
  reports[31].processing = "complete";
  reports[31].component = "plugin";
  reports[31].installedVersion = "1.2.3";
  reports[31].observation.actual = "Final report";
  let releasePage: (() => void) | undefined;
  const pendingPage = new Promise<void>((resolve) => {
    releasePage = resolve;
  });
  let delayed = false;
  const { ui, calls, state } = await host(page, {
    reports,
    listReports: async (result, args) => {
      if (args.cursor && !delayed) {
        delayed = true;
        await pendingPage;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  const list = ui.getByLabel("Report list", { exact: true });
  await expect(list.getByRole("article")).toHaveCount(30);
  await list
    .getByRole("button", { name: "Agents: Stale response" })
    .first()
    .click();
  await expect(
    ui.getByRole("article", { name: "Report details" }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Load more reports" }).click();
  await expect.poll(() => delayed).toBe(true);
  const reads = calls.filter((c) => c.name === "get_issue_report").length;
  state.cursor++;
  await expect
    .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
    .toBeGreaterThan(reads);
  releasePage!();
  await expect(list.getByRole("article")).toHaveCount(32);
  reports[31].observation.actual = "Updated final report";
  state.cursor++;
  await expect(list).toContainText("Updated final report");
  await expect(list.getByRole("article")).toHaveCount(32);
  expect(
    await list.getByRole("article").locator("p").allTextContents(),
  ).toEqual(
    reports.map(
      (r) => `${r.processing.replaceAll("_", " ")} · ${r.component} · ${r.id}`,
    ),
  );
  await ui.getByLabel("Processing", { exact: true }).selectOption("complete");
  await ui.getByLabel("Component", { exact: true }).fill("plugin");
  await ui.getByLabel("Version", { exact: true }).fill("1.2.3");
  await expect(list.getByRole("article")).toHaveCount(1);
  await expect(list).toContainText("Updated final report");
  await expect
    .poll(() =>
      calls.some(
        (c) =>
          c.name === "list_issue_reports" &&
          c.arguments.status === "complete" &&
          c.arguments.component === "plugin" &&
          c.arguments.version === "1.2.3",
      ),
    )
    .toBeTruthy();
  await ui.getByLabel("Processing", { exact: true }).selectOption("");
  await ui.getByLabel("Component", { exact: true }).fill("");
  await ui.getByLabel("Version", { exact: true }).fill("");
  await expect(list.getByRole("article")).toHaveCount(30);
  await expect(
    ui.getByRole("button", { name: "Load more reports" }),
  ).toBeEnabled();
});
test("repository review pipeline overrides system defaults and plugin skills remain selectable", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].item.kind = "pr";
  snapshot.inbox[0].item.reviewRequests = ["alex"];
  snapshot.profiles.push(
    { ...snapshot.profiles[0], id: "system-review", name: "System reviewer" },
    { ...snapshot.profiles[0], id: "repo-review", name: "Repository reviewer" },
  );
  snapshot.config.reviewProfileId = "system-review";
  snapshot.repositories = [
    {
      id: "owner/repo",
      checkout: "",
      base: "",
      lastSync: new Date().toISOString(),
      error: "",
      reviewProfileId: "repo-review",
      autoReview: false,
    },
  ];
  snapshot.skills = [
    {
      id: "plugin-skill",
      name: "Plugin review policy",
      path: "/plugins/review/SKILL.md",
      content: "Fixture skill",
      managed: false,
    },
    {
      id: "missing-skill",
      name: "Missing plugin skill",
      path: "/missing/SKILL.md",
      content: "",
      managed: false,
      disabled: true,
    },
  ];
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
  await ui.getByRole("button", { name: /P1 Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Agent pipeline", { exact: true })).toHaveValue(
    "repo-review",
  );
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await showSettings(page);
  await ui.getByText("Launch profiles", { exact: true }).click();
  await ui
    .getByLabel("Edit profile", { exact: true })
    .selectOption("repo-review");
  await ui
    .getByRole("checkbox", { name: "Plugin review policy", exact: true })
    .check();
  await expect(
    ui.getByRole("checkbox", { name: /Missing plugin skill/ }),
  ).toBeDisabled();
  await ui.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "save_profile").at(-1)?.arguments.skills,
    )
    .toEqual(["plugin-skill"]);
});

for (const theme of ["light", "dark"] as const) {
  test(`review queues from multiple repositories load full details from compact snapshots in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.inbox = ["owner/first", "owner/second"].map((repository, index) => {
      const entry = structuredClone(snapshot.inbox[0]);
      entry.item = {
        ...entry.item,
        id: `${repository}#${index + 1}`,
        repository,
        number: index + 1,
        kind: "pr",
        title: `Review from repository ${index + 1}`,
        author: "other",
        head: `head-${index + 1}`,
        reviewRequests: ["alex"],
        activity: [],
      };
      entry.queues = ["review"];
      return entry;
    });
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      readItem: async (detail) => ({
        ...detail,
        item: {
          ...detail.item,
          activity: [{ id: "comment:1", kind: "comment", author: "reviewer", body: "Full history is available on demand.", url: "" }],
          checks: { result: "Full checks retained" },
          pr: { id:detail.item.id,title:detail.item.title,url:detail.item.url,state:"open",draft:false,head:detail.item.head,reviewDecision:"none",ci:"success",syncedAt:new Date().toISOString(),checks:[{name:"Full current-head check",status:"completed",conclusion:"success",url:""}] },
        },
      }),
    });
    await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
    await expect(ui.getByRole("region", { name: "Items" }).getByRole("button")).toHaveCount(2);
    expect(calls.filter((c) => c.name === "read_item")).toHaveLength(0);
    await ui.getByRole("button", { name: /Review from repository 2/ }).click();
    await expect(ui.getByRole("heading", { name: "Review from repository 2" })).toBeVisible();
    await ui.getByRole("region",{name:"PR monitoring",exact:true}).getByText("CI checks",{exact:true}).click();
    await expect(ui.getByText(/Full current-head check/)).toBeVisible();
    await ui.getByText("Item details", { exact: true }).click();
    await ui.getByText("Activity", { exact: true }).click();
    await expect(ui.getByText("Full history is available on demand.", { exact: true })).toBeVisible();
    await ui.getByText("Checks", { exact: true }).click();
    await expect(ui.getByText(/Full checks retained/)).toBeVisible();
    expect(calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name))).toHaveLength(0);
    expect(calls.filter((c) => c.name === "read_item").every((c) => c.arguments.id === "owner/second#2")).toBe(true);
  });
}

for (const theme of ["light", "dark"] as const) {
  test(`GitHub usage settings show cached quota and save budget without polling in ${theme}`, async ({page})=>{
    await page.setViewportSize({width:390,height:820});
    const snapshot=initial();snapshot.githubUsage={requestsLastHour:12,budget:600,reserve:100,nextRequest:"",restRemaining:1200,restReset:"",graphqlRemaining:2400,graphqlReset:""};
    const {ui,calls}=await host(page,{snapshot,theme,surface:"settings"});
    await ui.getByText("System defaults",{exact:true}).click();
    const usage=ui.getByRole("group",{name:"GitHub API usage"});
    await expect(usage).toContainText("12 / 600 requests");
    await expect(usage).toContainText("REST remaining: 1200");
    await usage.getByLabel("Request budget per hour",{exact:true}).fill("120");
    await usage.getByLabel("Keep GitHub requests in reserve",{exact:true}).fill("200");
    await usage.getByLabel("Review notification poll interval",{exact:true}).selectOption("300");
    await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
    await expect.poll(()=>calls.filter(c=>c.name==="save_settings").length).toBe(1);
    expect(calls.find(c=>c.name==="save_settings")!.arguments).toMatchObject({githubRequestsPerHour:120,githubReserve:200,githubPollSeconds:300});
    expect(calls.some(c=>["sync_repositories","read_item","start_run","prepare_launch"].includes(c.name))).toBe(false);
    await expect(ui.locator("body")).toHaveJSProperty("scrollWidth",await ui.locator("body").evaluate(e=>e.clientWidth));
    await scanOwnedApp(page,`github-budget-${theme}`);
  });
}

test("a deferred detail refresh retains saved information and unread state until a successful retry",async({page})=>{
  let deferred=true;
  const {ui,calls}=await host(page,{readItem:async detail=>({...detail,refreshError:deferred?"Could not refresh details; showing saved information. GitHub request reserve reached.":""})});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByRole("alert")).toContainText("showing saved information");
  await expect(ui.getByRole("heading",{name:"Fix reconnect regression",exact:true})).toBeVisible();
  expect(calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read")).toHaveLength(0);
  deferred=false;await ui.getByRole("button",{name:"Retry detail refresh",exact:true}).click();
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect.poll(()=>calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read").length).toBe(1);
  expect(calls.some(c=>["prepare_launch","start_run"].includes(c.name))).toBe(false);
});

test("linked PR changes refresh full selected details without consuming another read or launch",async({page})=>{
 const snapshot=initial();
 snapshot.inbox[0].linkedPullRequests=[{id:"owner/repo#2",title:"Linked implementation",url:"https://github.com/owner/repo/pull/2",state:"open",draft:false,head:"abc",reviewDecision:"approved",ci:"success",syncedAt:new Date().toISOString(),checks:[{name:"Initial CI",status:"completed",conclusion:"success",url:""}]}];
 const {ui,calls,state}=await host(page,{snapshot,readItem:async detail=>({...detail,linkedPullRequests:structuredClone(snapshot.inbox[0].linkedPullRequests)})});
 await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
 const linked=ui.getByRole("region",{name:"Linked pull requests",exact:true});
 await expect(linked).toContainText("success");
 snapshot.inbox[0].linkedPullRequests![0].ci="failure";
 snapshot.inbox[0].linkedPullRequests![0].checks=[{name:"Latest CI",status:"completed",conclusion:"failure",url:""}];
 state.cursor++;
 await expect(linked).toContainText("failure");
 await linked.getByText("CI checks",{exact:true}).click();
 await expect(linked.getByText(/Latest CI/)).toBeVisible();
 expect(calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read")).toHaveLength(1);
 expect(calls.some(c=>["prepare_launch","start_run"].includes(c.name))).toBe(false);
});

test("Markdown issue headings do not replace the useful conversation summary", async ({ page }) => {
  const snapshot = initial();
  const paragraph = "The PR capacity helper must clamp exhausted capacity to zero.";
  snapshot.inbox[0].item.body = `## Issue\n\n### Context\n\n${paragraph}\n\n## Scope\n\nNo persistence changes.`;
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const summary = ui.getByRole("region", { name: "Item summary", exact: true });
  await expect(summary).toHaveText(paragraph);
  await showItemDetails(ui);
  await ui.getByText("Full description", { exact: true }).click();
  await expect(ui.getByText("Full description", { exact: true }).locator("..")).toContainText("## Issue");
  await expect(ui.getByText("Full description", { exact: true }).locator("..")).toContainText("No persistence changes.");
  expect(calls.some((c) => ["prepare_launch", "start_run"].includes(c.name))).toBe(false);
});


test("fresh read markers observe external changes after a host memoizes an empty journal page", async ({ page }) => {
  const { ui, calls, state } = await host(page, { memoizeReads: true });
  await expect.poll(() => calls.filter(c => c.name === "read_changes").length).toBeGreaterThan(0);
  state.inbox[0].item.title = "Externally updated pilot review";
  state.cursor++;
  await expect(ui.getByRole("button", { name: /Externally updated pilot review/ })).toBeVisible();
  state.inbox[0].item.title = "Second external review update";
  state.cursor++;
  await expect(ui.getByRole("button", { name: /Second external review update/ })).toBeVisible();
  const reads = calls.filter(c => ["read_changes", "read_snapshot"].includes(c.name));
  expect(new Set(reads.map(c => c.arguments.freshness)).size).toBe(reads.length);
  expect(reads.every(c => typeof c.arguments.freshness === "string" && c.arguments.freshness.length > 0)).toBe(true);
  expect(calls.some(c => ["prepare_launch", "start_run", "send_message"].includes(c.name))).toBe(false);
});


test("explicit full-access launch shows never approvals and authorized publishing", async ({ page }) => {
  const snapshot = initial();
  snapshot.profiles[0].sandbox = "danger-full-access";
  snapshot.profiles[0].approval = "never";
  snapshot.profiles[0].publishing = "authorized";
  const { ui, calls, state } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(dialog).toContainText("danger-full-access · never");
  await expect(dialog).toContainText("Authorized");
  expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
  await dialog.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect.poll(() => calls.filter(c => c.name === "start_run").length).toBe(1);
  expect(state.runs[0].profile).toMatchObject({ sandbox: "danger-full-access", approval: "never", publishing: "authorized" });
  await showSettings(page);
  await expect(ui.getByText(/Automatic reviews use the selected pipeline.s sandbox and publishing/)).toBeVisible();
});

for (const theme of ["light", "dark"] as const) {
  test(`posted reviews show verified verdicts findings and refresh after completion in ${theme}`, async ({page}) => {
    await page.setViewportSize({width:390,height:820});
    const snapshot=initial();const entry=snapshot.inbox[0];
    Object.assign(entry.item,{kind:"pr",head:"current-head",author:"someone",reviewRequests:["alex"]});entry.queues=["review"];
    let response: PRReviews={itemId:entry.item.id,reviews:[],syncedAt:"0001-01-01T00:00:00Z",lastAttempt:"0001-01-01T00:00:00Z",error:"GitHub temporarily unavailable"};
    const {ui,calls,state,openedLinks}=await host(page,{snapshot,theme,readReviews:async()=>structuredClone(response)});
    await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    const reviews=ui.getByRole("region",{name:"Posted reviews",exact:true});
    await expect(reviews.getByText("Publication has not been verified.",{exact:true})).toBeVisible();
    await expect(reviews.getByText("GitHub temporarily unavailable",{exact:true})).toBeVisible();
    await expect(reviews.getByText(/No posted reviews found/)).toHaveCount(0);
    response={itemId:entry.item.id,syncedAt:new Date().toISOString(),lastAttempt:new Date().toISOString(),reviews:[
      {id:"9",url:"https://github.com/owner/repo/pull/1#pullrequestreview-9",author:"alex",state:"COMMENTED",head:"current-head",submittedAt:"2026-10-24T03:00:00Z",body:"Concrete verified review",comments:[{id:"10",url:"https://github.com/owner/repo/pull/1#discussion_r10",author:"alex",path:"lib/reactor.py",line:79,body:"Actionable P2 failure"}]},
      {id:"8",url:"https://github.com/owner/repo/pull/1#pullrequestreview-8",author:"other",state:"APPROVED",head:"earlier-head",submittedAt:"2026-10-23T03:00:00Z",body:"Earlier approval",comments:[]}
    ]};
    state.reviewVersions={[entry.item.id]:response.lastAttempt};state.cursor++;
    await expect(reviews.getByText("Concrete verified review",{exact:true})).toBeVisible();
    await expect(reviews.getByText("Actionable P2 failure",{exact:true})).toBeVisible();
    await expect(reviews).toContainText("COMMENTED");await expect(reviews).toContainText("APPROVED");
    await expect(reviews).toContainText("Current head");await expect(reviews).toContainText("Earlier head");
    await reviews.getByRole("link",{name:"Review by alex",exact:true}).click();
    await reviews.getByRole("link",{name:"lib/reactor.py:79",exact:true}).click();
    await expect.poll(()=>openedLinks).toEqual([response.reviews[0].url,response.reviews[0].comments[0].url]);
    response={...response,error:"Could not refresh posted reviews; showing saved information."};
    await reviews.getByRole("button",{name:"Refresh reviews",exact:true}).click();
    await expect(reviews.getByRole("alert")).toContainText("showing saved information");
    await expect(reviews.getByText("Concrete verified review",{exact:true})).toBeVisible();
    expect(calls.filter(c=>["start_run","prepare_launch","prepare_pr_review"].includes(c.name))).toHaveLength(0);
    expect(calls.filter(c=>c.name==="read_pr_reviews").every(c=>c.arguments.id===entry.item.id)).toBe(true);
    await scanOwnedApp(page,`${theme}-posted-reviews`);
  });
}

for (const theme of ["light", "dark"] as const) {
  test(`saved details and receipts render during background refresh in ${theme}`, async ({page}) => {
    const snapshot=initial();const entry=snapshot.inbox[0];
    Object.assign(entry.item,{kind:"pr",head:"current-head",author:"someone",reviewRequests:["alex"]});entry.queues=["review"];
    let pending=true;
    const {ui,calls}=await host(page,{snapshot,theme,
      readItem:async(detail)=>({...detail,observation:{observedAt:"2026-10-24T01:00:00Z",refresh:pending?"pending":"fresh"}}),
      readReviews:async()=>({itemId:entry.item.id,refresh:pending?"pending":"fresh",syncedAt:"2026-10-24T01:00:00Z",lastAttempt:"2026-10-24T01:00:00Z",reviews:[{id:"saved",url:"https://github.com/owner/repo/pull/1#pullrequestreview-1",author:"alex",state:"COMMENTED",head:"current-head",submittedAt:"2026-10-24T01:00:00Z",body:"Saved receipt available immediately",comments:[]}]})
    });
    await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    await expect(ui.getByText("Refreshing saved details…",{exact:true})).toBeVisible();
    await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toBeVisible();
    await expect(ui.getByText("Saved receipt available immediately",{exact:true})).toBeVisible();
    pending=false;
    await expect(ui.getByText("Refreshing saved details…",{exact:true})).toHaveCount(0);
    await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toHaveCount(0);
    expect(calls.filter(c=>["start_run","prepare_launch","prepare_pr_review"].includes(c.name))).toHaveLength(0);
    expect(new Set(calls.filter(c=>c.name==="read_pr_reviews").map(c=>c.arguments.freshness)).size).toBeGreaterThan(1);
    await scanOwnedApp(page,`${theme}-saved-observations`);
  });
}
