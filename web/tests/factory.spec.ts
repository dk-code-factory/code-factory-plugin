import { test, expect, type Page } from "@playwright/test";
import type { Snapshot, Run } from "../src/types";
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
async function host(page: Page) {
  const state = initial();
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
        case "read_item":
          data = {
            item: state.inbox[0].item,
            user: state.inbox[0].user,
            relationships: [],
            tickets: [],
          };
          break;
        case "item_action":
          if (a.action === "read") {
            state.inbox[0].user.readRevision = String(a.revision);
            state.inbox[0].unread = false;
          }
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
        case "answer_request":
          state.attention = [];
          state.runs[0].status = "idle";
          state.runs[0].turnId = "";
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
      body: `<!doctype html><iframe title="Code Factory app" src="/" style="width:100%;height:95vh;border:0"></iframe><script>window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;let result={};if(m.method==='ui/initialize'){result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Work test host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:'light',displayMode:'inline'}}}else if(m.method==='tools/call'){result=await window.hostCall(m.params)}e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')});</script>`,
    }),
  );
  await page.goto("/host");
  const ui = page.frameLocator("iframe");
  await expect(
    ui.getByRole("heading", { name: "Repository inbox" }),
  ).toBeVisible();
  return { ui, state, calls };
}
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
  expect(calls.find((c) => c.name === "prepare_launch")?.arguments.model).toBe(
    "local-model",
  );
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toBeVisible();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toHaveCount(0);
  expect(
    calls.find((c) => c.name === "answer_request")?.arguments.answer,
  ).toEqual({ answers: { scope: { answers: ["Focused"] } } });
  await ui.getByRole("button", { name: "Inbox", exact: true }).click();
  await ui.getByRole("button", { name: "Agents", exact: true }).click();
  await ui.getByRole("button", { name: /Investigation · idle/ }).click();
  await ui.getByLabel("Message agent").fill("Explain the test result.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "send_message").length)
    .toBe(1);
  expect(calls.filter((c) => c.name === "start_run").length).toBe(1);
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

test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up", async ({
  page,
}) => {
  const calls: string[] = [];
  await page.exposeFunction(
    "daemonCall",
    async (p: { name: string; arguments: Record<string, unknown> }) => {
      calls.push(p.name);
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
        result?: unknown;
        error?: unknown;
      };
      if (!response.ok || result.error)
        throw new Error("Daemon MCP request failed");
      return result.result;
    },
  );
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
  await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
  expect(calls).not.toContain("start_run");
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
    .poll(() => calls.filter((n) => n === "send_message").length)
    .toBe(1);
  expect(calls.filter((n) => n === "start_run")).toHaveLength(1);
  expect(forbidden).toEqual([]);
});
