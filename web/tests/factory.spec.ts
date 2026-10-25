import { test, expect, type Page, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import type { Snapshot, Run, Detail, IssueReport } from "../src/types";
function initial(): Snapshot {
  return {
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
        id: "investigate",
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
    reports?: IssueReport[];
    readItem?: (detail: Detail) => Promise<Detail>;
    failPreparation?: boolean;
    theme?: "light" | "dark";
    answerFailure?: "failed" | "uncertain";
    failRefreshAfterAnswer?: boolean;
    transcript?: string;
    recoveryInput?: boolean;
  } = {},
) {
  const state = options.snapshot ?? initial();
  const reports = options.reports ?? [];
  let answered = false;
  let relationships: Detail["relationships"] = [];
  const calls: { name: string; arguments: Record<string, unknown> }[] = [];
  await page.exposeFunction(
    "hostCall",
    async (p: { name: string; arguments: Record<string, unknown> }) => {
      calls.push(p);
      const a = p.arguments;
      let data: unknown = null;
      switch (p.name) {
        case "read_snapshot":
        case "open_factory":
          if (answered && options.failRefreshAfterAnswer)
            return {
              isError: true,
              content: [{ type: "text", text: "Fixture refresh unavailable" }],
            };
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
          data = {
            items: structuredClone(rows.slice(0, limit)),
            nextCursor: rows.length > limit ? rows[limit - 1].id : "",
          };
          break;
        }
        case "get_issue_report":
          data = structuredClone(reports.find((r) => r.id === a.id));
          break;
        case "add_issue_evidence": {
          const report = reports.find((r) => r.id === a.id)!;
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
          data = structuredClone(report);
          break;
        }
        case "refresh_issue_reports":
          state.cursor++;
          data = null;
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
        case "save_repository":
          state.repositories = [
            {
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
            id: "run-1",
            itemId: String(a.itemId),
            profile: state.profiles[0],
            prompt: String(a.prompt),
            skills: [],
            worktree: "/local/worktrees/run-1",
            branch: "code-factory/run-1",
            base: "HEAD",
            threadId: "",
            turnId: "",
            status: "prepared",
            error: "",
            created: new Date().toISOString(),
          };
          state.runs = [run];
          data = run;
          state.cursor++;
          break;
        }
        case "start_run":
          state.runs[0].status = "waiting";
          state.runs[0].threadId = "thread-1";
          state.runs[0].turnId = "turn-1";
          state.attention = [
            {
              id: "attention-1",
              runId: "run-1",
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
          break;
        case "read_transcript":
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
                        params: {
                          delta:
                            options.transcript ?? "Inspecting reconnect logic",
                        },
                      },
                      at: new Date().toISOString(),
                    },
                    {
                      seq: state.cursor + 1,
                      kind: "run.output",
                      entity: String(a.id),
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
                                text: options.recoveryInput
                                  ? "Automatic question-only request"
                                  : "",
                              },
                            ],
                          },
                        },
                      },
                    },
                  ]
                : [],
            cursor: state.cursor,
            hasMore: false,
          };
          break;
        case "answer_request": {
          if (options.answerFailure === "failed")
            return {
              isError: true,
              content: [{ type: "text", text: "Fixture answer denied" }],
            };
          const request = state.attention.find((at) => at.id === a.id)!;
          const run = state.runs.find((r) => r.id === request.runId)!;
          state.attention = state.attention.filter((at) => at.id !== a.id);
          run.status = "idle";
          run.turnId = "";
          state.cursor++;
          answered = true;
          if (options.answerFailure === "uncertain")
            return {
              isError: true,
              content: [{ type: "text", text: "Answer delivery uncertain" }],
            };
          break;
        }
        case "dismiss_question":
          state.attention = state.attention.filter((at) => at.id !== a.id);
          state.cursor++;
          break;
        case "interrupt_run":
          state.questionRecoveries = [];
          state.runs.find((r) => r.id === a.id)!.status = "interrupted";
          state.runs.find((r) => r.id === a.id)!.turnId = "";
          state.cursor++;
          break;
        case "set_jira_credential":
          break;
        case "send_message":
          state.cursor++;
          break;
        default:
          data = {};
      }
      return { content: [], structuredContent: { data } };
    },
  );
  await page.route("**/host", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><iframe title="Code Factory app" src="/" style="width:100%;height:95vh;border:0"></iframe><script>window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;let result={};if(m.method==='ui/initialize'){result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Work test host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:${JSON.stringify(options.theme ?? "light")},displayMode:'inline'}}}else if(m.method==='tools/call'){result=await window.hostCall(m.params)}e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')});</script>`,
    }),
  );
  await page.goto("/host");
  const ui = page.frameLocator("iframe");
  await expect(
    ui.getByRole("heading", { name: "Repository inbox" }),
  ).toBeVisible();
  return { ui, state, calls };
}
async function tabTo(page: Page, target: Locator) {
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
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await ui.getByLabel("Link target").fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect(
      ui.locator(".detail p").filter({ hasText: "owner/repo#42" }),
    ).toBeVisible();
    await ui
      .getByRole("button", { name: "Record action", exact: true })
      .click();
    await expect(
      ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-detail`);
    await ui.getByRole("button", { name: "Launch agent", exact: true }).click();
    await expect(
      ui.getByRole("dialog", { name: "Launch agent" }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-launch`);
    await ui
      .getByRole("button", { name: "Review launch", exact: true })
      .click();
    await expect(
      ui.getByRole("heading", { name: "Review agent launch" }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-review`);
    await ui.getByRole("button", { name: "Start agent", exact: true }).click();
    await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
    await scanOwnedApp(page, `${theme}-agents`);
    await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
    await ui.getByRole("radio", { name: /Focused/ }).check();
    await expect(
      ui.getByRole("button", { name: "Answer", exact: true }),
    ).toBeEnabled();
    await scanOwnedApp(page, `${theme}-question`);
    await page.keyboard.press("Escape");
    await ui.getByRole("button", { name: "Settings", exact: true }).click();
    await expect(
      ui.getByRole("heading", { name: "Connection diagnostics" }),
    ).toBeVisible();
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
  const opener = ui.getByRole("button", { name: "Launch agent", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  const close = dialog.getByRole("button", { name: "Close launch" });
  await expect(close).toBeFocused();
  await expect(close).toHaveCSS("outline-style", "solid");
  await expect(close).toHaveCSS("outline-width", "3px");
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
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
    1,
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
  await tabTo(
    page,
    ui.getByRole("button", { name: "Launch agent", exact: true }),
  );
  await page.keyboard.press("Enter");
  await tabTo(
    page,
    ui.getByRole("button", { name: "Review launch", exact: true }),
  );
  await page.keyboard.press("Enter");
  await tabTo(
    page,
    ui.getByRole("button", { name: "Start agent", exact: true }),
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
  await expect(ui.getByRole("status", { name: "Run status" })).toHaveText(
    "idle",
  );
  await expect(ui.getByRole("status", { name: "Run status" })).toHaveAttribute(
    "aria-live",
    "polite",
  );
});

test("launch preparation failure is announced inside the dialog without losing keyboard edits", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { failPreparation: true });
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  const opener = ui.getByRole("button", { name: "Launch agent", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
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
  const { ui, calls } = await host(page);
  const row = ui.getByRole("button", { name: /Fix reconnect regression/ });
  await row.click();
  await expect(
    ui.getByRole("heading", { name: "Your action history" }),
  ).toBeVisible();
  await expect(row.locator(".dot")).toHaveCount(0);
  await expect(row.getByText("New", { exact: true })).toBeVisible();
  await ui.getByLabel("Link target").fill("owner/repo#draft");
  await ui.getByLabel("Priority override").selectOption("2");
  await ui.getByRole("button", { name: "Record action", exact: true }).click();
  await expect(
    ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
  ).toHaveCount(1);
  await expect(row).toContainText("Acted on");
  await expect(row.getByText("New", { exact: true })).toBeVisible();
  await ui.getByRole("button", { name: "Mark unread", exact: true }).click();
  await expect(row.locator(".dot")).toHaveCount(1);
  await ui
    .getByRole("button", { name: "Acknowledge new", exact: true })
    .click();
  await expect(row.getByText("New", { exact: true })).toHaveCount(0);
  await expect(row.locator(".dot")).toHaveCount(1);
  await expect(
    ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
  ).toHaveCount(1);
  await expect(ui.getByLabel("Link target")).toHaveValue("owner/repo#draft");
  await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  expect(
    calls.filter(
      (call) => call.name === "item_action" && call.arguments.action === "read",
    ),
  ).toHaveLength(1);
  expect(
    calls.filter((call) => ["prepare_launch", "start_run"].includes(call.name)),
  ).toHaveLength(0);
});

test("unlink removes the visible relationship without clearing unrelated detail drafts", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Link target").fill("owner/repo#42");
  await ui.getByRole("button", { name: "Add local link", exact: true }).click();
  const link = ui.locator(".detail p").filter({ hasText: "owner/repo#42" });
  await expect(link).toContainText("Local link");
  await ui.getByLabel("Link target").fill("owner/repo#next-draft");
  await ui.getByLabel("Priority override").selectOption("3");
  await link.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(link).toHaveCount(0);
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 2 }),
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
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await ui.getByLabel("Link target").fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect.poll(() => delayed).toBe(true);
    await ui.getByRole("button", { name: /Second retained item/ }).click();
    await expect(
      ui.getByRole("heading", { name: "Second retained item", level: 2 }),
    ).toBeVisible();
    await ui.getByLabel("Link target").fill("owner/repo#second-draft");
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
      ui.getByRole("heading", { name: "Second retained item", level: 2 }),
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
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 2 }),
  ).toBeVisible();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "item_action" && c.arguments.revision === "rev-1",
      ),
    )
    .toBe(true);
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  await ui.getByRole("button", { name: "Launch agent", exact: true }).click();
  await ui
    .getByLabel("Task prompt")
    .fill("Investigate the reconnect race with a focused test.");
  await ui.getByLabel("Model", { exact: true }).fill("local-model");
  await ui.getByRole("button", { name: "Review launch" }).click();
  await expect(
    ui.getByRole("heading", { name: "Review agent launch" }),
  ).toBeVisible();
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  expect(calls.find((c) => c.name === "prepare_launch")?.arguments.prompt).toBe(
    "Investigate the reconnect race with a focused test.",
  );
  expect(calls.find((c) => c.name === "prepare_launch")?.arguments.model).toBe(
    "local-model",
  );
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
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
  await expect(
    ui.getByRole("heading", { name: "Repository inbox" }),
  ).toBeVisible();
  expect(
    calls.filter((c) => c.name === "read_snapshot").length,
  ).toBeGreaterThan(snapshotsBeforeReopen);
  await expect(
    ui.getByRole("button", { name: "Needs attention 1", exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: /^Agents/ }).click();
  await expect(
    ui.getByRole("button", { name: /Investigation · waiting/ }),
  ).toHaveCount(1);
  await ui.getByRole("button", { name: /Investigation · waiting/ }).click();
  await expect(
    ui.getByText("/local/worktrees/run-1", { exact: true }),
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
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
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
  await ui.getByRole("button", { name: "Inbox", exact: true }).click();
  await ui.getByRole("button", { name: /^Agents/ }).click();
  await ui.getByRole("button", { name: /Investigation · idle/ }).click();
  await ui.getByLabel("Message agent").fill("Explain the test result.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "send_message").length)
    .toBe(1);
  expect(calls.find((c) => c.name === "send_message")?.arguments).toEqual({
    id: "run-1",
    text: "Explain the test result.",
    expectedTurn: "",
  });
  expect(calls.filter((c) => c.name === "start_run").length).toBe(1);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
});
test("plugin settings accept repository and secure credential without echo", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await ui.getByRole("button", { name: "Settings", exact: true }).click();
  await ui.getByLabel("GitHub repository").fill("owner/repo");
  await ui
    .getByLabel("Existing checkout (optional)")
    .fill("/home/user/project");
  await ui
    .getByRole("button", { name: "Save repository", exact: true })
    .click();
  await expect(
    ui.getByText("owner/repo · /home/user/project · Connected"),
  ).toBeVisible();
  const helper = ui.getByLabel("Question helper model (optional)");
  await expect(helper).toHaveValue("");
  await helper.fill("fixture-helper-model");
  await ui.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.find((c) => c.name === "save_settings")?.arguments
          .questionHelperModel,
    )
    .toBe("fixture-helper-model");
  const input = ui.getByLabel("Jira API token");
  await expect(input).toHaveAttribute("type", "password");
  await input.fill("secure-test-token");
  await ui.getByRole("button", { name: "Save credential securely" }).click();
  await expect(input).toHaveValue("");
  expect(
    calls.find((c) => c.name === "set_jira_credential")?.arguments.token,
  ).toBe("secure-test-token");
  await expect(ui.locator("body")).not.toContainText("secure-test-token");
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
  async function request(p: ToolCall): Promise<ToolResult> {
    calls.push(p);
    const response = await fetch("http://127.0.0.1:4174/mcp", {
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
    if (r.frame().parentFrame() && /127\.0\.0\.1:4174|localhost/.test(r.url()))
      forbidden.push(r.url());
  });
  await page.goto("/assembled-host");
  const ui = page.frameLocator("iframe");
  await expect(
    ui.getByRole("heading", { name: "Repository inbox" }),
  ).toBeVisible();
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

test("repeated Start after a lost first response reports rejection and retains exactly one daemon run", async ({
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
    await ui.getByRole("button", { name: "Launch agent", exact: true }).click();
    await ui
      .getByRole("button", { name: "Review launch", exact: true })
      .click();
    const dialog = ui.getByRole("dialog", { name: "Launch agent" });
    const start = ui.getByRole("button", { name: "Start agent", exact: true });
    await start.dblclick();
    await expect(start).toBeDisabled();
    await expect
      .poll(() => calls.filter((call) => call.name === "start_run").length)
      .toBe(1);
    release();
    await expect(dialog.getByRole("alert")).toContainText(
      "Start response lost",
    );
    await expect(
      ui.getByRole("heading", { name: "Review agent launch" }),
    ).toBeVisible();
    await tabTo(page, start);
    await page.keyboard.press("Enter");
    await expect(dialog.getByRole("alert")).toContainText(
      /run cannot start from/,
    );
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
    ).toEqual([{ id: created[0].id }, { id: created[0].id }]);
    await page.keyboard.press("Escape");
    await expect(
      ui.getByRole("button", { name: "Launch agent", exact: true }),
    ).toBeFocused();
    await tabTo(page, ui.getByRole("button", { name: "Agents", exact: true }));
    await page.keyboard.press("Enter");
    await tabTo(page, ui.getByRole("button", { name: /· waiting/ }));
    await page.keyboard.press("Enter");
    await expect(
      ui.getByText(created[0].worktree, { exact: true }),
    ).toBeVisible();
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

test("dirty cleanup retains the daemon workspace and history until explicit Resume", async ({
  page,
}) => {
  const { ui, calls, acceptedStarts, forbidden, daemon } =
    await assembledHost(page);
  await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
  await ui.getByRole("button", { name: "Launch agent", exact: true }).click();
  await ui.getByRole("button", { name: "Review launch", exact: true }).click();
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(ui.locator(".detail .status")).toHaveText("idle");
  const run = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === acceptedStarts[0].id,
  )!;
  expect(relative(tmpdir(), run.worktree)).toMatch(
    /^factory-browser-[^/]+\/worktrees\/[a-f0-9]+$/,
  );
  const sentinel = join(run.worktree, "browser-dirty-fixture.txt");
  await writeFile(sentinel, "retain this browser fixture change\n");
  await ui.getByLabel("Message agent").fill("Retain this follow-up draft.");
  await ui
    .getByRole("button", { name: "Clean up worktree", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    /modified or untracked files|worktree/,
  );
  await expect(ui.locator(".detail .status")).toHaveText("interrupted");
  await expect(
    ui.getByRole("button", { name: "Resume agent", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeDisabled();
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
  await ui.getByRole("button", { name: "Resume agent", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Waiting for your answer/ }),
  ).toBeVisible();
  await expect(ui.locator(".detail .status")).toHaveText("waiting");
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
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain this follow-up draft.",
  );
  expect(
    calls
      .filter((call) => call.name === "start_run")
      .map((call) => call.arguments),
  ).toEqual([{ id: run.id }, { id: run.id, resume: true }]);
  expect(forbidden).toEqual([]);
});

test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up", async ({
  page,
}) => {
  const { ui, calls, forbidden } = await assembledHost(page);
  await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
  expect(calls.map((call) => call.name)).not.toContain("start_run");
  await ui.getByRole("button", { name: "Launch agent", exact: true }).click();
  await ui
    .getByLabel("Task prompt")
    .fill("Inspect the local fixture and ask which scope to investigate.");
  await ui.getByRole("button", { name: "Review launch" }).click();
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
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

function questionSnapshot(): Snapshot {
  const snap = initial();
  snap.runs = ["run-1", "run-2"].map((id, i) => ({
    id,
    itemId: snap.inbox[0].item.id,
    profile: { ...snap.profiles[0], name: `Agent ${i + 1}` },
    prompt: "Investigate",
    skills: [],
    worktree: `/local/${id}`,
    branch: id,
    base: "HEAD",
    threadId: id,
    turnId: `turn-${i + 1}`,
    status: "waiting",
    error: "",
    created: new Date().toISOString(),
  }));
  snap.attention = [
    {
      id: "question-1",
      runId: "run-1",
      origin: "rpc",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "scope",
            header: "Scope",
            question: "Which target?",
            isOther: true,
          },
        ],
      },
    },
  ];
  return snap;
}

test("answer confirmation expires despite polling, stays with its run and never alters transcript text", async ({
  page,
}) => {
  await page.clock.install();
  const { ui, state, calls } = await host(page, {
    snapshot: questionSnapshot(),
    transcript: "UI_ANSWER_OK ✓ Answer sent is agent text",
  });
  await ui
    .getByRole("button", { name: "Needs attention 1", exact: true })
    .click();
  await ui.getByRole("textbox", { name: "Which target?" }).fill("One");
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  const confirmation = ui.locator(".answer-confirmation");
  await expect(confirmation).toHaveText("✓ Answer sent");
  await expect(
    ui.getByRole("heading", { name: "Agent 1", exact: true }),
  ).toBeVisible();
  await expect(ui.locator(".transcript")).toContainText(
    "UI_ANSWER_OK ✓ Answer sent is agent text",
  );
  await ui
    .getByRole("textbox", { name: "Message agent" })
    .fill("Keep my draft");
  await ui.getByRole("button").filter({ hasText: "Agent 2 · waiting" }).click();
  await expect(confirmation).toHaveCount(0);
  await ui.getByRole("button").filter({ hasText: "Agent 1 · idle" }).click();
  await expect(confirmation).toHaveText("✓ Answer sent");
  await expect(ui.getByRole("textbox", { name: "Message agent" })).toHaveValue(
    "Keep my draft",
  );
  state.cursor++;
  await page.clock.runFor(4000);
  await expect(confirmation).toBeVisible();
  state.cursor++;
  await page.clock.runFor(1200);
  await expect(confirmation).toHaveCount(0);
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(1);
  await page.reload();
  await ui.getByRole("button", { name: /^Agents/ }).click();
  await ui.getByRole("button").filter({ hasText: "Agent 1 · idle" }).click();
  await expect(confirmation).toHaveCount(0);
});

for (const failure of ["failed", "uncertain"] as const) {
  test(`${failure} answers retain error feedback without confirmation or replay`, async ({
    page,
  }) => {
    const { ui, calls } = await host(page, {
      snapshot: questionSnapshot(),
      answerFailure: failure,
    });
    await ui
      .getByRole("button", { name: "Needs attention 1", exact: true })
      .click();
    await ui.getByRole("textbox", { name: "Which target?" }).fill("One");
    await ui.getByRole("button", { name: "Answer", exact: true }).click();
    await expect(ui.getByRole("alert")).toContainText(
      failure === "failed"
        ? "Fixture answer denied"
        : "Answer delivery uncertain",
    );
    await expect(ui.locator(".answer-confirmation")).toHaveCount(0);
    await page.waitForTimeout(2200);
    expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(1);
  });
}

test("successful answer keeps confirmation when the following refresh fails", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    snapshot: questionSnapshot(),
    failRefreshAfterAnswer: true,
  });
  await ui
    .getByRole("button", { name: "Needs attention 1", exact: true })
    .click();
  await ui.getByRole("textbox", { name: "Which target?" }).fill("One");
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(ui.locator(".answer-confirmation")).toHaveText("✓ Answer sent");
  await expect(ui.getByRole("alert")).toContainText(
    "Response sent, but refresh failed",
  );
  await expect(ui.getByRole("dialog")).toHaveCount(0);
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(1);
});

test("approval responses use Response sent and keep other run attention", async ({
  page,
}) => {
  const snap = questionSnapshot();
  snap.attention[0].method = "item/commandExecution/requestApproval";
  snap.attention[0].params = {
    command: "git status",
    availableDecisions: ["decline"],
  };
  snap.attention.push({
    ...snap.attention[0],
    id: "approval-2",
    runId: "run-2",
  });
  const { ui, state } = await host(page, { snapshot: snap });
  await ui
    .getByRole("button", { name: "Needs attention 2", exact: true })
    .click();
  await ui.getByRole("button", { name: "decline", exact: true }).click();
  await expect(ui.locator(".answer-confirmation")).toHaveText(
    "✓ Response sent",
  );
  expect(state.attention.map((a) => a.id)).toEqual(["approval-2"]);
});

for (const theme of ["light", "dark"] as const) {
  test(`recovery icons, fallback controls and keyboard navigation at narrow width in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snap = questionSnapshot();
    snap.questionRecoveries = [
      {
        id: "recovery-1",
        runId: "run-1",
        stage: "requesting-form",
        attempts: 2,
      },
    ];
    const { ui, state, calls } = await host(page, {
      snapshot: snap,
      theme,
      recoveryInput: true,
    });
    await ui.getByRole("button", { name: /^Agents/ }).click();
    await ui
      .getByRole("button")
      .filter({ hasText: "Agent 1 · waiting" })
      .click();
    await expect(
      ui.getByText("Recovering question · attempt 2 of 2"),
    ).toBeVisible();
    await expect(
      ui
        .locator(".transcript article")
        .filter({ hasText: "Automatic question-only request" })
        .locator("small"),
    ).toHaveText("Question recovery");
    const recovery = ui.locator("div.notice");
    await expect(recovery.locator("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    await expect(recovery.locator("svg path")).toHaveCount(2);
    await expect(
      ui.getByRole("status", { name: "Run status" }).locator("svg circle"),
    ).toHaveCount(1);
    await expect(
      ui.getByRole("textbox", { name: "Message agent" }),
    ).toBeEnabled();
    await ui
      .getByRole("textbox", { name: "Message agent" })
      .fill("Retain recovery draft");
    await expect(
      ui.getByRole("button", { name: "Send message", exact: true }),
    ).toBeDisabled();
    await scanOwnedApp(page, `${theme}-recovery-narrow`);
    await ui.getByRole("button", { name: "Take over", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(
      ui.getByRole("button", { name: "Take over", exact: true }),
    ).toHaveCount(0);
    expect(
      calls.some(
        (c) => c.name === "interrupt_run" && c.arguments.id === "run-1",
      ),
    ).toBe(true);
    state.attention[0].origin = "text";
    state.cursor++;
    await page.waitForTimeout(1200);
    const opener = ui.getByRole("button", {
      name: "Waiting for your answer — Answer",
    });
    await opener.focus();
    await page.keyboard.press("Enter");
    const inspect = ui.getByRole("button", {
      name: "Inspect technical transcript",
    });
    await tabTo(page, inspect);
    await page.keyboard.press("Enter");
    await expect(
      ui.getByRole("textbox", { name: "Message agent" }),
    ).toHaveValue("Retain recovery draft");
    await expect(
      ui.getByRole("heading", { name: "Technical transcript" }),
    ).toBeFocused();
    await opener.click();
    await scanOwnedApp(page, `${theme}-fallback-narrow`);
    await ui
      .getByRole("button", { name: "No reply needed", exact: true })
      .focus();
    await page.keyboard.press("Enter");
    await expect(ui.getByRole("dialog")).toHaveCount(0);
    expect(calls.filter((c) => c.name === "dismiss_question")).toHaveLength(1);
    expect(
      calls.filter(
        (c) => c.name === "answer_request" || c.name === "send_message",
      ),
    ).toHaveLength(0);
    expect(
      await ui.locator("body").evaluate((e) => e.scrollWidth <= e.clientWidth),
    ).toBe(true);
  });
}

test("text fallback submits its answer through the owning request", async ({
  page,
}) => {
  const snap = questionSnapshot();
  snap.attention[0].origin = "text";
  const { ui, calls } = await host(page, { snapshot: snap });
  await ui
    .getByRole("button", { name: "Needs attention 1", exact: true })
    .click();
  await ui
    .getByRole("textbox", { name: "Which target?" })
    .fill("My fallback answer");
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(ui.locator(".answer-confirmation")).toHaveText("✓ Answer sent");
  expect(
    calls.filter((c) => c.name === "answer_request").map((c) => c.arguments),
  ).toEqual([
    {
      id: "question-1",
      answer: { answers: { scope: { answers: ["My fallback answer"] } } },
    },
  ]);
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
  const { ui, calls } = await host(page, { reports: [report] });
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
  const { ui, calls } = await host(page, { reports });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  const list = ui.getByLabel("Report list", { exact: true });
  await expect(list.getByRole("article")).toHaveCount(30);
  await ui.getByRole("button", { name: "Load more reports" }).click();
  await expect(list.getByRole("article")).toHaveCount(32);
  await ui.getByLabel("Processing", { exact: true }).selectOption("complete");
  await ui.getByLabel("Component", { exact: true }).fill("plugin");
  await ui.getByLabel("Version", { exact: true }).fill("1.2.3");
  await expect(list.getByRole("article")).toHaveCount(1);
  await expect(list).toContainText("Final report");
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
});
