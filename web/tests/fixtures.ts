import { test, expect, type Page, type Locator, type FrameLocator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { writeFile } from "node:fs/promises";
import type { Snapshot, Run, Detail, JournalEvent, PRReviews, IssueReport, ReportPage } from "../src/types";
import { fixtureUIOrigin, fixtureDaemonOrigin } from "../fixture-ports";

export function initial(): Snapshot {
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

export async function host(
  page: Page,
  options: {
    snapshot?: Snapshot;
    initializeFailures?: number;
    memoizeReads?: boolean;
    watchDomains?: string[];
    transportFault?: (name: string, args: Record<string, unknown>) => boolean;
    toolError?: { name: string; message: string };
    readItem?: (detail: Detail) => Promise<Detail>;
    readUIState?: (state: Snapshot, args: Record<string,unknown>) => Promise<Snapshot>;
    readReviews?: (id: string) => Promise<PRReviews>;
    readSettings?: (config: Snapshot["config"]) => Promise<Snapshot["config"]>;
    beforeDKImport?: (repository?: string) => Promise<void>;
    beforeReadReviewPolicy?: () => Promise<void>;
    afterSaveReviewPolicy?: () => Promise<void>;
    dkReviewSettings?: Pick<Snapshot["config"], "reviewers" | "reviewRules">;
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
    readRun?: (data:{run?:Run;attention:Attention[]})=>Promise<{run?:Run;attention:Attention[]}>;
    loseStartResponse?: boolean;
    beforeStartResponse?: () => Promise<void>;
    rejectStart?: boolean;
    transcript?: JournalEvent[];
    readTranscript?: (page:EventPage)=>Promise<EventPage>;
    theme?: "light" | "dark";
    denyOpenLink?: boolean;
    failOpenLink?: boolean;
    surface?: "settings";
  } = {},
) {
  const state = options.snapshot ?? initial();
  const prPolicies = new Map<string, boolean | null>();
  function postingPolicy(id: string) {
    const item = state.inbox.find((e) => e.item.id === id)?.item;
    const repo = state.repositories.find((r) => r.id === item?.repository);
    const pr = prPolicies.get(id);
    const source =
      pr != null
        ? "pr"
        : repo?.autoPostReview != null
          ? "repository"
          : "global";
    return {
      itemId: id,
      autoPostReview: pr ?? null,
      effective: {
        enabled:
          pr ?? repo?.autoPostReview ?? state.config.autoPostReview ?? false,
        source,
      },
    };
  }


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
  let transcriptLength=options.transcript?.length??0;
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
        case "read_ui_state": {
          const q=String(a.queue||"all");
          const query=String(a.search||"").toLowerCase();
          const filter=String(a.filter||"all");
          const rows=state.inbox.filter(v=>{
            const owns=v.item.author.toLowerCase()===state.viewer?.login.toLowerCase();
            const requested=v.item.reviewRequests?.some(l=>l.toLowerCase()===state.viewer?.login.toLowerCase())||v.item.reviewTeams?.some(t=>state.viewer?.teams?.includes(t));
            const member=v.queues?v.queues.includes(q):q==="issues"?v.item.kind==="issue":q==="mine"?v.item.kind==="pr"&&owns:q==="review"?v.item.kind==="pr"&&requested:q==="working"?state.runs.some(r=>r.itemId===v.item.id&&r.status!=="prepared")&&v.item.state==="open":true;
            return (q==="all"||member)&&(filter==="all"||filter==="open"&&v.item.state==="open"||filter==="pending"&&v.item.state==="open"&&!v.hasPullRequest||filter==="new"&&v.user.new||filter==="unread"&&v.unread||filter==="acted-on"&&v.user.actions.length>0)&&
              (v.item.title+" "+v.item.id+" "+v.item.body).toLowerCase().includes(query);
          }).sort((a,b)=>(a.priority??4)-(b.priority??4)||Date.parse(b.item.updated)-Date.parse(a.item.updated));
          const offset=Number(a.offset||0),limit=Number(a.limit||50);
          const views=(a.views as string[]|undefined)??["inbox"];
          const projected:Partial<Snapshot>={cursor:state.cursor};
          const copy=structuredClone(state);
          if(views.includes("shell")) Object.assign(projected,{viewer:copy.viewer,repositories:copy.repositories,profiles:copy.profiles,config:copy.config});
          if(views.includes("messages")) Object.assign(projected,{runs:copy.runs,attention:copy.attention,questionRecoveries:copy.questionRecoveries});
          if(views.includes("settings")||views.includes("launch")) Object.assign(projected,{repositories:copy.repositories,profiles:copy.profiles,config:copy.config,skills:copy.skills.map(skill=>({...skill,content:""})),githubUsage:copy.githubUsage});
          if(views.includes("skill")){
            const skill=copy.skills.find(skill=>skill.id===a.id);
            if(!skill||!skill.managed) throw new Error("only managed skills can be edited");
            Object.assign(projected,{skill,skillCursor:state.cursor});
          }
          if(views.includes("reviews")) projected.reviewVersions=copy.reviewVersions;
          if(views.includes("inbox")) Object.assign(projected,{inbox:structuredClone(rows.slice(offset,offset+limit)),page:{offset,limit,total:rows.length,hasMore:offset+limit<rows.length}});
          projected.selectedEntry=structuredClone(state.inbox.find(e=>e.item.id===a.id));
          data=options.readUIState?await options.readUIState(projected as Snapshot,a):projected;
          break;
        }
        case "open_factory":
          data=structuredClone(state);
          break;
        case "watch_ui_changes": {
          // Fixture long poll releases on durable changes or a short idle timeout.
          const until=Date.now()+500;
          while(state.cursor===Number(a.after)&&Date.now()<until) {
            if((options.transcript?.length??0)!==transcriptLength){
              transcriptLength=options.transcript?.length??0;state.cursor++;break;
            }
            await new Promise(r=>setTimeout(r,20));
          }
          data={cursor:state.cursor,resync:state.cursor<Number(a.after),changes:state.cursor!==Number(a.after)?{"item":state.inbox.map(e=>e.item.id),"user":state.inbox.map(e=>e.item.id),"run":state.runs.map(r=>r.id),"transcript":state.runs.map(r=>r.id),"attention":state.attention.map(r=>r.id),"pr-reviews":state.inbox.map(e=>e.item.id),"pr-review-policy":state.inbox.map(e=>e.item.id),"relationship":[],"observation":state.inbox.map(e=>e.item.id),"issue-report":reports.map(r=>r.id),"plugin-inventory":["current"],"plugin-operation":pluginOperations.map(r=>r.id),"config":["settings"],"repository":state.repositories.map(r=>r.id),"github-notifications":["cursor"]}:{}};
          if(state.cursor<Number(a.after)) (data as {changes:Record<string,string[]>}).changes={};
          if(options.watchDomains){
            const change=data as {changes:Record<string,string[]>};
            change.changes=Object.fromEntries(Object.entries(change.changes).filter(([domain])=>options.watchDomains!.includes(domain)));
          }
          break;
        }
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
        case "read_review_policy":
          await options.beforeReadReviewPolicy?.();
          data = postingPolicy(String(a.id));
          break;
        case "save_review_policy":
          if ("autoPostReview" in a)
            prPolicies.set(String(a.id), a.autoPostReview as boolean | null);
          data = postingPolicy(String(a.id));
          state.cursor++;
          await options.afterSaveReviewPolicy?.();
          break;
        case "import_dk_review_settings":
          await options.beforeDKImport?.(a.repository as string | undefined);
          Object.assign(
            a.repository
              ? state.repositories.find((repository) => repository.id === a.repository)!
              : state.config,
            options.dkReviewSettings,
          );
          data = {
            scope: a.repository ?? "global",
            imported: Object.keys(options.dkReviewSettings ?? {}),
            conflicts: ["Code Factory values preserved"],
            completed: true,
          };
          break;
        case "get_settings":
          data = options.readSettings
            ? await options.readSettings(structuredClone(state.config))
            : structuredClone(state.config);
          break;
        case "post_retained_review": {
          const run = state.runs.find((r) => r.id === a.runId)!;
          run.postingState = "verified";
          run.postingOperation = "op-1";
          run.postingReceipt = {
            id: "receipt",
            url: "https://github.com/owner/repo/pull/1#receipt",
            head: run.base,
            comments: 1,
          };
          state.cursor++;
          data = {
            id: "op-1",
            status: "verified",
            receipt: run.postingReceipt,
          };
          break;
        }
        case "reconcile_review_publication": {
          const run = state.runs.find((r) => r.postingOperation === a.id)!;
          run.postingState = "verified";
          run.postingReceipt = {
            id: "receipt",
            url: "https://github.com/owner/repo/pull/1#receipt",
            head: run.base,
            comments: 1,
          };
          state.cursor++;
          data = { id: a.id, status: "verified", receipt: run.postingReceipt };
          break;
        }
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
              autoPostReview: a.autoPostReview as boolean | null,
              reviewers:
                a.reviewers as Snapshot["repositories"][0]["reviewers"],
              reviewRules: a.reviewRules as string | null,

              reviewProfileId: String(a.reviewProfileId ?? ""),
              implementationProfileId: String(a.implementationProfileId ?? ""),
              reviewPrompt: String(a.reviewPrompt ?? ""),
              id: String(a.id),
              checkout: String(a.checkout),
              base: String(a.base),
              lastSync: "0001-01-01",
              error: "",
            },
            ...state.repositories.filter((repository) => repository.id !== a.id),
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
          if (
            state.inbox.find((e) => e.item.id === a.itemId)?.item.kind === "pr"
          ) {
            run.autoPostReview = a.autoPostReview as boolean | null;
            run.effectiveAutoPostReview =
              a.autoPostReview != null
                ? { enabled: Boolean(a.autoPostReview), source: "launch" }
                : postingPolicy(String(a.itemId)).effective;
          }
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
          if(options.readRun)data=await options.readRun(structuredClone(data as {run?:Run;attention:Attention[]}));
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
          data = structuredClone(state.runs[0]);
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
          if(options.readTranscript) data=await options.readTranscript(data as EventPage);
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
        case "interrupt_run": {
          const run = state.runs.find((r) => r.id === a.id);
          if (run?.status === "deferred") {
            run.status = "interrupted";
            run.resumePending = false;
            run.launchRetryAt = undefined;
            run.error = "";
            state.cursor++;
          }
          break;
        }
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
    const response = await route.fetch({ url: `${fixtureUIOrigin}/` });
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
      body: `<!doctype html><iframe title="Code Factory app" src="${options.surface === "settings" ? "/settings-ui" : "/"}" style="width:100%;height:95vh;border:0"></iframe><script>const readCache=new Map();window.addEventListener('message',async e=>{const m=e.data;if(!m||m.id===undefined)return;let result={};if(m.method==='ui/initialize'){window.initializationCount=(window.initializationCount||0)+1;if(window.initializationCount<=${options.initializeFailures ?? 0}){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:'McpServerError: Connection failed.'}},'*');return}result={protocolVersion:m.params.protocolVersion,hostInfo:{name:'Work test host',version:'1'},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:${JSON.stringify(options.theme ?? "light")},displayMode:'inline'}}}else if(m.method==='tools/call'){try{const key=JSON.stringify(m.params);if(${options.memoizeReads ?? false}&&['read_ui_state','watch_ui_changes','read_plugin_admin'].includes(m.params.name)){if(readCache.has(key))result=readCache.get(key);else{result=await window.hostCall(m.params);readCache.set(key,structuredClone(result))}}else result=await window.hostCall(m.params)}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*');return}}else if(m.method==='ui/open-link'){try{result=await window.hostOpenLink(m.params)}catch(error){e.source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:String(error)}},'*');return}}e.source.postMessage({jsonrpc:'2.0',id:m.id,result},'*')});</script>`,
    }),
  );
  await page.goto("/host");
  const ui = page.frameLocator('iframe[title="Code Factory app"]');
  await expect(
    ui.getByLabel(
      options.surface === "settings" ? "GitHub repository" : "Search inbox",
    ),
  ).toBeVisible();
  return { ui, state, calls, openedLinks, openLinks: openedLinks, prPolicies };
}

export async function showSettings(page: Page) {
  await page.locator("iframe").evaluate((frame: HTMLIFrameElement) => {
    frame.src = "/settings-ui";
  });
  await expect(
    page
      .frameLocator("iframe")
      .getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
}

export async function openItem(ui: FrameLocator, name: RegExp) {
  if (!(await ui.locator(".item-list").count()))
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await ui.getByRole("button", { name }).click();
  await showItemDetails(ui);
}

export async function showItemDetails(ui: FrameLocator) {
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

export async function showAgents(ui: FrameLocator) {
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

export async function openWaiting(ui: FrameLocator) {
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .first()
    .click();
}

export async function linkTarget(ui: FrameLocator) {
  await showItemDetails(ui);
  const input = ui.getByLabel("Link target");
  if (!(await input.isVisible()))
    await ui.getByText("Relationships", { exact: true }).click();
  return input;
}

export async function triage(ui: FrameLocator, name: string) {
  await showItemDetails(ui);
  const button = ui.getByRole("button", { name, exact: true });
  if (!(await button.isVisible()))
    await ui.getByText("Actions", { exact: true }).click();
  await button.click();
}

export async function tabTo(page: Page, target: Locator) {
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

export async function scanOwnedApp(page: Page, state: string) {
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

export function retained(
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

export type ToolCall = { name: string; arguments: Record<string, unknown> };

export type ToolResult = {
  isError?: boolean;
  content?: { type: string; text?: string }[];
  structuredContent?: { data: unknown };
};

export async function assembledHost(
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
    const response = await fetch(`${fixtureDaemonOrigin}/mcp`, {
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
  const fixtureDaemonHost = new URL(fixtureDaemonOrigin).host;
  const forbidden: string[] = [];
  page.on("request", (r) => {
    if (
      r.frame().parentFrame() &&
      (r.url().includes(fixtureDaemonHost) || /localhost/.test(r.url()))
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

export function reportFixture(id = "report-1"): IssueReport {
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
