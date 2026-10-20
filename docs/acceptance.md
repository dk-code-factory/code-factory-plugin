# Work acceptance gate

Status: **core browser workflow verified; full pilot readiness blocked**
(2026-10-20).

Current daemon: **0.1.0-850eb63cda16**. Complete browser plugin: **0.2.2**.
The [installed private plugin](https://chatgpt.com/plugins/Plugin_34caeb5c66048191a1ac0c8f01c31e73)
references the observed registered app
`asdk_app_6ad792b279348191b76d3443615b66e0`. Its page shows Apps1, Skills2,
version0.2.2, a connected account and **Try in chat**. The starter opens browser
Work successfully. The canonical endpoint is
`https://dkropachev-ai-rig.tail62c9f9.ts.net/code-factory/mcp`.

The agent controlled the owner's signed-in Chrome using owner-approved local
Chrome DevTools auto-connect. Authentication values, headers and browser profiles
were not exported. The actual ChatGPT browser creation, OAuth callback, tools,
embedded UI, installed setup skill and real Codex interaction were observed.
[Versioned evidence](evidence/2026-10-20-work-browser-acceptance.json) and
[complete automated verification](evidence/2026-10-20-final-verification.log).

| Current browser checkpoint | Observed result |
| --- | --- |
| Reachability | ChatGPT discovery/creation timed out on10000; equivalent443 discovery succeeded in1.5s. Code Factory moved under `/code-factory` with scoped RFC metadata routes. All previous Tailscale routes and privacy boundaries retained. This is an observed workaround, not a general documented port ban. |
| Native consent | Fixed `no-referrer` causing native form `Origin:null`, and CSP blocking the OAuth redirect. Private GET uses `strict-origin` and permits only the validated ChatGPT callback beside self. Native Chromium regression passes strict origin/referrer, real303 redirect, state/code, replay and invalid-origin checks. |
| Actual Work OAuth | DCR201, private owner approval303, token exchange200 and authenticated MCP200 observed. No callback ID was invented. |
| Complete plugin | Linked browser ZIP uploaded and installed through ChatGPT; both skills and required app are present. Desktop-launch behavior resolved. |
| Inbox | Actual embedded iframe shows Connected,2issues,1PR from the private pilot repository, and advancing30-second sync timestamps. The model could not see its widget; the browser agent directly verified it. |
| Real Codex | Installed setup skill prepared an edited Plan-mode/read-only snapshot, started Codex0.161.0 in isolated pilot worktree09f11628428c72dea969f0f860b6334d, answered Focused and sent a follow-up. Durable transcript confirms both. |
| Actual widget answer | Sent another request through embedded agent chat; a native `item/tool/requestUserInput` question appeared. After recovery, clicked Continue and Answer. `UI_ANSWER_OK` and `UI_FOLLOWUP_OK` appeared in the widget; run returned idle. |
| Recovery | Reload retained pending attention; actual daemon restart marked the old request stale and the real MCP endpoint rejected its answer. Explicit UI Resume preserved worktree/history and asked a fresh question. Gateway restart retained authenticated polling with no UI alerts. |
| Rollback migration | Real df7e345c8bba→c68e17b77f80 rollback restored compatible SQLite and the old10000 transport profile; upgrade/configure returned to443. The current850 build contains the same tested installer plus browser header fixes. A later live850 rollback was not repeated because it would archive newer active history. |
| Automated checks | `make verify` passed on850 source: race/protocol/native-browser/frontend tests, vet/types, packaging, installer failure/recovery and compiled smoke. |

**Readiness remains blocked.** A fresh Work chat after Refresh tools explicitly
reported that host MCP Events subscriptions are unavailable. Zero subscriptions
exist; no real signed callback delivery, host response, filtering, refresh or
unsubscribe is claimed. Fixture and protocol tests do not substitute for these.
The exact host/account restriction remains undetermined.

Also incomplete: a one-hour connected soak, a coordinated logout/login startup
check, the full current-version Work New/revision/priority/cleanup matrix, and
host CSP enforcement (the host displayed CSP off). The earlier local checks below
remain dated evidence for their original versions. The private fixture repository
and test worktrees are retained; fixture issues/PRs remain open while acceptance
is incomplete. Operator-recorded checkpoints under the private application data
directory distinguish verified and unverified states for850.

Use [the operating guide](operations.md) for setup, daily use, diagnostics,
upgrade, compatible-profile rollback and uninstall. The agent can repeat browser
tests through [the browser controller](browser-lab.md).

## Earlier installation and test history

The entries below retain earlier observations and blockers. Statements that Work
or browser registration had not been verified describe that earlier point in time;
the current results above supersede them.

Selected transport: **Tailscale Funnel + OAuth**, replacing the OpenAI tunnel at
the user's request. Endpoint:
`https://dkropachev-ai-rig.tail62c9f9.ts.net:10000/mcp`.
The private approval page uses Tailscale Serve on 8444 and checks the exact owner.
No Platform tunnel identifiers, organization/workspace IDs or runtime key are
needed for this route. See [Tailscale setup](tailscale.md).

[Tailscale evidence](evidence/2026-10-19-tailscale.json) records the installed build and real HTTPS checks. A real HTTPS test passed public registration, owner recognition through Serve,
PKCE exchange, authenticated MCP access to the real pilot inbox, refresh rotation,
revocation, unauthenticated denial and public approval-page denial. Public ingress
was separately verified through public DNS IPv4 with TLS hostname verification.
Existing Tailscale listeners on 443 and 8443 were preserved. The OpenAI tunnel
unit remains inactive. Work's actual OAuth connection, UI and callbacks remain
unverified until observed in Work.

Local tests and real Codex lifecycle tests have passed. These do not establish
an installed Work connection. The user confirmed browser access and has been given the Tailscale URL. No observed Work app ID or cloud callback receipt has been supplied or inferred.

Local evidence is retained in [the versioned verification record](evidence/2026-10-19-verification.json) and [verification log](evidence/2026-10-19-local-verification.log).
Installed build: **`0.1.0-9ea1273b279a`**, daemon active under the real user
systemd manager. The stable launcher and persistent plugin are installed. The Tailscale OAuth gateway is active. The optional OpenAI tunnel unit is inactive. `doctor` reports no operational
failures and leaves Work acceptance unverified.

Real host upgrade/rollback was exercised through `ce2d2e486051`, `775ee3fee883`,
`b6d163cfd985` and finally `5dde57428e46`; authenticated version health passed
and pre-upgrade database snapshots were retained. Both units passed systemd's
unit verifier, with the tunnel requiring/starting after the daemon. Crash restart
was exercised with SIGKILL and the configured restart policy.

The installed daemon's local GitHub/Codex acceptance on `0.1.0-b6d163cfd985`
verified the initial two-item baseline, a subsequent New issue after 27.43 seconds,
unread restoration after a comment, independent action history, PR enrichment,
a real Plan-mode question/answer/follow-up, restart recovery and stale-answer
rejection, explicit resume and dirty/clean worktree cleanup. These results are
[versioned local evidence](evidence/2026-10-19-installed-local-acceptance.json),
not evidence of Work UI or callback behavior. Upgrades retained the pilot state.

[Installed diagnostics](evidence/2026-10-19-tailscale-diagnostics.json) contain
only nonsecret operational summaries. User services preserve the existing login
policy; a real logout/login check is still outstanding. Use [the operating guide](operations.md) for commands and recovery.

| Local checkpoint · 2026-10-19 | Observed result |
| --- | --- |
| Go race tests, vet and frontend types | Passed |
| Browser fixture tests | Passed |
| Assembled browser → MCP → app → SQLite → Git → fake Codex | Passed launch/question/answer/follow-up; iframe made no daemon network request |
| Real Codex 0.161.0 | Passed real question/answer, duplicate/stale rejection, selected skill, follow-up, interrupt, resume, priority inference |
| Streaming output | 2,048-notification RPC regression and all secret split-point tests passed |
| Credential form | Reachability, origin/nonce checks, expiry and concurrent single-submission tests passed |
| Packaged installation fixture | Fresh/rerun, spaces, missing prerequisite, checksum failure, occupied port, failed/successful upgrade, rollback, partial failure, uninstall/reinstall passed |
| SQLite backup | Committed WAL rows present in private consistent snapshot |
| Official tunnel client | Installed v0.0.16 from official Linux ZIP; SHA256 matched release asset digest `d60cdba019bce451bcc3a15478cc5b9cb11270b049f5b56ea39a80b517f8b117` |
| GitHub isolation | Private `dkropachev/code-factory-pilot-20261019`; issues #1 and #3, PR #2, labels, relationship comment, COMMENT review and synthetic status seeded |
| Existing login policy | `loginctl show-user` reported `Linger=yes` before installation; setup did not alter it |
| Isolated Linux user systemd | Passed on `0.1.0-5dde57428e46`: real user manager, install/rerun, authenticated health after restart, retained SQLite profiles, uninstall retaining state; no Go/Node. Providers were fixtures. [Log](evidence/2026-10-19-isolated-systemd.log). Initial harness failures were corrected. |
| One-hour Work soak and logout/login | Not run; require connected Work and a user-coordinated login cycle |

The private [pilot repository](https://github.com/dkropachev/code-factory-pilot-20261019) is retained.
The installed plugin is not linked to any invented app ID. The next guided step is to add the provided HTTPS endpoint in Work using OAuth, approve on the private Tailscale page, link the observed Work app ID and test in a fresh conversation.

Remaining required checks are real Work OAuth discovery,
installed Work UI round-trip and visible-poll timing, Work subscription and signed
callback receipt/filtering/refresh/unsubscribe, UI/tunnel reconnection with pending
attention, one-hour connected soak, and logout/login service startup. Their absence
keeps readiness blocked.

Fixture issues/PRs will be closed after
live acceptance; they remain open while initial unread/New checks are pending.

Use the plugin's setup skill with local computer/browser access. Record actual evidence for each checkpoint; do not infer a passed cloud connection from a locally healthy daemon.

| Check | Required evidence |
| --- | --- |
| Linux packaging | User daemon service starts, restart preserves SQLite state; installed binary and PATH remain valid |
| Provider access | Existing `gh` and Codex auth work; Codex reports 0.161.0; missing/revoked access is visible |
| Private tunnel | Tailscale HTTPS Funnel reachable; OAuth/PKCE enforced; only the owner can approve over private Serve |
| Ownership | Private Tailscale consent accepts only the configured owner; Work connection is privately installed |
| MCP protocol | Work discovers `2026-07-28`, tools, UI resource and all three events |
| UI | Work opens embedded resource; polling uses app-only tools; no iframe localhost network requests |
| Inbox | Configure a repository in UI; initial records unread without New; a later created item arrives in the next successful 30-second sync |
| Item states | Opening marks its displayed revision read; new comment/review/commit restores unread; recording an action does not consume it |
| Priority | Override beats mapped label, which beats inference; edits to linked context/policy invalidate cached inference; inference failure stays visible |
| Launch | Select profile, edit prompt/model/effort/skills/base; review permissions, publishing and worktree; only Start creates work |
| Attention | Read-only task asks one question; badge and dialog appear on next successful UI poll; answer reaches matching live run once |
| Approvals | Command/file changes and requested permissions display their details; permitted choices reach Codex; disallowed grants are rejected |
| Interaction | Follow-up starts idle turn or steers the expected active turn; interrupted turns require explicit resume |
| Persistence | Close/reopen UI and tunnel while a question is pending; same run/request remains available |
| Recovery | Stop daemon/app-server; retained run is interrupted, old answers fail, explicit resume preserves worktree/history |
| Event callbacks | Work subscribes, challenge echoes, signed event receives 2xx, chat receives alert; nonmatching entity is filtered |
| Subscription lifecycle | Duplicate creation is idempotent; expiry, refresh, rotation, retry IDs, restart and unsubscribe behave correctly |
| Credentials | Jira/tunnel values and secret answers are absent from returned tool data, logs and transcripts |
| Cleanup | Dirty retained worktree is refused; explicit cleanup of clean stopped run succeeds |

MCP Events is available in supported Work Cloud chats, subject to workspace controls. The host controls background alert timing, batching and notifications. The UI displays pending attention when its next poll succeeds. The computer and both user services must remain online.

Sources used for integration contracts:

- [Codex app-server](https://learn.chatgpt.com/docs/app-server)
- [Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
- [MCP Events](https://developers.openai.com/plugins/build/mcp-events)
- [Plugin packaging](https://developers.openai.com/plugins/build/plugins)
- [MCP Apps patterns](https://apps.extensions.modelcontextprotocol.io/api/documents/Patterns.html)

A tunnel is supported for private connections and testing, not public plugin distribution. Claude plugin/Channels and desktop-specific navigation/notifications remain later releases; the core and legacy MCP adapter are reusable boundaries, not evidence of those products being delivered.


## Complete private plugin · 2026-10-19

Plugin `0.2.0` now bundles the remote MCP connection, portable and compatibility
manifests, branding and setup/daily-use skills. The ZIP and its contents have
checksums. Portable manifests pass their canonical schemas; packaging tests verify
transport consistency, asset paths, deterministic builds and exclusion of private
host state and credentials. A real Codex plugin host discovered and installed the
package in an isolated `CODEX_HOME`. Both skills pass the Skill Creator validator.

The installed source is registered in this machine's personal plugin marketplace
and is discoverable as `code-factory@code-factory-personal`. Existing plugin
choices remain unchanged. This does not constitute a Work cloud installation.
The user has not yet specified their Work surface or completed its OAuth/install
flow. See [plugin installation](plugin.md) and [versioned package evidence](evidence/2026-10-19-plugin-package.json).


## Plugin package hosting · 2026-10-20

The complete plugin is now served from the same machine through Tailscale:

- Installation page: https://dkropachev-ai-rig.tail62c9f9.ts.net:10000/
- Complete ZIP: https://dkropachev-ai-rig.tail62c9f9.ts.net:10000/plugin.zip
- Release and checksum: `/plugin-release.json` and `/plugin.zip.sha256`.

Build `0.1.0-9ea1273b279a` embeds the plugin archive. The running gateway binds its
connection metadata to the configured HTTPS host and produces matching checksums.
Actual HTTPS requests verified package/manifest/assets availability and ZIP
contents/checksums. Public Funnel ingress, resolved through public DNS rather than
the tailnet route, returned the same ZIP. A headless Chromium browser opened the
page and downloaded `code-factory-plugin-0.2.0.zip`. MCP unauthenticated access
remains 401; public consent and private application files remain 404. Existing
Tailscale routes are unchanged. [Evidence](evidence/2026-10-20-hosted-plugin.json)
and [page screenshot](evidence/2026-10-20-hosted-plugin.png).

This verifies hosting and package delivery. It does not create a registered Work
app, import bundled skills into a browser account, or establish Work acceptance.
The hosted page states these limits and provides the supported browser MCP/OAuth
connection steps. No automatic install/deep link is invented.

## Browser package wiring blocker

The user supplied HTML for installed cloud package
`Plugin_e162fcc74c788191975d60b6f80137f2`. It displays version 0.2.0 and an explicit
`/codex/open-app?target=plugin&plugin_id=...` action. A subsequent Work conversation
reported that `open_factory` was unavailable. Neither is a successful Work
acceptance checkpoint. Server authorization state contains only test clients and
no Work access/refresh tokens.

The packaging code now supports a distinct private browser artifact linked to an
observed registered MCP server ID via `.app.json`, without bundled MCP launch
configuration. Tests reject the supplied Plugin_ package ID as a server ID and
verify checksums and connection references. The gateway refuses to offer an
unbound browser ZIP (HTTP 409). An actual asdk_app_/connector_ server ID has not
been provided, so no working browser ZIP or completed cloud installation is
claimed. A registered server ID and real OAuth/Work verification remain required.


## Observed app-creation blocker

The user retried Add custom MCP server and received: “A service needed to create the app is temporarily unavailable. Try again shortly.” The initial investigation inferred a ChatGPT app-creation failure because local HTTPS worked and no new gateway requests appeared. **That inference is withdrawn.** Local DNS can route this hostname through the tailnet, bypassing public Funnel ingress; a TLS failure before HTTP produces no gateway request. The historical [record](evidence/2026-10-20-app-creation-blocker.json) is superseded by the public-ingress investigation below. The precise cause of the user's ChatGPT error remains unverified.

## Independent connection investigation · 2026-10-20

Two independent agents investigated gateway interoperability and built a private
browser lab. The public probe explicitly resolves public DNS and connects to each
ingress IP while retaining TLS SNI and certificate verification. Both addresses
initially closed TLS before HTTP. A separate GitHub-hosted Ubuntu runner reproduced
that failure, ruling out tailnet-only reachability as evidence of public access.

A targeted port-10000 Funnel reconfiguration did not repair access. Restarting
`tailscaled` retained authentication and all existing routes. After propagation,
the independent MCP SDK completed public OAuth discovery, DCR, PKCE with real
owner-only private consent, transport negotiation, discovery of 24 tools,
`open_factory`, and the embedded UI resource. Its test tokens were revoked.
Subsequent repeated probes still found one ingress address failing, so a successful
SDK exchange is not a stability or ChatGPT acceptance pass.
The [dated evidence](evidence/2026-10-20-independent-connection.json) records the
installed version, public/local SDK distinction, repeated probes and independent
external runs. The first external recheck also found one edge failing. After
further propagation, both addresses passed fresh verified TLS and the full raw
OAuth/MCP lifecycle; the independent SDK also passed with a new socket and
validated peer address for each IP. The final
[external runner](https://github.com/dkropachev/code-factory-pilot-20261019/actions/runs/39445603900)
passed both addresses. Earlier SDK IP checks could reuse a pooled socket; those
probe results are superseded by the corrected fresh-connection evidence. This
records recovery at the recheck, not a completed soak or real ChatGPT connection.

The failure resembles Tailscale's documented control-session/ingress registration
issues ([20739](https://github.com/tailscale/tailscale/issues/20739),
[21114](https://github.com/tailscale/tailscale/issues/21114)); the exact upstream
cause is unproven. No ACL, login state, provider credentials or unrelated routes
were replaced. Installed Tailscale 1.102.4 already includes the earlier 1.102.2
capability-filter fix; no downgrade is justified by that older regression.

Repeatable checks are `make connection-test` and `make connection-sdk-test`.
They deliberately fail on any failed public address. Local SDK verification with
`--tailnet` is explicitly labelled local-only. Reports contain check metadata,
never OAuth values or inbox contents. The private [browser lab](browser-lab.md)
is running on Serve port 8445 with a separate local profile. The user must sign
in and explicitly confirm before the agent inspects or automates that session.
Actual ChatGPT creation, app ID, plugin tool availability and Work acceptance
remain unverified.
