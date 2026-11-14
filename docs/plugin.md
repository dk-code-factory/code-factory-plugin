# Code Factory plugin

The complete private plugin is `dist/code-factory-plugin-0.4.0.zip`. Its root
contains a portable `plugin.json`, portable `mcp.json`, Codex compatibility
manifests, SVG branding, onboarding, daily-work, administration, issue-drafting and status-reporting skills.
It bundles the Tailscale HTTPS endpoint and discovers OAuth from the server's
protected-resource metadata. No client secret or local owner token ships.

When bundled plugin content changes, advance the version in both `plugin.json`
and `.codex-plugin/plugin.json` before replacing the browser package. Work's
**Upload new version** action rejects an archive that reuses the installed version.
Version 0.4.0 packages API v2 with the daemon and all five current skills;
earlier upload observations do not establish acceptance of this version.

The installed source lives at `~/.local/lib/code-factory/plugin`. Running
`code-factory install-plugin` adds it to `~/.agents/plugins/marketplace.json`.
Existing marketplace identity and unrelated entries are retained. The plugin is
available as `code-factory@code-factory-personal` when setup creates a new catalog.
It is discoverable, rather than automatically enabled in unrelated Codex sessions.

On a ChatGPT desktop host that reads this user's filesystem, restart the app,
open the **Code Factory personal** plugin source, install **Code Factory**, and
finish OAuth. Start a new Work conversation and ask **Open Code Factory**.
The browser authorizing the connection must be on the owner's Tailscale network.

Browser-only Work cannot read this machine's personal marketplace. OpenAI's
current private/local MCP packaging flow may require registering the remote MCP
connection first, then linking the observed `plugin_asdk_app_...` ID with
`code-factory link-plugin --app-id OBSERVED`. The package cannot create a cloud
registration itself. No cloud app ID is fabricated, and private Work installation
and authorization remain unverified until observed. The ZIP is not a public plugin
directory submission; public distribution remains outside this personal pilot.

Build the ZIP with `make plugin`. Install build-time validation dependencies with
`python3 -m pip install -r scripts/requirements-dev.txt` if they are missing.
`python3 scripts/test_plugin_host.py` verifies marketplace discovery and package
installation using the real Codex plugin host in an isolated `CODEX_HOME`; it does
not sign in, invoke a model or change the user's enabled plugins.
The canonical portable schemas are vendored for offline manifest validation.

The full Linux release also contains the plugin; `make package` emits both the
Linux archive and plugin ZIP. `code-factory configure-tailscale` updates both MCP
configuration files to the installed host's current HTTPS endpoint. Registered
Work app mappings remain separate from portable ZIPs, which never contain local
credentials or host application state.

Source contract: [OpenAI plugin packaging](https://developers.openai.com/plugins/build/plugins).

## Hosted package

The running Tailscale gateway hosts the complete package and a browser installation
page at its HTTPS base URL. Under the default `/code-factory` base,
`/code-factory/plugin.zip` downloads the plugin with all five skills,
branding and MCP connection; the adjacent `plugin.zip.sha256` and `plugin-release.json` describe
those exact bytes. Public `plugin.json` and `mcp.json` provide package metadata.
The archive is embedded in the versioned binary, so services do not depend on a
source checkout or serve the private application directory. On startup its endpoint
is bound to the configured Tailscale host and archive checksums are regenerated.

Hosting does not register or install a plugin inside a ChatGPT account. The landing
page gives the private browser MCP connection steps and explains where package
import depends on Work account support. MCP and private consent retain their
existing authorization boundaries. Only explicit public artifacts are served.

## Registered browser package

An uploaded `Plugin_...` identifier names a package, not an MCP server.
A private browser package references its separately registered remote server:

```sh
python3 scripts/package_plugin.py --registered-app-id plugin_asdk_app_OBSERVED
```

The generated `code-factory-browser-plugin-<version>.zip` includes the registered
`.app.json` reference, all five skills and branding. It omits `mcp.json`, `.mcp.json`
and the compatibility `mcpServers` setting. Both manifests point to `.app.json`.
No fake or placeholder ID is accepted as a substitute for the observed ID, and
this does not create or authenticate the remote connection itself. Access to that
registered server must be verified in Work.

`code-factory link-plugin --app-id OBSERVED` records the nonsecret reference for
the gateway. Its `/code-factory/plugin-browser.zip` route then generates the linked package
without publishing arbitrary host files. Until a registered ID is recorded, that
route returns HTTP 409 with a setup explanation and no download. The portable
`/plugin.zip` remains a separate artifact and is not labelled as a verified browser
installation. Public submission requires the separate With MCP review flow.

## Conversations and configuration

Open the inbox with `open_factory`, then select an item to inspect its saved
context. Selection starts no agent. Sending `session_chat` creates or resumes that
item's canonical conversation; event rules can also authorize its first action.
The agent uses the repository's selected native Codex profile/agent definition and
a retained worktree. Rules may attach any installed skills. Skills decide how to
review, implement, publish and verify work.

`open_settings` opens a persistent configuration conversation with readable
effective settings and proposed diffs. The bundled administration skill uses
`configuration_chat`, `inspect_configuration` and `propose_configuration`.
Proposing a change grants no authority to apply it: the owner must explicitly
confirm the concrete diff in the app. Stale proposals are rejected and confirmed
changes wait for safe turn boundaries. Native Codex configuration and skills stay
native; there is no separate pipeline/profile-authoring format or built-in plugin
updater. Install or update skills through the native Codex tooling.

Configuration agents use `code_factory_communicate` with `kind`, `title`, `body`
and optional question `options` (label/description). Messages render as titled
plain-text cards. Questions render as forms, suspend the tool call and resume
once with the owner's `answer`; options are suggestions and free text is allowed.
Only explicit communication, owner messages, questions and proposal controls
appear in the configuration conversation. Ordinary native assistant narration
and status summaries remain diagnostic history. Existing retained threads keep
their tool catalog and use explicit status-tool results/questions as a compatible
communication path. Application proposal diffs omit unchanged settings; the
existing `code_factory_propose_configuration` tool requests owner approval through
the separate Confirm/Reject controls. Answering a question never confirms a change.

The bundled status skill teaches agents to call `code_factory_report_status` with
working, done or blocked. Blocked requires a concrete human question or requested
action. Native questions and approvals appear in the same attention queue with
their original options. A completed turn without a status report appears idle.

## GitHub API allowance

Inspect and propose the shared application GitHub budget through configuration
chat. Defaults are 2,000 request attempts/hour, 100 reported quota units reserved,
and a 120-second notification interval. REST quota uses requests and GraphQL quota
uses points; the local allowance counts actual HTTP attempts. Reads honor the
provider's server cooldown, conditional cache and persistent shared gate.

Managed REST reads, GraphQL queries and the generic REST mutation transport share
one request lock across clients and processes using the same private state
database. Admission conservatively aggregates REST endpoints at 900 secondary
points/minute and GraphQL at 2,000; reads cost one point and generic REST writes
cost five. Generic POST requests to GraphQL are conservatively counted as
five-point content operations even for query-only payloads; the application-owned
GraphQL query transport costs one point. Writes also reserve against 80 content requests/minute and 500/hour
before sending. These rolling reservations and provider cooldowns survive restart;
hourly attempts, observed primary quota/reserve and endpoint polling deadlines
remain independent. There is no built-in publication subprocess in the current
daemon; agent and skill subprocesses remain independent clients.

A throttled Sync operation shows its reason and UTC retry time while saved PR
data remains available. Repeated scans join that operation. Safe synchronization
reads resume after the cooldown without resubmitting the owner action; uncertain
mutations are never automatically replayed. Restart preserves provider cooldowns
and background checkpoints, while interrupted operation identities remain
uncertain for inspection. Server `Retry-After` seconds or HTTP dates and applicable
exhausted-primary reset times govern retries. Secondary throttling without an
applicable server deadline waits at least one minute, then increases exponentially
to a bounded 32-minute wait on repeated failures.

Throttled team-membership reads also keep synchronization waiting without
advancing saved repository checkpoints or discarding saved membership. Each scan
admits at most 256 additional request identities; excess unique submissions return
busy, while already accepted identities still resolve to the original scan.
Alias identity reservations share the active 64 MiB admission allowance and are
released when the scan finishes. Accepted terminal identity records remain for
the daemon lifetime, like other operations: this bounds intake during a blocked
scan, rather than total lifetime identity storage.

GitHub's [secondary limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#about-secondary-rate-limits)
also include account-wide concurrency, CPU/resource limits, lower endpoint limits
and undisclosed costs. Local serialization and conservative accounting cannot
measure other clients or guarantee absence of server throttling; response headers
and throttle errors remain authoritative.

Repository summary synchronization and detailed reconciliation recover observable
changes even when the app is closed. Initial import establishes a baseline without
launching historical work. Signed webhooks enter the same durable event stream;
redeliveries and overlapping observed facets are deduplicated. Review requests,
comments, threads and current-head CI can match ordered event rules. Batches wait
for a configurable quiet period, default 120 seconds, and an available session.
Human questions and explicit interruption hold automatic delivery. Chat, answers
and explicit run/resume bypass the timer.

Agent and skill GitHub commands, native plugin management and other clients use
GitHub independently of the daemon's request budget. Verify each installation
separately using the [acceptance guide](acceptance.md).

## API version 2

Upgrade the daemon and plugin together, refresh tool schemas and open a new
content-addressed UI resource. The application database and saved conversations
remain in place; only safe operation metadata is added.

Tool results without MCP `isError` return `structuredContent.data` with
`apiVersion: 2`, the current `instanceId` and a `response`. Check `isError` first:
ordinary tool
errors, including an unavailable document version, use text content without that
envelope. `open_factory` and `open_settings` return ready
bootstrap and `resourceUri`; application data is loaded with separate read tools.
Read responses are `pending`, `busy`, `failed`, or `ready` with `revision` and
`value`. Pending means the requested saved revision is not prepared; do not present
an older snapshot as current. Poll visible pending work after 250 ms.
Read selectors `factory_item.id`, `session_transcript.id` and
`factory_state.itemId` accept at most 1024 UTF-8 bytes. Oversized selectors are
explicitly rejected before projection admission; identifiers are never shortened.
This bound does not limit source bodies or document chunks.
Revisions are comparable only within their daemon instance. A `factory_state`
unchanged check requires both `sinceVersion` and its `sinceInstanceId`; another or
absent instance returns the full current projection. A daemon change retires old
read ownership, including displayed-detail acknowledgments. Late prior-instance
responses cannot replace current content or change the active command identity.

Chat, controls, answers, local edits, configuration proposals/confirmation,
synchronization, diagnostics and native configuration inspection require a new
client-generated `requestId` and bootstrap `instanceId`. Their receipt has
`requestId`, `instanceId`, `tool` and `state`. Poll `operation_status` with those
original identities for queued, running, succeeded, failed, uncertain or lost.
Acceptance is never delivery. A succeeded receipt contains `result`; an unavailable
retained result is explicit. Error details can also expire; `errorUnavailable`
distinguishes missing details from an empty error. Terminal identities and states
survive body eviction, and neither eviction nor restart permits replay. Retained
serialized result/error payloads share a 64 MiB budget, separate from the encoded
document cache; identity metadata is retained separately.
Lost responses are resolved by status without another
submission. Repeated IDs return original work with the same current detail-availability flags
as status polling, including independently evicted document chunks. IDs from an older daemon
instance never authorize resubmission after restart. Queue saturation returns
`busy` without acceptance. Outer JSON-RPC IDs are separate transport correlation
identifiers: their daemon JSON encoding, including escaping, is limited to 512
bytes. Larger identifiers receive HTTP 413 before tool execution or operation
admission; identifiers are never shortened. This limit does not reduce resource
content or change the logical `requestId`/`instanceId` contract. The complete
tool response, including SDK transport validation errors, has a 64 KiB ceiling;
large resource responses retain their separate limit. Background operation execution has a separate five-minute
deadline; interrupted effects are uncertain and never replayed automatically. Doctor
bounds its complete retrieval, including polling, to 20 seconds.

A large value/result is represented as `document` with `version`, `chunks`, `bytes`
and `encoding: "base64-json"`. Fetch each zero-based `document_chunk` using the
exact version and index. Decode base64, concatenate bytes in order, verify the
byte count, then decode UTF-8 JSON. Each encoded chunk is at most 32 KiB and each
tool response at most 64 KiB. Evicted document versions require refreshing their
owning read; never silently truncate exact source, configuration or questions.

`session_questions` returns `questions`, `total`, `offset` and `limit` in its ready
value. It accepts an optional `sessionId`, a `status` (pending by default, or all
for retained history), nonnegative offset and a limit from 1 to 200. The UI exposes
previous/next question pages; exact payloads can still use document chunks.
