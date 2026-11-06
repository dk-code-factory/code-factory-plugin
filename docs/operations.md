# Personal pilot operations

Code Factory is a single-user Linux x86-64 service. The readiness ledger is
[acceptance.md](acceptance.md). A healthy local daemon does not prove Work access.
Jira stays disabled for this pilot.

## Setup

**Selected transport: Tailscale.** Follow [the Tailscale connection guide](tailscale.md) and run `code-factory configure-tailscale` after installing. It replaces the Platform tunnel selection and runtime-key steps below. No OpenAI tunnel identifiers or runtime key are needed for Tailscale.

The agent runs commands; the user handles browser choices and password fields.
Unpack the versioned `dist/code-factory-…-linux-amd64.tar.gz` archive and verify its
adjacent SHA256, then its `SHA256SUMS`. From the extracted directory run
`./code-factory install-dependencies` and `./code-factory install`.
The installer needs no Go or Node. It reuses Git, authenticated GitHub CLI and
stable Codex 0.161.0 or later. Managed Codex updates require a compatibility probe. Missing provider executables are reported without replacing them.
The tunnel downloader uses the official latest-release metadata and requires a
matching SHA256 digest. The checksum establishes integrity against that official
HTTPS source; it is not an independent release signature.

The stable launcher is `~/.local/bin/code-factory`. Releases, backups, recovery
records and the installed plugin are under `~/.local/lib/code-factory`.
Data stays at `$XDG_DATA_HOME/code-factory` or `~/.local/share/code-factory`.
The install records its PATH and executable locations in private `runtime.json`.
If a provider moves, reinstall from the release after repairing its installation.

After installation run `code-factory configure-tailscale`. The canonical MCP
endpoint is `https://YOUR-TAILSCALE-HOST/code-factory/mcp`; setup preserves the
other public port-443 routes and keeps approval private on port 8444. No Platform
tunnel selection or runtime key is needed. See [the connection guide](tailscale.md).

In [ChatGPT Plugins](https://chatgpt.com/plugins), choose **Add → Add custom MCP
server**, enter that URL, select **OAuth**, and choose **Create as a plugin**.
Approve **Connect Code Factory** in your owner-connected browser. Reuse an
existing working connection rather than creating another.

Record the registered remote app ID shown by its plugin page. Run
`code-factory link-plugin --app-id OBSERVED`; then upload the linked browser ZIP
from `/code-factory/plugin-browser.zip` through **Add → Upload plugin archive**
and install it. The browser package contains all five skills and the required app
reference. The portable `/code-factory/plugin.zip` is a separate artifact with
MCP launch configuration for compatible local plugin hosts. A cloud `Plugin_...`
package ID cannot replace the remote `asdk_app_...` connection ID.

Open the installed Code Factory plugin and choose **Try in chat**. Use **Work**.
Its setup skill checks diagnostics and can verify a read-only pilot task. The
ordinary starter prompt opens the inbox. Account/workspace policy still governs
custom connections and event-triggered tasks. Follow the
[official plugin testing flow](https://developers.openai.com/plugins/deploy/connect-chatgpt).

## Daily use

Open the embedded inbox and use configuration chat to propose monitored
repositories, native agents, event rules, batching and priority policies. Inspect
the concrete diff and explicitly confirm each change. Active turns finish before
confirmed configuration takes effect. Use the dedicated private pilot repository
for authorized browser/model acceptance.

Each PR has one retained conversation and worktree. Its first authorized event
action or user message creates the session. Merely opening an item starts nothing.
Rules select GitHub event types/actions, optional filters, instructions and any
installed skills; all matching actions enter an ordered batch. A new matching
event resets the quiet timer (120 seconds by default, with repository overrides).
Automatic delivery waits for the active turn, unanswered requests and explicit
interruption holds. Chat, answers and explicit run/resume bypass the timer.
Skills own review, implementation, publication and verification decisions.

Messages preserves native questions and approvals, exact options and blocking
semantics. A blocked status report must supply a question or requested action.
Answer the displayed form before continuing a native blocking turn. Execution
state, reported work status and queued events are separate; finishing a turn alone
does not report completion. No classifier or repair agent interprets stopped text.

Issues retain manual conversations, priorities and issue-to-PR relationships.
Manual priority overrides precede mapped labels, then configured read-only agent
assessment. Working tracks retained work and linked implementation completion.
Source closure, merge and reported done preserve conversations and worktrees.
Cleanup is explicit and refuses dirty worktrees.

Signed webhooks are persisted before acknowledgement. Polling reconciles
observable changes and initial import establishes a quiet historical baseline.
Both sources share durable normalization/deduplication. Failed reads and rate
limits preserve saved rows and honor cooldowns. Do not force repeated manual polls
during a cooldown. Closing the app leaves the daemon running.

## Diagnostics and evidence

`code-factory doctor` queries the authenticated daemon when available. The CLI and
plugin use the same typed verified/failed/unverified checks. Provider checks never
return raw provider output. Local tunnel readiness and callback endpoint receipt
are observations; fresh-chat behavior and receipt inside Work require real evidence.

Inspect service state with `systemctl --user status code-factory.service` and
`systemctl --user status code-factory-tunnel.service`; use the user journal for
startup errors. Never enable raw tunnel HTTP logging. Runtime keys and secret
answers must not appear in diagnostics or transcripts. Streaming redaction
withholds a suffix that could be the start of a known secret; an unfinished suffix
is discarded on process loss.

Incoming Code Factory MCP and private owner API calls are recorded in
`$XDG_DATA_HOME/code-factory/logs/api-calls.jsonl` (normally
`~/.local/share/code-factory/logs/api-calls.jsonl`). Each JSON line contains UTC
`startedAt`, the API/tool name, optional JSON-RPC method, HTTP method,
`parameters`, `httpStatus` and `durationMs`. Timing covers server request handling,
including validation and response writing; it excludes client/WAN orchestration,
background agent execution and writing the log itself. HTTP 200 can still carry
a tool-level error; this field is transport status, not task completion proof.

Credential fields, answer payloads, free-form messages/prompts/content and
metadata are redacted. URLs omit userinfo, queries and fragments; known service
credentials and common token forms are masked. Other parameters retain bounded
values; oversized or malformed input is represented by safe markers. Responses,
headers, raw request bodies and exception text are not logged. Rejected requests
that reach these endpoints are recorded too, without reading an unauthorized
request body. This does not enable raw gateway/tunnel or outbound GitHub logging.

The log directory is private (0700), files are 0600, and rotation retains the
current 10 MiB file plus up to three numbered backups. A failed log write emits
one generic service warning and never retries or changes the API operation.
Logging observes request reads without consuming or replacing their body/limits.
Newly built logging is source-only until a release containing it is installed;
prior calls are not reconstructed.

Record a nonsecret evidence reference for each observed acceptance checkpoint:

```sh
code-factory record-evidence --checkpoint question.answer --state verified \
  --evidence '2026-10-19 Work conversation reference and observed result'
```

Records are private, dated and bound to the binary version. Doctor reports the
acceptance gate verified only when every required checkpoint has evidence for that
build. This is an operator-maintained ledger, not automatic proof of cloud access.

## Upgrade and recovery

Extract and verify a new release, then run
`code-factory upgrade --from /absolute/path/to/extracted-release`.
The daemon and tunnel stop before a consistent SQLite `VACUUM INTO` snapshot.
Installation switches the version link and requires authenticated health for the
new version. A failed upgrade restores the prior binary and database and attempts
to restart the previous services. Recovery failures are reported explicitly.
`pending.json` retains an interrupted operation for explicit rollback.
The first cutover preserves the legacy `state.db` without importing it and records
that `factory-v1.db` was absent. Recovery restores that absence before starting
the old binary. Explicit rollback archives any new wrapper database before
removing it; a missing recorded database snapshot fails before stopping services.
The installer also snapshots the existing Tailscale profile beside the database
backup. Recovery restores that compatible gateway profile before restarting the
previous binary; OAuth credentials remain in their separate private state file.
For the first upgrade from a version lacking this backup support, run the
extracted new release's `./code-factory upgrade --from ...`, so the new installer
creates the transport snapshot before switching binaries.

Run `code-factory rollback` to restore the pre-upgrade database and previous
binary. State created after the upgrade leaves the active database; a separate
pre-rollback snapshot retains it. Worktrees, vault encryption key and owner token
are preserved. Keep backups private and copy the full data directory for recovery
of encrypted credentials. An offline copy of SQLite must include its WAL, or use
the installer's consistent snapshots. Do not copy only a live application database.
Rollback also archives the current gateway profile and restores its recorded
pre-upgrade profile. The canonical MCP URL can therefore change back; check the
restored profile before reconnecting Work. Old installation records without a
gateway snapshot retain their current profile and need a compatibility check.

The rebuilt application uses fresh `factory-v1.db` state. It imports no old runs,
settings or history. Native Codex authentication/configuration and connection
infrastructure remain separate. Preserve old databases and private backups until
the owner explicitly chooses their removal.

After daemon loss, queues and delivery claims survive. Connection-bound request
identities become stale; answered requests are never replayed. Uncertain sends
hold work until retained thread history is inspected. Use explicit reconciliation
and resolve an uncertain batch only after checking the conversation. Ordinary
resumption reuses native history and the retained worktree; it never blindly
repeats an uncertain external action. Source closure or merge does not terminate
the conversation. Explicit cleanup refuses dirty files.

## Uninstall

Run `code-factory uninstall`. It disables/removes the two user units and removes
the launcher. Credentials, settings, history, worktrees, release backups and the
installed app mapping remain for recovery/reinstall. Existing GitHub, Codex and
tunnel-client installations remain available. Removing the retained directories is
a separate explicit destructive operation. Setup never changes login lingering.

## Verification

`make connection-test` probes every public Funnel IPv4 address using verified TLS
and exercises owner consent, OAuth, MCP, refresh rotation and revocation against
the installed service. `make connection-sdk-test` repeats discovery, OAuth and
read-only MCP calls through the independent JavaScript MCP SDK. Both are opt-in,
require the configured private owner route, and make no model calls. They revoke
their ephemeral tokens; nonsecret test client registrations may remain. JSON
reports under `/tmp/code-factory-*.json` distinguish public access from local-only
SDK checks. A passing tailnet request cannot prove public Funnel health.

For intermittent TLS failures, compare all public ingress addresses before
attributing an app-creation error to ChatGPT. Preserve route configuration and
credentials. A Tailscale daemon restart can briefly interrupt every tailnet
service; allow ingress propagation afterward and recheck externally. Repeated
restarts or ACL changes are not a substitute for investigating an unhealthy
public edge. The private [browser lab](browser-lab.md) supports signed-in browser
acceptance after manual owner login. Its debugger stays on loopback and its UI
must stay private.

`make verify` builds, checks types/vet, runs race/protocol/browser tests, creates a
checksummed package, exercises installer failure/recovery paths and runs a compiled
daemon smoke test. The explicit `make test-real-provider` target checks the installed
Codex handshake, account read and skill discovery without starting a model turn.
It does not test real model lifecycle behavior; that requires separately authorized
verification under the browser runbook.
`python3 scripts/test_systemd.py` separately tests the package under isolated Ubuntu
with a real user systemd manager and fixture provider executables. The container has
no Go or Node. Neither fixtures nor real Codex alone establish Work acceptance.

## Native configuration and skills

Configuration chat inspects actual Codex layers, profiles, agent definitions and
installed skills. Read/propose tools cannot confirm their own changes. Every edit
requires an explicit owner confirmation, stale previews are rejected, and unrelated
settings are preserved. Use native Codex tooling to install or update plugins and
skills; the former application maintenance forms and updater are retired.
See [the plugin guide](plugin.md#conversations-and-configuration).

## GitHub request budget

Inspect and propose the shared rolling-hour allowance, reported-quota reserve
and notification interval through configuration chat. Defaults are 600 request
attempts/hour, 100 reported quota units reserved, and 120 seconds. REST quota uses
requests and GraphQL quota uses points. Manual polls obey the same persistent
provider gate, conditional cache and server cooldown as background synchronization.
Changing an allowance does not erase prior usage.

Diagnostics check credential availability without dumping authentication output.
Managed clones use Git transport and the existing credential helper. Agent/skill
commands and other GitHub clients remain outside the daemon provider cap.

## Native permissions

Item sessions use the selected native Codex profile and agent definition. Code
Factory supplies the retained worktree and session tools; it does not maintain a
second review-specific permission or publishing policy. Inspect native settings
and select appropriate installed skills when configuring automatic actions.
Configuration and priority bookkeeping sessions are read-only, disable external
MCP/apps, and expose only their bounded local tools. Unsupported experimental
client-tool schemas stop startup rather than silently losing those tools.

Keep fixture checks, real-provider checks and installation observations separate
in private operator evidence. Public connection, installed-host and model tests
remain explicit opt-ins under [the browser runbook](agent-e2e.md).
