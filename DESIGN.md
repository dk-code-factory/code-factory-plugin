# Design

The daemon is the single owner of mutable local state and agent processes. Transports invoke application methods; domain types contain no Work/Claude-specific behavior. Linux packaging, provider HTTP, app-server JSON-lines, SQLite, MCP and webhook networking are separate adapters.

## State and events

SQLite runs in WAL mode with schema version 1. `entities(kind,id,data)` stores typed JSON; `events(seq,kind,entity,data,at)` is an append-only journal. Every application state transition with a corresponding observable event is committed in one transaction. Events have monotonically increasing cursors. Failed syncs never advance import checkpoints. Snapshots obtain their cursor before loading records so a concurrent change can be delivered again but cannot be skipped.

Inbox revisions cover full provider content/activity, head and check state. `readRevision` belongs to plugin activity and is independent of New and action history. A stale displayed revision can be marked read without consuming a newer revision. Local overrides never write provider labels. Inference is valid only for its item revision, linked-context fingerprint and policy version.

Credential values are AES-GCM encrypted with per-record authenticated names. The key and owner token are mode 0600 outside model-visible state. Subscription secrets use the vault. Credential writes have no value-bearing journal entry. Secret answers are never journaled; redaction values alone are encrypted in the vault per run so recovery can still redact echoes. Known service and secret-answer values are redacted from incoming Codex messages before journaling.

## Runs

`prepared → queued → starting → running` is the normal launch sequence. Blocking attention sets `waiting`; nonblocking attention leaves the run running. Completion sets `idle`, permitting another turn. Cancellation sets `interrupted`; errors set `failed`. Process loss sets interrupted and invalidates pending requests. Startup recovery interrupts every nonterminal live state and stales every outstanding request. Prepared snapshots remain startable. Cleanup sets `cleaned` only after Git removes a stopped clean worktree.

A prepared launch is immutable. The Start transaction requires prepared state; a repeated Start cannot create another process. Explicit Resume requires interrupted/failed state. Old process callbacks are retired before replacement. Each live run has a mutex serializing send, answer, interrupt and process-state handling; the client reader routes RPC responses separately from the notification dispatcher to avoid response/notification deadlocks.

Question IDs are service-generated and map to the original process request ID. Answers require a matching live request and permitted choices/schema. The service durably claims a request before sending its response. A process failure in that window may lose delivery; the response is never replayed. Original worktrees and history remain available for explicit recovery. Notification of a resolved request invalidates it across all views.

The app-server connection/process belongs to the daemon, not to an MCP request or UI frame. Every run receives its own worktree. The client uses the installed Codex app-server protocol with a stable 0.161.0 minimum, checks the version before launching, and lets Codex enforce managed requirements. Managed upgrades use the owner-selected dkropachev/codex fork and require a compatibility probe before provider activation. Background inference uses a separate ephemeral read-only thread, disables external tools/apps through config overrides, rejects requests, and yields to manual work.

Publishing settings become developer instructions for the agent. The service cannot independently police every external operation performed by arbitrary tools; sandbox/approval policy and credentials define the execution boundary. A future host adapter must not reinterpret a profile to weaken it.

## Work projections and question recovery

Queue membership is a server projection, shared by snapshots and filtered inbox calls. Started runs track an item independently of runtime completion. A closed/merged PR leaves Working; a closed issue or merged explicitly associated implementation PR completes issue work. Reopening restores eligible work. Manual associations and GitHub development links carry implementation intent; source references retain evidence without acquiring that intent. Item activity aggregates all sessions so an older blocker remains visible.

PR lifecycle, review and CI are independent typed values. Read-only GraphQL metadata provides current-head rollups and development links; latest opinionated reviews supply approval information when a repository has no authoritative review decision. Referenced PRs are fetched individually without configuring their repositories. Poll freshness and synchronization errors do not change content revisions. Failed metadata refresh preserves same-head results and development links; a changed head cannot inherit old CI success.

Questions have RPC, structured-message or textual origins. Only RPC origins carry live process request IDs. Message/text answers durably claim the attention before steering or restoring the original session; uncertain delivery is not automatically replayed. Synthetic answers retain encrypted redaction values before delivery. Lost native questions can become textual continuations, while approvals must regain a native authorization request.

A recovery episode is keyed by run and source turn, with durable stage and at most two claimed repair attempts. Its helper uses bounded redacted context, an ephemeral read-only thread, disabled external MCP/apps and a supported low reasoning effort. Repair turns use question-only Plan mode in the original thread and retain its sandbox, approval and publishing authority. Successful answers finish that question-only turn before an ordinary continuation explicitly restores the profile mode. User takeover cancels further repair; process loss retains the episode without replay. The console displays the same redacted journal and uses existing message, answer and interrupt operations. Journal metadata identifies daemon-generated repair and continuation input by its turn and exact text, preserving the role of real answers in that same turn.

## Host boundary

HTTP is loopback-only and bearer-authenticated. The tunnel's stdio proxy reads the token locally and forwards MCP requests and required version-2 HTTP headers. The tunnel and associated Work workspace must be owner-only, since all tunnel callers share the owner's local service identity. Shared workspace deployment requires an additional authenticated principal boundary.

The React UI uses only the standard MCP Apps postMessage bridge. It does not fetch localhost or receive service credentials. App-only polling tools carry incremental cursors; opening detail sends the displayed revision explicitly. Output and diffs are bounded journal chunks, loaded in pages. UI closure has no process lifecycle effect.

Events subscriptions have deterministic identities from owner, callback URL, name and canonical arguments. A signed random challenge must be echoed before activation. HTTPS callback dialing resolves addresses at connection time, rejects non-public destinations, preserves hostname TLS verification and blocks redirects. Each event retains its ID across retries; fresh timestamps/signatures accompany each attempt. Cursor progress and retry state persist. Expiry and unsubscribe stop delivery. Protocol cursors are null: this adapter does not promise webhook replay. Local journal cursors are independent.

Real Work/tunnel acceptance is a release gate. Fixture tests establish local behavior; they cannot establish workspace permissions, MCP Apps availability, callback receipt by Work or host notification timing. Unsupported native user-verification requests remain visible with an explicit host requirement and decline/cancel choices.


## Installation and pilot evidence

A checksummed Linux x86-64 archive contains the static daemon, embedded UI, plugin,
dependency manifest and operator guide. Versioned releases live under the user
library with a stable launcher. Systemd units persist the selected data path,
listener and executable search path. The tunnel requires the daemon. No lingering
policy or provider authentication is changed. Codex Plan mode is an explicit profile
choice that supplies the supported question path.

Upgrade stops both services and obtains the daemon lock before a SQLite snapshot,
then changes the active release and requires authenticated version health. Failed
upgrades restore compatible state. A pending-operation record supports rollback
following installer loss; pre-rollback snapshots retain newer state. The installed
plugin app mapping and all application data survive uninstall and reinstall.

CLI and MCP diagnostics share typed verified/failed/unverified checks. Sync and
callback receipt reflect observed state. Dated operator evidence is bound to the
installed version and cannot be inferred from fixture results. Browser reachability
is checked before showing a local credential input; submissions are single-use and
expiry-bound, and server shutdown drains the response. Ownership verification is
an explicit operator checkpoint, not a claim of automatic Platform permission audit.
