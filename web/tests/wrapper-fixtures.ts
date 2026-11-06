import { expect, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { fixtureDaemonOrigin } from "../fixture-ports";
export type Call = { name: string; arguments: Record<string, any> };
export function initial() {
  return {
    inboxTotal: 2,
    inbox: [
      {
        item: {
          id: "o/r#1",
          repository: "o/r",
          number: 1,
          kind: "issue",
          title: "Fix reconnect",
          body: "Retain work through disconnection",
          state: "open",
          url: "https://github.com/o/r/issues/1",
          revision: "v1",
          labels: ["P1"],
        },
        priority: 1,
        prioritySource: "label",
        unread: true,
        user: { new: true },
        queues: ["issues"],
        linkedPullRequests: [
          { id: "o/r#2", title: "Reconnect PR", state: "open" },
        ],
      },
      {
        item: {
          id: "o/r#2",
          repository: "o/r",
          number: 2,
          kind: "pr",
          title: "Reconnect PR",
          body: "PR context",
          state: "open",
          url: "https://github.com/o/r/pull/2",
          revision: "v1",
          labels: [],
          head: "abc123",
        },
        priority: null,
        prioritySource: "pending",
        unread: true,
        user: { new: false },
        queues: ["mine", "review"],
        linkedPullRequests: [],
      },
    ],
    sessions: [] as any[],
    questions: [] as any[],
    proposals: [] as any[],
    settings: {
      version: 1,
      quietSeconds: 120,
      repositories: [{ id: "o/r" }],
      rules: [],
    },
  };
}
const hostHTML = `<!doctype html><html lang="en"><title>Fixture host</title><iframe title="Code Factory" src="/" style="width:100%;height:95vh;border:0"></iframe><script>window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;try{let result={};if(m.method==='ui/initialize')result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Fixture host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:'light',displayMode:'inline'}};else if(m.method==='tools/call')result=await window.fixtureCall(m.params);else if(m.method==='ui/open-link')await window.fixtureOpenLink(m.params);e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*')}});</script></html>`;
export async function host(
  page: Page,
  options: {
    state?: ReturnType<typeof initial>;
    assembled?: boolean;
    archive?: Record<string, Record<string, any>[]>;
    observeToolCompletions?: boolean;
    fail?: string;
    lost?: string;
    settings?: boolean;
    transcript?: unknown[];
    transcriptPage?: (after: number) => unknown;
    relationships?: {
      source: string;
      target: string;
      evidence: string;
      manual: boolean;
    }[];
    theme?: "light" | "dark";
    before?: (call: Call) => Promise<void>;
    after?: (call: Call, data: unknown) => Promise<void>;
    reject?: (call: Call) => string | undefined;
  } = {},
) {
  const state = options.state ?? initial();
  const calls: Call[] = [];
  const toolCompletions: Call[] = [];
  if (options.observeToolCompletions) {
    await page.exposeFunction("fixtureToolCompleted", (call: Call) => {
      toolCompletions.push(call);
    });
    await page.route("**/src/bridge.ts", async (route) => {
      const response = await route.fetch();
      const source = await response.text();
      const signature = "export async function call(";
      if (source.split(signature).length !== 2)
        throw new Error("Expected one actual bridge call implementation");
      // Observe the actual SDK result decoding/resolution. The wrapper returns
      // the same result/error; two frames then let its React continuation and
      // read-acknowledgement effects run before notifying the test. Only this
      // fixture requests the observer; production modules contain no test hook.
      const body =
        source.replace(signature, "async function fixtureObservedCall(") +
        `
export async function call(name, args = {}) {
  try { return await fixtureObservedCall(name, args); }
  finally {
    requestAnimationFrame(() => requestAnimationFrame(() => {
      void window.fixtureToolCompleted({ name, arguments: args });
    }));
  }
}
`;
      await route.fulfill({ response, body });
    });
  }
  const stateReplies: { version: number; unchanged: boolean; view: string }[] =
    [];
  let stateVersion = 0;
  let stateFingerprint = "";
  const relationships = options.relationships ?? [
    { source: "o/r#1", target: "o/r#2", evidence: "manual", manual: true },
  ];
  const detailVersion = (id: string) =>
    createHash("sha256")
      .update(
        JSON.stringify([
          state.inbox.find((entry) => entry.item.id === id)?.item.revision,
          relationships.filter(
            (link) => link.source === id || link.target === id,
          ),
        ]),
      )
      .digest("hex");
  const forbidden: string[] = [];
  const openedLinks: string[] = [];
  await page.exposeFunction("fixtureOpenLink", (params: { url: string }) => {
    openedLinks.push(params.url);
  });
  page.on("request", (r) => {
    if (
      r.frame().parentFrame() &&
      r.url().includes(new URL(fixtureDaemonOrigin).host)
    )
      forbidden.push(r.url());
  });
  await page.exposeFunction("fixtureCall", async (p: Call) => {
    calls.push(p);
    await options.before?.(p);
    if (options.assembled) {
      const response = await fetch(fixtureDaemonOrigin + "/mcp", {
        method: "POST",
        headers: {
          Authorization: "Bearer " + "a".repeat(64),
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
                name: "fixture",
                version: "1",
              },
              "io.modelcontextprotocol/clientCapabilities": {},
            },
          },
        }),
      });
      const result = await response.json();
      if (result.error) throw new Error(JSON.stringify(result.error));
      return result.result;
    }
    const rejection = options.reject?.(p);
    if (p.name === options.fail || rejection)
      return {
        isError: true,
        content: [
          { type: "text", text: rejection || "Fixture operation failed" },
        ],
      };
    let data: any = null;
    const args = p.arguments;
    switch (p.name) {
      case "factory_state": {
        const view = args.view || "issues";
        const fingerprint = JSON.stringify([state, relationships]);
        if (fingerprint !== stateFingerprint) {
          stateFingerprint = fingerprint;
          stateVersion++;
        }
        if (args.sinceVersion === stateVersion) {
          data = { unchanged: true, version: stateVersion };
          stateReplies.push({ version: stateVersion, unchanged: true, view });
          break;
        }
        stateReplies.push({ version: stateVersion, unchanged: false, view });
        const filter = args.filter || "open";
        const search = String(args.search || "").toLowerCase();
        const rows = state.inbox.filter((entry) => {
          if (
            filter === "open" &&
            !["open", "unknown"].includes(entry.item.state)
          )
            return false;
          if (filter === "unread" && !entry.unread) return false;
          if (filter === "new" && !entry.user.new) return false;
          if (
            search &&
            ![
              entry.item.id,
              entry.item.title,
              entry.item.body,
              (entry.item as any).author,
              ...entry.item.labels,
            ]
              .join(" ")
              .toLowerCase()
              .includes(search)
          )
            return false;
          if (view === "issues") return entry.item.kind === "issue";
          if (view === "mine" || view === "review")
            return entry.queues.includes(view);
          if (view === "working")
            return (
              !(entry as any).work?.complete &&
              state.sessions.some((session) => session.itemId === entry.item.id)
            );
          return false;
        });
        const offset = args.offset || 0,
          limit = Math.min(200, args.limit || 50);
        data = {
          ...state,
          version: stateVersion,
          selectedEntry:
            view === "conversation"
              ? state.inbox
                  .filter((entry) => entry.item.id === args.itemId)
                  .map((entry) => ({
                    ...entry,
                    detailVersion: detailVersion(entry.item.id),
                    item: { ...entry.item, body: undefined },
                  }))[0]
              : undefined,
          inbox: rows.slice(offset, offset + limit).map((entry) => ({
            ...entry,
            item: { ...entry.item, body: undefined },
          })),
          inboxTotal: rows.length,
        };
        if (view !== "configuration") {
          data.settings = undefined;
          data.proposals = [];
        }
        break;
      }
      case "factory_item":
        data = {
          detailVersion: detailVersion(args.id),
          item: state.inbox.find((e) => e.item.id === args.id)?.item,
          relationships: relationships.filter(
            (link) => link.source === args.id || link.target === args.id,
          ),
        };
        break;
      case "session_transcript":
        data = options.transcriptPage?.(args.after || 0) ?? {
          events: args.after ? [] : options.transcript || [],
          cursor: 100,
          hasMore: false,
        };
        break;
      case "session_chat":
      case "configuration_chat": {
        const id =
          p.name === "configuration_chat" ? "configuration" : args.itemId;
        let s = state.sessions.find((s) => s.id === id);
        if (!s) {
          s = {
            id,
            itemId: id,
            kind: id === "configuration" ? "configuration" : "item",
            execution: "running",
            reported: "",
            pendingCount: 0,
            deliveryState: "delivered",
            agent: {},
            hold: false,
          };
          state.sessions.push(s);
        }
        s.pendingCount = 0;
        data = s;
        break;
      }
      case "session_control": {
        const s = state.sessions.find((s) => s.id === args.id);
        s.execution =
          args.action === "interrupt"
            ? "interrupted"
            : args.action === "cleanup"
              ? "cleaned"
              : "running";
        data = s;
        break;
      }
      case "answer_session_question": {
        const q = state.questions.find((q) => q.id === args.id);
        q.status = "answered";
        data = {};
        break;
      }
      case "confirm_configuration": {
        const p = state.proposals.find((p) => p.id === args.id);
        p.state = args.confirm ? "applied" : "rejected";
        data = p;
        break;
      }
      case "factory_events": {
        const kind = args.kind || "events";
        const source =
          options.archive?.[kind] ??
          (kind === "events"
            ? [{ id: "event-1", type: "pull_request", action: "synchronize" }]
            : kind === "rules"
              ? state.settings.rules
              : []);
        const offset = args.offset || 0;
        const limit = args.limit || 50;
        data = {
          kind,
          offset,
          limit,
          total: source.length,
          rows: source.slice(offset, offset + limit).map((row) => {
            const {
              id,
              repository,
              itemId,
              sessionId,
              at,
              type,
              action,
              state,
              preview,
              index,
              enabled,
              repositories,
              repositoriesCount,
              eventTypes,
              eventTypesCount,
              actions,
              actionsCount,
              skills,
              skillsCount,
            } = row;
            return {
              id,
              kind,
              repository,
              itemId,
              sessionId,
              at,
              type,
              action,
              state,
              preview,
              index,
              enabled,
              repositories,
              repositoriesCount,
              eventTypes,
              eventTypesCount,
              actions,
              actionsCount,
              skills,
              skillsCount,
            };
          }),
        };
        break;
      }
      case "inspect_configuration":
        data = {
          config: { profiles: { custom: { model: "fixture" } } },
          layers: [],
        };
        break;
      case "user_action": {
        const entry = state.inbox.find((e) => e.item.id === args.id)!;
        if (args.action === "priority") {
          entry.priority = args.priority;
          (entry.user as any).override = args.priority;
        }
        if (args.action === "acknowledge") entry.user.new = false;
        if (args.action === "unread") entry.unread = true;
        if (args.action === "read" && args.revision === entry.item.revision)
          entry.unread = false;
        break;
      }
      case "link_items":
        relationships.splice(
          0,
          relationships.length,
          ...relationships.filter(
            (link) =>
              link.source !== args.source || link.target !== args.target,
          ),
        );
        if (!args.remove)
          relationships.push({
            source: args.source,
            target: args.target,
            evidence: args.evidence,
            manual: true,
          });
        break;
      case "sync_repositories":
        break;
      case "resolve_session_delivery": {
        const session = state.sessions.find((s) => s.id === args.id);
        if (session) session.deliveryState = "";
        break;
      }
      default:
        throw new Error(`Unimplemented fixture tool ${p.name}`);
    }
    if (p.name === options.lost)
      throw new Error("Connection failed after acceptance");
    const responseData = structuredClone(data);
    await options.after?.(p, responseData);
    return {
      content: [{ type: "text", text: "Fixture result" }],
      structuredContent: { data: responseData },
    };
  });
  await page.route("**/wrapper-host", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: hostHTML.replace(
        "theme:'light'",
        `theme:'${options.theme || "light"}'`,
      ),
    }),
  );
  if (options.settings)
    await page.addInitScript(() => {
      if (window !== window.parent)
        document.addEventListener("DOMContentLoaded", () => {
          document.documentElement.dataset.codeFactoryView = "settings";
        });
    });
  await page.goto("/wrapper-host");
  const ui = page.frameLocator("iframe");
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected");
  const nextStateCallAfter = async (count: number) => {
    await expect
      .poll(() => calls.filter((call) => call.name === "factory_state").length)
      .toBeGreaterThan(count);
    return calls.filter((call) => call.name === "factory_state")[count];
  };
  return {
    ui,
    calls,
    state,
    forbidden,
    openedLinks,
    relationships,
    stateReplies,
    toolCompletions,
    nextStateCallAfter,
  };
}
