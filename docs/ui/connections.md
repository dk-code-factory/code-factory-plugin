# UI connections and transitions

[Entity model](README.md) · [Widgets](widgets.md) · [Panes](panes.md) ·
[Pages](pages.md) · [Source/tests](references.md)

Containment is defined in the pane and page tables. This document records
interaction/data connections separately so a visual relationship is not mistaken
for a clickable navigation control.

## Relationship types

| Type | Meaning |
| --- | --- |
| contains | A page/dialog places a pane; a pane places widget instances; a widget names its elements. |
| selects | A control changes which record a pane displays, while staying on the same page. |
| navigates | The active logical page changes. |
| opens / closes | A dialog is shown/dismissed above a page. Closing alone has no service-side task effect. |
| submits | An explicit action changes application state or responds to a live request. |
| refreshes | Updated shared state supplies new values to widgets; it is not a navigation action. |
| references | A displayed identifier or local relationship names another domain record. It is not necessarily a clickable link. |
| external | A host-mediated link, artifact download or callback leaves the current owned surface. |

## Connection catalog

<!-- specs:connections-0 -->
| ID | From → to | Trigger and carried context | Result/type |
| --- | --- | --- | --- |
| `C-NAVIGATE` | `W-NAVIGATION` → one embedded `PAGE-*` | Working, Issues, My PRs, Review PRs, Messages or Reported issues tab. | Navigates between four task queues with independent query state; configuration remains a separate document. |
| `C-FILTER` | `W-INBOX-QUERY` → `P-INBOX-LIST` | Search text and filter value. | Applies per-tab kind/ownership/review membership, filter and query. Pending issues exclude linked PRs; lists sort by priority then update time. |
| `C-SELECT-ITEM` | Work row → item conversation | Click a row; item ID. | Loads and marks the displayed item revision read, continues the newest retained conversation or shows a prefilled first message. Never starts or prepares on selection. |
| `C-ITEM-STATE` | `W-ITEM-ACTIONS` → persisted item state → shared snapshot indicators; detail history is a separately retained read. | Unread, acknowledge New or record action; item ID. | Submits item state changes, refreshes snapshot indicators and rereads selected detail/history on success. Independent read/New/action indicators and unrelated drafts remain unchanged. A deferred provider refresh leaves the displayed saved information unread until a successful retry. |
| `C-PRIORITY` | `W-PRIORITY.override` → local priority state and displayed item | Picker change; automatic clears the override. | Saves immediately, disables during submission and restores the saved value with inline error on failure. |
| `C-LINK` | `W-RELATIONSHIPS` → selected item's relationships/context | Add submits `source` and `target`; Remove submits `source`, `target` and `remove: true`. | Submits local Add/Remove, then refreshes selected relationships without clearing unrelated drafts. A late earlier response cannot replace later selected detail. |
| `C-LAUNCH-OPEN` | `W-ITEM-ACTIONS.launch` → `D-LAUNCH` | Selected item entry. | New run under Item details prepares an optional proposal and opens its durable review. No workspace/process starts until Start. |
| `C-LAUNCH-PROFILE` | `W-LAUNCH-EDITOR.profile` → its editable fields | Select a profile. | Replaces prompt/model/effort/skill drafts; keeps the current base draft. |
| `C-PREPARE` | Default launch or editor Review → prepared review | Default launch, or explicit Review after customization. | Default launch prepares immediately; optional edits reprepare explicitly. Preparation errors keep retry and Edit available without Start. PR review preparation rejects the signed-in account’s own PR and missing author/identity without creating a proposal. |
| `C-LAUNCH-EDIT` | `W-LAUNCH-REVIEW.edit` → `P-LAUNCH-EDITOR` | Edit launch click. | Returns to the editor. The preceding prepared record is retained, not modified/deleted. |
| `C-START` | `W-LAUNCH-REVIEW.start` → `PAGE-AGENTS`/`P-RUN-DETAIL` | Prepared run ID. | Explicit Start only. Error/lost response reads the same run; accepted work opens, still-prepared work permits retry, unknown status disables Start/Edit and offers Check start status. PR review Start rechecks the current author and identity and rejects self review without queuing work; the retained proposal remains available. |
| `C-SELECT-RUN` | Retained run or draft row → conversation or review | Retained conversation or draft row inside the owning item’s Work history. | Opens the selected retained conversation within the owning item, preserving the originating work queue Back context. Prepared rows reopen their existing immutable review without preparing or starting again. |
| `C-RUN-ACTION` | `W-RUN-CONTROLS` → selected run → Agents widgets | Explicit Interrupt, question-recovery Take over session, or Cleanup for the selected retained run. | Takeover cancels further form repair and opens the same session console. Cleanup preserves the existing clean-worktree boundary. Source closure or merge permanently terminates the session while retaining its history and workspace. |
| `C-FOLLOW-UP` | `W-MESSAGE` → selected run → `W-TRANSCRIPT` | Message text and displayed current-turn context. | Send with current-turn identity while active. Acknowledge durable local queue acceptance before contacting the agent; background delivery restores the same retained session when needed. Saved source context is explicitly unverified; task-required GitHub reads belong to the agent. Accepted sends clear unchanged submitted drafts; rejected acceptance preserves them. Delivery uncertainty remains visible and is never automatically replayed. Retained message/text questions leave actionable attention individually after explicit same-run decision acknowledgement; partial and unverified questions remain available. Other-run publication does not retire them. |
| `C-RUN-ATTENTION` | `W-ATTENTION-NOTICE[requestId]` → `D-ATTENTION` | Notice click; request ID belonging to the selected run. | Opens that request without answering. |
| `C-GLOBAL-ATTENTION` | Messages request → owning run and attention dialog | Select one pending request. | Selects that exact owning run and request; identifies item/profile and returns to that conversation after answering. Retained message/text questions leave actionable attention individually after explicit same-run decision acknowledgement; partial and unverified questions remain available. Other-run publication does not retire them. |
| `C-ATTENTION-CHAT` | Attention dialog → owning conversation | Current attention's owning run ID. | Closes attention and opens the owning conversation without submitting an answer. |
| `C-ANSWER` | `W-QUESTIONS` / `W-APPROVAL` / `W-ELICITATION` → request/run state | Response matched to current request ID. | Submits to the matching request. Success selects the owning run, refreshes and closes; a refresh failure is reported in the shell without replaying the answer. Answer failures stay in attention. Original approval payloads are preserved behind readable labels. Retained message/text questions leave actionable attention individually after explicit same-run decision acknowledgement; partial and unverified questions remain available. Other-run publication does not retire them. |
| `C-DISMISS` | Launch/attention close control → underlying page | Close click or Escape inside the active dialog. | Dismisses only that dialog and restores its connected opener when permitted. No attention answer or interrupt is sent. |
| `C-REPOSITORY-SAVE` | `W-REPOSITORY-SETTINGS.save` → repository summary / sync inputs | Repository ID, checkout and default base. | Shows saving/success/error within the independent settings view. Successful saves remain there; other open task views receive refreshed shared data without navigation or draft reset. |
| `C-DISCOVER-SKILLS` | `W-REPOSITORY-SETTINGS.discover-skills` → profile and launch skill choices | Explicit skill discovery directory, initially the filesystem root. | Loads/discovers skills and refreshes shared choices without saving the repository first. |
| `C-POLICY-SAVE` | `W-POLICY-SETTINGS` → config / priority assessment inputs | Label/priority rows, assessment prompt and notification setting; disabled Jira values are not editable here. | Submits configuration; later background assessment can update priority displays. Does not create a host subscription. |
| `C-PROFILE-SAVE` | `W-PROFILE-SETTINGS` → future `W-LAUNCH-EDITOR` | Saved profile draft. | Refreshes available/default launch settings; existing prepared snapshots stay unchanged. |
| `C-SKILL-SAVE` | `W-SKILL-SETTINGS` → profile/launch skill choices | Managed skill name/instructions. | Refreshes shared skills; immutable prepared skill snapshots stay unchanged. |
| `C-DIAGNOSE` | `W-DIAGNOSTICS` → its result display | Check connection click. | Shows local Checking feedback and then health results or an exact local error. No repair or page transition. |
| `C-STATE-REFRESH` | Shared updated state → Inbox/Agents/Settings summaries, shell counts | Successful polling or action refresh. | Refreshes shared snapshot after successful or failed actions, preserving action errors. Successful selected item mutations also trigger a separate guarded detail reread. Background work/attention updates do not save local drafts. Only an explicit allowlist of read operations retries transient transport errors. The exact host-wrapped McpServerError connection failure is recognized as transport; other tool/business errors and all mutations are never automatically replayed. Poll recovery clears only polling feedback, not action failures. Dashboard refreshes use item summaries; selected-item reads fetch full activity and raw checks separately. Known read calls carry a unique freshness marker so host reuse of an earlier response cannot freeze an empty journal page or saved snapshot. This marker does not change server state, source authority or mutation replay rules. Saved item detail and review reads return immediately with observation state. Pending observations reread through the bridge, preserving saved content. Freshness markers never force provider requests or bypass monitoring pauses, budgets and cooldowns. Provider query cache hits preserve original observation times. Post-turn publication verification uses live data through the same cache boundary; live failure retains prior saved observations with an error. |
| `C-TRANSCRIPT` | History pages → `W-TRANSCRIPT` | Selected run and current history cursor; optional reset to beginning. | Refreshes display only. Resetting history does not resume/replay agent work. |
| `C-PACKAGE` | Package page links → artifacts / external host setup | Download or follow documented setup instructions. | External package downloads and OAuth connection guidance. Only a valid observed registration enables the primary browser package. Portable downloads stay advanced; no connection is fabricated. |
| `C-CONSENT` | External host → `PAGE-OWNER-CONSENT` → registered host callback | Valid private request; Allow or Deny. | External. Consumes request; authentication then occurs in host, not by navigating into a local inbox URL. |
| `C-EXTERNAL-AUTH` | `W-ELICITATION.open-authorization` → external authorization page | Request-supplied URL opened through host. | External. Continue reports the user's completion decision; it does not independently prove external success. |
| `C-TUNNEL-KEY` | `W-LOCAL-TUNNEL-KEY` → alternate local transport session | Single-use private setup input. | Submits and removes form on success; does not navigate into Settings or activate Jira. |
| `C-BROWSER-LAB` | `W-BROWSER-LAB` controls → isolated browser → screenshot/location | Explicit lab navigation/click/key/scroll operations. | Developer utility feedback loop; never an Inbox/Agents page transition. |
| `C-DISMISS-QUESTION` | No reply needed dismisses only local textual attention and closes the form without sending a task turn or answering an approval. | No reply needed dismisses only local textual attention and closes the form without sending a task turn or answering an approval. | No reply needed dismisses only local textual attention and closes the form without sending a task turn or answering an approval. |
| `C-REPORT-SELECT` | `W-REPORT-LIST.select` → `W-REPORT-DETAIL` | Report row selection | Reads selected report without starting an agent. Selection and mutation ordering discard superseded details; journal updates queue a refresh behind a pending read so details can progress during ongoing changes. |
| `C-REPORT-EVIDENCE` | `W-REPORT-DETAIL.add-evidence` → `W-REPORT-DETAIL`, `W-REPORT-LIST` | Add evidence; selected report ID, draft text and retained idempotency key. | Append idempotent host observations and queue reports waiting for information. After an uncertain response, an unchanged draft can be retried without duplicate additions; editing the retained draft permits another observation. |
| `C-REPORT-RECOVER` | `W-REPORT-DETAIL.resume` → `W-REPORT-DETAIL`, `W-REPORT-LIST` | Resume or reconcile report; selected report ID, recovery note and effects acknowledgement. | Validate recovery prerequisites and acknowledgement before resuming retained work. |
| `C-REPORT-REFRESH` | `W-REPORT-LIST.refresh` → `W-REPORT-LIST`, `W-REPORT-DETAIL` | Refresh issues and releases | Read external issue and verified delivery evidence, retaining successful state on failure; never starts an agent. |
| `C-REPORT-QUERY` | `W-REPORT-LIST.search/processing-filter/component-filter/version-filter` → `W-REPORT-LIST` | Search or processing/component/version filter changes; current query values. | Loads the matching report subset and resets the loaded range to its first page. Existing selected detail remains independently selected. |
| `C-REPORT-MORE` | `W-REPORT-LIST.load-more` → `W-REPORT-LIST` | Load more reports; current query, next cursor and loaded range. | Extends the displayed range with bounded pages and unique report IDs. Journal updates retain pending pagination and refresh the loaded range. Background refresh leaves pagination available; an already pending page request suppresses duplicate clicks. |
| `C-REPORT-OPEN-LINK` | `W-REPORT-DETAIL.open-issue/open-fix/open-release` → external GitHub issue, fix PR/commit or release page; owned endpoint `W-REPORT-DETAIL` | Open a retained issue, fix or release URL through the host. | Sends the stored URL to the host open-link boundary; a fix without a PR uses its repository/commit URL. Failures use the shared report error alert. Opening a link does not verify issue, fix or release state. |
| `C-BACK` | Conversation Back → originating work or Messages tab | Back action on the record page. | Restores list query, scroll and row focus when the row exists; missing rows leave a usable focus fallback. Ordinary drafts remain keyed to their records. |
| `C-ITEM-SOURCE` | Owned issue, PR and check links open their original GitHub or CI-provider destination through the host browser. | Activate an item identifier, linked PR identifier or individual CI check link. Activate a posted-review receipt or inline-comment link. | Opens through host openLink. It does not select/read/acknowledge or start work. Denied or failed opens remain visible in the current list. |
| `C-OPEN-SETTINGS` | Host open_settings invocation → independent settings document | The settings entry tool advertises its own content-specific resource and output template. | Opens configuration in its own view. The inbox entry and legacy resource remain inbox-only. Both surfaces reuse authenticated bridged tools without owner credentials or direct iframe localhost access. |
| `C-START-CHAT` | First-message Start → retained run conversation | Explicit Start with the visible message and authority. | Prepares then starts once; verifies the same run after an uncertain response. Preserves per-item draft and recovery state. |
| `C-SESSION-CONSOLE` | Conversation presentation to the same session console. | View session console, or Open session console from a textual fallback. | Switches presentation without launching or resuming another process. |
| `C-PIPELINE-SELECT` | Selecting an agent pipeline updates its authority and automatic prompt while retaining an edited message and invalidating an old prepared proposal. | Selecting an agent pipeline updates its authority and automatic prompt while retaining an edited message and invalidating an old prepared proposal. | Selecting an agent pipeline updates its authority and automatic prompt while retaining an edited message and invalidating an old prepared proposal. |
| `C-PLUGIN-MAINTENANCE` | Submit explicit plugin management to the pinned CLI through owner MCP tools and poll durable results; no automatic replay. | Explicit owner configuration or management choice; opted-in successful repository polls. | Submit explicit plugin management to the pinned CLI through owner MCP tools and poll durable results; no automatic replay. |
| `C-REVIEW-POLICY` | Resolve system policy and repository override against fresh current user/team review requests; start eligible claimed reviews once under the configured authority. | Explicit owner configuration or management choice; opted-in successful repository polls. | Resolve system policy and repository override against fresh current user/team review requests; start eligible claimed reviews once under the configured authority. |
| `C-REPOSITORY-REVIEW-PROMPT` | Append owner repository instructions to the generic review prompt during immutable launch preparation. | Explicit owner configuration or management choice; opted-in successful repository polls. | Append owner repository instructions to the generic review prompt during immutable launch preparation. |
| `C-GITHUB-USAGE` | Owner usage settings → persisted request limits and cached accounting | Save system defaults or read cached Settings. | Validates and persists bounded request budget/reserve/poll interval; omitted fields from older clients retain their saved values. Budget-only changes preserve assessment policy identity. |
<!-- /specs:connections-0 -->

## Navigation overview

<!-- specs:connections -->
```mermaid
flowchart LR
    N2["W-NAVIGATION"]
    N63["PAGE-INBOX"]
    N64["PAGE-AGENTS"]
    N105["PAGE-REPORTS"]
    N122["PAGE-MY-PRS"]
    N123["PAGE-REVIEW-PRS"]
    N130["PAGE-WORKING"]
    N5["W-INBOX-QUERY"]
    N42["P-INBOX-LIST"]
    N7["W-ITEM-ROW"]
    N117["PAGE-AGENT"]
    N10["W-ITEM-ACTIONS"]
    N43["P-ITEM-DETAIL"]
    N11["W-PRIORITY"]
    N12["W-RELATIONSHIPS"]
    N13["W-TICKET-CONTEXT"]
    N66["D-LAUNCH"]
    N23["W-LAUNCH-EDITOR"]
    N53["P-LAUNCH-REVIEW"]
    N24["W-LAUNCH-REVIEW"]
    N52["P-LAUNCH-EDITOR"]
    N16["W-RUN-ROW"]
    N18["W-RUN-CONTROLS"]
    N132["W-QUESTION-RECOVERY"]
    N45["P-RUN-DETAIL"]
    N44["P-RUN-LIST"]
    N21["W-MESSAGE"]
    N20["W-TRANSCRIPT"]
    N19["W-ATTENTION-NOTICE"]
    N67["D-ATTENTION"]
    N126["W-WAITING-ROW"]
    N22["W-DIALOG-FRAME"]
    N25["W-QUESTIONS"]
    N26["W-APPROVAL"]
    N27["W-ELICITATION"]
    N28["W-REPOSITORY-SETTINGS"]
    N6["W-SYNC-HEALTH"]
    N30["W-PROFILE-SETTINGS"]
    N29["W-POLICY-SETTINGS"]
    N31["W-SKILL-SETTINGS"]
    N32["W-DIAGNOSTICS"]
    N0["S-FACTORY"]
    N65["PAGE-SETTINGS"]
    N142["W-GITHUB-USAGE"]
    N33["W-PACKAGE-OVERVIEW"]
    N34["W-CONNECTION-GUIDE"]
    N68["PAGE-PACKAGE"]
    N35["W-OWNER-CONSENT"]
    N69["PAGE-OWNER-CONSENT"]
    N36["W-LOCAL-TUNNEL-KEY"]
    N70["PAGE-LOCAL-TUNNEL-SETUP"]
    N37["W-BROWSER-LAB"]
    N71["PAGE-BROWSER-LAB"]
    N124["P-WAITING-QUEUE"]
    N108["W-REPORT-LIST"]
    N109["W-REPORT-DETAIL"]
    N14["W-PR-EVIDENCE"]
    N120["S-SETTINGS"]
    N127["W-NEW-CHAT"]
    N131["W-SESSION-CONSOLE"]
    N135["W-PLUGIN-ADMIN"]
    N136["W-SYSTEM-DEFAULTS"]
    N129["W-WORK-STATUS"]
    N2 -->|"C-NAVIGATE"| N63
    N2 -->|"C-NAVIGATE"| N64
    N2 -->|"C-NAVIGATE"| N105
    N2 -->|"C-NAVIGATE"| N122
    N2 -->|"C-NAVIGATE"| N123
    N2 -->|"C-NAVIGATE"| N130
    N5 -->|"C-FILTER"| N42
    N7 -->|"C-SELECT-ITEM"| N117
    N10 -->|"C-ITEM-STATE"| N42
    N10 -->|"C-ITEM-STATE"| N43
    N11 -->|"C-PRIORITY"| N11
    N11 -->|"C-PRIORITY"| N7
    N12 -->|"C-LINK"| N12
    N12 -->|"C-LINK"| N13
    N10 -->|"C-LAUNCH-OPEN"| N66
    N23 -->|"C-LAUNCH-PROFILE"| N23
    N23 -->|"C-PREPARE"| N53
    N10 -->|"C-PREPARE"| N53
    N24 -->|"C-LAUNCH-EDIT"| N52
    N24 -->|"C-START"| N117
    N16 -->|"C-SELECT-RUN"| N117
    N16 -->|"C-SELECT-RUN"| N66
    N18 -->|"C-RUN-ACTION"| N45
    N18 -->|"C-RUN-ACTION"| N44
    N132 -->|"C-RUN-ACTION"| N45
    N132 -->|"C-RUN-ACTION"| N44
    N21 -->|"C-FOLLOW-UP"| N20
    N19 -->|"C-RUN-ATTENTION"| N67
    N126 -->|"C-GLOBAL-ATTENTION"| N117
    N126 -->|"C-GLOBAL-ATTENTION"| N67
    N22 -->|"C-ATTENTION-CHAT"| N117
    N25 -->|"C-ANSWER"| N67
    N25 -->|"C-ANSWER"| N45
    N26 -->|"C-ANSWER"| N67
    N26 -->|"C-ANSWER"| N45
    N27 -->|"C-ANSWER"| N67
    N27 -->|"C-ANSWER"| N45
    N22 -->|"C-DISMISS"| N66
    N22 -->|"C-DISMISS"| N67
    N28 -->|"C-REPOSITORY-SAVE"| N28
    N28 -->|"C-REPOSITORY-SAVE"| N6
    N28 -->|"C-DISCOVER-SKILLS"| N30
    N28 -->|"C-DISCOVER-SKILLS"| N23
    N29 -->|"C-POLICY-SAVE"| N11
    N30 -->|"C-PROFILE-SAVE"| N23
    N31 -->|"C-SKILL-SAVE"| N30
    N31 -->|"C-SKILL-SAVE"| N23
    N32 -->|"C-DIAGNOSE"| N32
    N0 -->|"C-STATE-REFRESH"| N63
    N0 -->|"C-STATE-REFRESH"| N122
    N0 -->|"C-STATE-REFRESH"| N123
    N0 -->|"C-STATE-REFRESH"| N64
    N0 -->|"C-STATE-REFRESH"| N117
    N0 -->|"C-STATE-REFRESH"| N65
    N0 -->|"C-STATE-REFRESH"| N2
    N0 -->|"C-STATE-REFRESH"| N130
    N0 -->|"C-STATE-REFRESH"| N142
    N20 -->|"C-TRANSCRIPT"| N20
    N33 -->|"C-PACKAGE"| N68
    N34 -->|"C-PACKAGE"| N68
    N35 -->|"C-CONSENT"| N69
    N27 -->|"C-EXTERNAL-AUTH"| N27
    N36 -->|"C-TUNNEL-KEY"| N70
    N37 -->|"C-BROWSER-LAB"| N71
    N25 -->|"C-DISMISS-QUESTION"| N117
    N25 -->|"C-DISMISS-QUESTION"| N19
    N25 -->|"C-DISMISS-QUESTION"| N124
    N108 -->|"C-REPORT-SELECT"| N109
    N109 -->|"C-REPORT-EVIDENCE"| N109
    N109 -->|"C-REPORT-EVIDENCE"| N108
    N109 -->|"C-REPORT-RECOVER"| N109
    N109 -->|"C-REPORT-RECOVER"| N108
    N108 -->|"C-REPORT-REFRESH"| N108
    N108 -->|"C-REPORT-REFRESH"| N109
    N108 -->|"C-REPORT-QUERY"| N108
    N108 -->|"C-REPORT-QUERY"| N108
    N108 -->|"C-REPORT-QUERY"| N108
    N108 -->|"C-REPORT-QUERY"| N108
    N108 -->|"C-REPORT-MORE"| N108
    N109 -->|"C-REPORT-OPEN-LINK"| N109
    N109 -->|"C-REPORT-OPEN-LINK"| N109
    N109 -->|"C-REPORT-OPEN-LINK"| N109
    N2 -->|"C-BACK"| N63
    N2 -->|"C-BACK"| N122
    N2 -->|"C-BACK"| N123
    N2 -->|"C-BACK"| N64
    N2 -->|"C-BACK"| N130
    N7 -->|"C-ITEM-SOURCE"| N7
    N7 -->|"C-ITEM-SOURCE"| N14
    N7 -->|"C-ITEM-SOURCE"| N14
    N7 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N7
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N7
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N7
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N14 -->|"C-ITEM-SOURCE"| N14
    N120 -->|"C-OPEN-SETTINGS"| N65
    N127 -->|"C-START-CHAT"| N117
    N127 -->|"C-START-CHAT"| N117
    N20 -->|"C-SESSION-CONSOLE"| N131
    N22 -->|"C-SESSION-CONSOLE"| N131
    N127 -->|"C-PIPELINE-SELECT"| N127
    N127 -->|"C-PIPELINE-SELECT"| N127
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N135 -->|"C-PLUGIN-MAINTENANCE"| N135
    N28 -->|"C-REVIEW-POLICY"| N129
    N28 -->|"C-REVIEW-POLICY"| N28
    N136 -->|"C-REVIEW-POLICY"| N129
    N136 -->|"C-REVIEW-POLICY"| N28
    N28 -->|"C-REPOSITORY-REVIEW-PROMPT"| N24
    N142 -->|"C-GITHUB-USAGE"| N142
    N142 -->|"C-GITHUB-USAGE"| N28
    N142 -->|"C-GITHUB-USAGE"| N142
    N142 -->|"C-GITHUB-USAGE"| N28
    N142 -->|"C-GITHUB-USAGE"| N142
    N142 -->|"C-GITHUB-USAGE"| N28
    N136 -->|"C-GITHUB-USAGE"| N142
    N136 -->|"C-GITHUB-USAGE"| N28
```
<!-- /specs:connections -->

The Messages request connection also selects its owning conversation. Its
request selection and the dialog’s Open agent chat action both preserve owning-run identity. The latter defers the answer.

## Launch phase boundaries

Review submits preparation and replaces editing with the prepared review on
success. Preparation failure retains editing; start failure retains review.
Edit returns to editing. Close dismisses either phase while retaining prepared
records. Successful start shows the run in its conversation. Close remains available
during in-flight preparation/start and does not cancel the request.

## Update and lifecycle boundaries

- Updates travel through the host connection. The embedded iframe has no direct
  local-service request path and does not receive owner credentials.
- Visible shared-state/history polling normally repeats once per second, with
  hidden-view delay and failure backoff. This is implementation timing documented
  for reconciling the current UI, not proof of a real-host delivery deadline.
- Loading content and loading a page are different transitions. An item read can
  make a row cease to match the unread filter while leaving its detail selected.
- Request resolution removes pending badges/notices on the next relevant refresh.
  A request that disappears cannot be answered successfully using stale identity.
- Closing a view or changing tabs is a view transition. Agent completion,
  interruption, failure and recovery are application transitions with separate
  application lifecycle effects.
