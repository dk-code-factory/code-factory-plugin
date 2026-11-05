# Native session wrapper architecture

The production daemon combines a retained triage/provider layer with the small
`internal/factory` coordinator. It uses Codex app-server directly. Native agents
and installed skills own execution policy and review/implementation/publication
work; the daemon routes authorized work and retains human interaction.

## Durable records and source observations

`factory-v1.db` is fresh application state. It stores canonical sessions,
normalized events and facet observations, configured rules, queued actions, delivery
batches, attention requests and configuration proposals. There is no import from
the retired application's database. The existing encrypted vault key and external
connection files remain outside Git and are reused for their existing purposes.
Native Codex configuration, authentication and thread history remain native.

GitHub's gateway route forwards exact webhook bytes to the daemon. The daemon
verifies SHA256 HMAC signatures and records delivery identity, digest and payload
before acknowledgement. Durable pending deliveries are processed with retry
state. Redeliveries with the same identity and bytes do not create work; identity
collisions fail. Current PR facets deduplicate webhook/poll overlap.

The existing GitHub provider supplies authenticated, budgeted, conditionally cached
HTTP/GraphQL reads. Initial synchronization fetches detailed activity before
establishing a baseline. Polling detects observable changes, with explicit generic
observed actions where source history is unavailable. It never invents missed
historical events. CI deliveries resolve every associated current-head PR. Review
threads are paginated and retained only after complete observations.

## Sessions, rules and delivery

The work item ID is the canonical session key. Rules match repositories, event
types, actions and optional labels, author and draft filters. All matches contribute
actions in stored order. Repository defaults select native profile/agent settings;
an explicit first-message or confirmed per-item override can select another agent.
A repository default edit does not silently rebind existing item conversations.

Each matching event atomically appends its actions and resets the quiet deadline.
The default is 120 seconds, with per-repository overrides. Automatic batches wait
for the active turn to finish and stop behind pending attention, a reported blocker
or an explicit interruption hold. Human chat, answers and explicit run/resume bypass
the timer; ordinary reads do not. Unblocked active turns receive `turn/steer`.

Before a native send the service persists a claimed batch and removes the claimed
queue entries in the same transaction. Thread creation also has a durable pending
marker. Lost responses remain uncertain. Resume searches retained native items for
the batch marker before determining whether it was accepted. Missing evidence does
not imply failure; retry/discard requires an explicit human decision. Restart
retains queued actions and expires lost connection-bound requests.

The session keeps its chosen agent, native thread and Git worktree. `done`, closure
and merge retain the conversation. Cleanup is explicit and refuses dirty worktrees.
Unrelated source checkouts and native user settings are preserved.

## Native protocol and attention

App-server handles thread creation/resumption, turns, steering, streaming and native
requests. A schema probe checks the experimental dynamic-tool request and response
surface and versioned configuration writes before native execution. Native TOML
profiles and agent definitions resolve through actual configuration layers; the
binary parses agent TOML without a separate runtime interpreter.

The session-bound `code_factory_report_status` tool reports working, done or blocked.
Blocked requires concrete human questions/actions. Its RPC response stays suspended
until the human answers. Execution and reported status remain separate: turn
completion alone leaves work idle. The bundled status skill explains this contract.

Native user-input, command/file approvals, permissions and elicitation requests
share the questions ledger. Options, blocking semantics, turn identity, connection
epoch and native RPC ID are retained. Answers are durably claimed before response.
Disconnected identities become stale; answered requests are never replayed. There
is no AI question classifier, repair turn or built-in review orchestration.

Secret answers are encrypted for persistent redaction before response delivery.
Managed secrets are removed from split streaming and completed output. Public
callback events contain metadata only. The UI reads paginated saved transcripts
through authenticated MCP rather than contacting localhost.

## Configuration and priorities

One retained read/propose configuration session inspects application settings,
native layers, profiles, agent definitions and installed skills. It can create
concrete diffs but cannot confirm them. Explicit user confirmation is a separate
app-only operation. Application proposals use a settings digest; native edits use
actual layer identity/version and key-scoped `config/batchWrite`. Stale proposals
fail. Confirmed changes apply only at safe turn boundaries, close idle clients and
leave active work uninterrupted. An uncertain native write is never blindly replayed.

Manual priorities override mapped labels, which override AI assessment. Selected
priority agents/skills run in reusable read-only per-repository bookkeeping
sessions. A local record tool validates exact item revision, linked context and
policy before saving assessment. These sessions have no mutating MCP/apps and
cannot publish labels. Local relationships, discovery/read state and issue tracking
remain in the triage layer.

## Verification boundary

Fixture tests cover delivery validation/deduplication, baselining, quiet clocks,
concurrent creation, retained reuse, native requests and status, stale/uncertain
recovery, configuration versions, priorities and dirty cleanup. Browser fixtures
cover session chat, native questions/approvals, configuration confirmation,
relationships, queues and event inspection. The assembled fixture uses a real MCP
daemon and fake native subprocess. The production embedded UI keeps its isolated
100 ms launch gate. Real-provider and installed-host acceptance remain opt-in.

Protocol references: [Codex app-server](https://learn.chatgpt.com/docs/app-server),
[native profiles](https://learn.chatgpt.com/docs/config-file/config-advanced#profiles),
[native agents](https://learn.chatgpt.com/docs/agent-configuration/subagents#custom-agents).
