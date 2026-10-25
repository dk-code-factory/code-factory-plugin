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
and install it. The browser package contains all three skills and the required app
reference. The portable `/code-factory/plugin.zip` is a separate artifact with
MCP launch configuration for compatible local plugin hosts. A cloud `Plugin_...`
package ID cannot replace the remote `asdk_app_...` connection ID.

Open the installed Code Factory plugin and choose **Try in chat**. Use **Work**.
Its setup skill checks diagnostics and can verify a read-only pilot task. The
ordinary starter prompt opens the inbox. Account/workspace policy still governs
custom connections and event-triggered tasks. Follow the
[official plugin testing flow](https://developers.openai.com/plugins/deploy/connect-chatgpt).

## Daily use

Open the embedded inbox and configure monitored repositories in Settings. Use the dedicated private pilot repository for synthetic browser/model acceptance. Working tracks launched work until its GitHub completion; Issues includes linked PR information; My PRs and Review PRs follow GitHub author/reviewer identity. Each queue retains its query and Back context. Item selection opens the conversation without starting an agent.

For manual work, choose a pipeline, review its mode, permissions and publishing authority, and explicitly Start. System defaults can instead opt into automatically starting eligible requested PR reviews; queue-only is the default, and repository policy can inherit or override it. Review/implementation pipeline defaults and additional repository review instructions are configured in Settings. Automatic reviews wait for current work and are durably claimed once per observed request/head. They support read-only profiles with publishing set to ask, or explicitly selected danger-full-access profiles with approvals set to never and publishing set to authorized. Publishing requires owner approval under ask and may proceed autonomously under authorized. Selecting a profile does not enable automatic review or implementation. Each pipeline is one retained Codex session. New run and prepared proposals remain under Item details. Follow up by sending a message; the service saves it durably and acknowledges queue acceptance before background delivery. Inactive work restores its original thread and workspace using saved source context, with task-required GitHub reads left to the agent. Tracking and the conversation distinguish queued, delivering, delivered and uncertain state. Interrupt remains explicit and cleanup refuses dirty worktrees.

Messages lists native, structured-message and recovered textual questions and approvals. Answer the displayed form. If a stopped agent fails to produce a form, the helper gets at most two question-only repair attempts before offering a free-text answer and Open session console. The console shows the same session's technical activity and retains its draft. Take over session cancels further repair. Unknown delivery is never retried automatically; inspect the console first.

PR lifecycle, review approval and current-head CI are independent. Linked implementation PRs can complete issue work on merge without changing the issue on GitHub. Incidental references cannot. Inaccessible or stale PR information remains visibly unknown. GitHub review-notification checks default to two minutes, with repository summary reconciliation every 15 minutes and an initial baseline for new repositories. Full histories/checks refresh only for selected or tracked work and bounded explicit references. Visible UI refreshes through the host bridge; closing the UI leaves work running, while observing source closure or merge terminates its sessions. Dashboard reads use summaries and load full history/check lists only for the selected item. A GitHub rate limit preserves imported rows and delays retries; do not force repeated manual polls during its cooldown.

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
the installer's consistent snapshots. Do not copy only a live `state.db`.
Rollback also archives the current gateway profile and restores its recorded
pre-upgrade profile. The canonical MCP URL can therefore change back; check the
restored profile before reconnecting Work. Old installation records without a
gateway snapshot retain their current profile and need a compatibility check.

After daemon loss, pending work continues from its retained session and saved
progress. Completed turns remain idle; explicit interrupts and unanswered questions
remain paused. Before initial execution or automatic unfinished-work restoration, a targeted fresh
source check compares scope, state, head and assignments. An explicit follow-up
message is saved first and restores its retained session without a GitHub preflight;
the agent receives explicitly unverified saved context and checks GitHub as needed. The agent assesses
changes and reuses only still-valid checkpoints. Failed reads keep work stopped.
Closed issues and closed/merged PRs permanently terminate their sessions and retire
pending requests. Reopening a source requires new work rather than reviving its
terminated sessions. History and workspaces remain; explicit cleanup refuses dirty
files. Answers and completed external actions are never replayed automatically.

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
daemon smoke test. Use the explicit `make test-real-provider` target for real model lifecycle tests.
`python3 scripts/test_systemd.py` separately tests the package under isolated Ubuntu
with a real user systemd manager and fixture provider executables. The container has
no Go or Node. Neither fixtures nor real Codex alone establish Work acceptance.

## Plugin administration

Settings → Plugins & skills lists observed installed plugins, versions, enabled
state, update availability and maintenance operations. Sources, explicit installs/
updates/removals and periodic metadata-check cadence are configurable through the
widget, owner tools and bundled admin skill. See [the administration guide](plugin.md#repository-monitoring-and-plugin-administration). No plugin update is
automatically installed. Unknown remote version metadata stays unknown, and
failed or interrupted operations require inventory inspection before a retry.

## GitHub request budget

Use **Settings → System defaults → GitHub API usage** for the shared rolling-hour
allowance, reported-quota reserve and notification interval. Defaults are
600 requests/hour, 100 reported quota units reserved, and 120 seconds. REST
quota is reported in requests and GraphQL quota in points; the local allowance
counts HTTP request attempts. See [GitHub’s GraphQL quota documentation](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api). Manual polls
obey the same budget and server cooldowns. Limits, conditional cache and recorded
attempts survive service restart; raising a limit does not erase prior usage.

The gate serializes REST/GraphQL requests, reads quota headers instead of polling
`/rate_limit`, and persists safe credential-scoped validators. Failed repository
reads back off; paused/deferred monitoring is distinct from a missing provider.
Diagnostics check local GitHub credential availability without `gh auth status`.
Managed clones use Git transport and the existing credential helper instead of
an API repository lookup. No global authentication configuration is replaced.

Notification GET includes read threads and never marks them read/done. Changed
configured PRs are fetched once per observed notification update; periodic lists
cover absent notifications. Saved details and unread state survive deferred reads.
Do not force repeated polls during a server cooldown. Independent plugin/skill/
agent commands and other GitHub clients are outside the daemon cap. The optional,
disabled alternate-tunnel installer has one explicit unauthenticated release
lookup when its client is absent; it is not a background poll of this pilot.

Verify quota controls on each installation and retain private evidence outside
the checkout. Callback, soak, login and manual accessibility acceptance remain
separate; see [acceptance](acceptance.md).

## Bounded review command sandbox · 2026-10-23

Read-only and workspace-write tasks now use an invocation-local native permission
profile in pinned Codex 0.161.0. Source permissions, private writable scratch and
network access are independent. Reviews cannot change their source checkout or
Git metadata; implementations can change their own checkout. Required executables,
selected-repository Git metadata, installed skills and existing GitHub auth
configuration remain readable. Unrelated owner files are excluded.

The enforced command proxy allows `api.github.com`, `github.com`,
`raw.githubusercontent.com`, `objects.githubusercontent.com`,
`codeload.github.com`, `api.openai.com`, `chatgpt.com` and `auth.openai.com`.
Other destinations, localhost and private-network access remain blocked. Temporary
artifacts and isolated review clones belong under `TMPDIR`. Skill data indexes for
repository rules and PR decisions are bounded per-run copies under `XDG_DATA_HOME`;
refreshing them cannot modify the owner's original settings. Resume preserves the
run cache and reselects the same process-local policy. Explicit clean cleanup
removes scratch. No global Codex config, authentication or Tailscale change is
required, and initialization failure never falls back to full access.

The policy governs sandboxed commands. Codex service/control traffic, MCP servers,
apps and browser tools retain their separate controls. A destination allowlist
does not filter HTTP methods or grant publishing authority: the profile's approval
and publishing rules remain applicable. Existing explicitly selected full-access
profiles remain a separate owner choice. Agent/skill GitHub commands still use
GitHub independently of the daemon's request budget.

Keep fixture checks, real pinned-provider command-only checks and installation
observations separate in private operator evidence.
Native tests verify GitHub TLS without an HTTP API request, denied other/private
destinations, blocked direct localhost access, read-only review files, protected
Git metadata, workspace-only implementation edits, inaccessible unrelated files,
unchanged host files and persistent private scratch. They also verify existing
GitHub authentication and local reviewer configuration without printing tokens.
Linux may provide private synthetic parent directories inside the namespace;
writes there are checked against the unchanged owner host files. These command
checks do not establish a full model-led third-party review pipeline or close
callback, soak, login, nested runtime or accessibility acceptance gaps.

Shell temp settings include both `TMPDIR` and zsh `TMPPREFIX`; otherwise heredoc
creation can fail despite a writable scratch directory. Installed-version checks
and the initial-candidate failure are retained in the sandbox evidence.

## Dependency updates

Use the [private owner dependency API](dependency-admin.md) to check and update
Codex, installed plugins and standalone skills with recorded GitHub provenance.
Operations retain per-dependency outcomes and backups and never replay uncertain
changes. New runs load updated skill bundles; retained runs keep frozen contents.
