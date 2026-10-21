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
Codex 0.161.0. Missing provider executables are reported without replacing them.
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
and install it. The browser package contains both skills and the required app
reference. The portable `/code-factory/plugin.zip` is a separate artifact with
MCP launch configuration for compatible local plugin hosts. A cloud `Plugin_...`
package ID cannot replace the remote `asdk_app_...` connection ID.

Open the installed Code Factory plugin and choose **Try in chat**. Use **Work**.
Its setup skill checks diagnostics and can verify a read-only pilot task. The
ordinary starter prompt opens the inbox. Account/workspace policy still governs
custom connections and event-triggered tasks. Follow the
[official plugin testing flow](https://developers.openai.com/plugins/deploy/connect-chatgpt).

## Daily use

Open the embedded inbox and configure only the dedicated private pilot repository.
Initial records are unread without New; new discoveries get New. Opening detail
marks the displayed revision read. Action history is independent of unread state.
Sync runs every 30 seconds; visible UI polling runs every second after successful
responses. Network failures defer those updates and appear in diagnostics.

Edit a profile, select skills, review the prepared launch and explicitly Start.
Use **Plan · interactive questions** for the supported Codex question path.
Answer in the attention dialog. Follow up in agent chat. Interrupt and Resume are
separate operations. Opening an item never starts work. Closing the UI leaves work
running; the computer and both services must remain available.

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

After daemon/app-server loss, old attention IDs are stale. Refresh, inspect the
retained run/worktree, and Resume explicitly. Answers are never replayed. Clean up
only stopped, clean worktrees; dirty ones are refused. A missing worktree or a
provider authentication failure needs repair before resume.

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
