# Work acceptance gate

Status: **workbench and bounded question-recovery smoke verified; full pilot readiness blocked**
(2026-10-22).

Current installed daemon: **0.1.0-ebf7a6cdd84a**. Complete browser plugin: **0.2.2**.
The [installed private plugin](https://chatgpt.com/plugins/Plugin_34caeb5c66048191a1ac0c8f01c31e73)
uses registered app `asdk_app_6ad792b279348191b76d3443615b66e0`; its package and
registered app identifiers remain distinct. The existing signed-in pilot profile
and connected account were reused. Historical results below remain bound to their
recorded versions.

## Workbench, PR monitoring and question recovery · 2026-10-22

Installed **0.1.0-ebf7a6cdd84a** provides Working, Issues, My PRs, Review PRs and
Messages, with open Issues by default. Working follows GitHub completion rather
than a stopped agent. Linked PRs expose independent lifecycle, review and current-head
CI, while manual/development implementation links remain distinct from references.
The actual pilot PR displayed open / no review decision / CI success. Pipeline
selection showed its selected mode, read-only sandbox and ask-before-publishing
authority before explicit first-message Start.

All **68 browser tests**, Go race tests, reviewed specification checks and branch
CI passed. **23 fixture Axe scans** had zero automated violations; incomplete
nodes and manual accessibility remain separate. The pinned **Codex 0.161.0** real
lifecycle target also passed in an isolated temporary repository before the final
technical-message display correction. Fixture coverage includes two failed form
attempts, free-text fallback, restart/answer races, native/async formats, GitHub
completion, current-head metadata and exact PR/check links.

After Refresh tools and a fresh installed Work chat, a real native question reached
Messages and its UI answer completed; a UI follow-up retained the same thread.
That final Plan/read-only/ask snapshot was prepared and started through the owner
MCP interface after authority review. An earlier `71a9` build also exercised the
explicit Plan launch review/Start in the actual UI. On the final build, a new
Investigation / Default / read-only / ask session was launched through the UI
pipeline selector and first-message Start. It ended with a plain-text question;
the helper produced a native form on its **first repair attempt**, the UI answer
continued the original thread, and the completion marker appeared with no remaining
questions. Automatic repair/continuation input stayed outside the normal
conversation and appeared as two Question recovery blocks in the same-session
console. Console switching retained the draft; the owned draft was then cleared
using native keyboard editing. The four new test worktrees were cleaned, retaining
history. Issue-5 work remains tracked until its GitHub completion, as configured.

A keyboard-navigation error triggered cleanup of older clean pilot run
`09f11628428c72dea969f0f860b6334d`. Its tracked worktree was restored from the
retained branch and its prior interrupted state restored while the services were
stopped with no live/idle runs. The branch head, path, Codex thread and other run
record fields were verified. All six original run states match their baseline at
the end. Owner credentials and the pinned executable path were preserved. Required
unrelated public-root/private routes and scoped Code Factory routes were verified;
the full Serve configuration hash changed during the session, so complete byte-for-byte
route preservation is not claimed.

See the [version-bound workbench record](evidence/2026-10-22-workbench.json).
Real host subscriptions/callbacks, a soak on this build, desktop logout/login,
full manual accessibility, and the real model's second-attempt/text-fallback matrix
remain open. No full readiness or delivered host push notification claim is made.

## Item work status, message wake and blocking asks · 2026-10-21

Installed **0.1.0-08c15806095a** displays activity only as issue/PR work status. Resume and
All agents are removed from the routine UI. Sending a message restores inactive
retained work using that message as its first continuation turn; the run, Codex
thread, workspace, permissions and encrypted redactions remain attached. Messages
lists/counts only live questions and approvals blocking human input; retained
conversations/drafts are scoped to the item’s Work history.

All 62 browser tests, mapped reconciliation, specification checks and branch CI
passed. Twenty-one fixture Axe scans have no automated violations; incomplete
nodes and manual accessibility remain separate. Cold recovery/concurrent sends,
missing retained resources, failed drafts, secret redaction and dirty-workspace
continuation have targeted assertions. The signed-in installed Work view loaded
the matching release bundle. One new dedicated Plan/read-only/ask pilot run showed
a real native question in Messages, UI interruption/stale exclusion, UI Send
restoring its same thread/workspace, a new question, UI answer and real completion.
Its clean workspace was then removed through the UI, retaining history. Existing
runs, credentials, routes and pinned provider paths were preserved. This bounded
check does not replace real approval variants, Events, login, soak or full manual
accessibility acceptance. See the [message-wake record](evidence/2026-10-21-message-wake.json).

## Concise agent conversation · 2026-10-21

Installed **0.1.0-9313f82ae73f** keeps agent progress/final messages, user replies,
readable errors and attention controls in the normal conversation. Commands,
plans, raw diffs, internal summaries and structured error data move into the
existing collapsed Details disclosure with workspace/cleanup controls. Stored
history, paging/limits, explicit actions and read recovery remain unchanged.

All 58 browser tests, specification checks/reconciliation and branch CI passed.
Twenty-one fixture Axe scans have no automated violations; manual/incomplete
variants remain recorded. The fresh signed-in Work view loaded matching release
JavaScript. An already-read retained pilot conversation showed 3 human-facing
blocks and 4 technical blocks, hidden by default and available after expanding
Details. No retained body/command text was recorded, and no real agent or settings
mutation was started. Existing runs, routes, authentication and provider paths are
preserved. This bounded rendering check does not replace full lifecycle, Events,
login, soak or manual accessibility acceptance. See the
[concise conversation record](evidence/2026-10-21-concise-conversation.json).

## Intermittent connection recovery · 2026-10-21

The [investigation](evidence/2026-10-21-connection-investigation.json) confirms
Connected status alongside a stale McpServerError banner. During the bounded
watch, 263 RPC replies had no new JSON-RPC error while that banner persisted.
Four earlier MCP HTTP 502 responses lack enough old diagnostics to distinguish
client cancellation from upstream connection failure; current public OAuth/MCP
checks passed both ingress peers. No Tailscale reset or route/auth changes made.

Installed **0.1.0-d28d7827a940** separates read feedback from action failures,
retries known transient reads and failed initialization, and preserves drafts and
output. The exact host-wrapped transport exception is recognized without retrying
business errors. Mutations are never automatically replayed. Safe gateway logs
now include a fixed failure category and numeric request duration.

All 56 browser tests, specification checks/reconciliation and branch CI passed.
The fresh signed-in view loaded the exact release bundle. Two controlled,
read-only host-bridge probes (JSON-RPC error and tool-result envelope) recovered
without a document reload or even a temporary raw-framework alert. No real agent
or settings mutation was started. Full host/network uptime, lifecycle, manual
accessibility, Events, login and soak claims remain separate. See the
[recovery record](evidence/2026-10-21-connection-recovery.json).

## Focused work queues and direct chat · 2026-10-21

Build **0.1.0-c0601b8f3508** is installed with healthy services, unchanged scoped
routes/authentication/provider paths, recovery backups and four retained runs.
Issues defaults to open issues without a linked PR, ordered by priority. My PRs
uses the authenticated author; Review PRs uses individual/team review requests.
Messages lists pending requests with their owning context. Item selection opens
chat immediately with its summary and existing conversation or prefilled first
message. Start/Resume stay explicit; changed authority requires review again.
Priority badges are left of the title; identifiers remain original-source links.

All 48 browser tests, specification reconciliation/check and branch CI passed.
Nineteen fixture Axe scans have no automated violations; incomplete/manual
variants remain recorded. Fresh signed-in Work loaded the exact release bundle
and all four tabs; opening an already-read pilot item showed summary and composer
with Item details collapsed. No real agent or configuration was changed by this
bounded smoke. Nonempty review/waiting cases and first-message execution are
fixtures; full host/provider, Events, login, soak and manual acceptance remain
separate. See the [work queues record](evidence/2026-10-21-work-queues.json).

## Repository identifier links · 2026-10-21

Each inbox row now uses its repository/number as the original GitHub issue/PR
link, displayed once in metadata outside the selection button. The extra GitHub
label/icon is removed. Row selection and source navigation remain independent.
Build **0.1.0-87df721c6d6c** is installed with healthy services and preserved
routes, authentication, provider paths and four retained runs. A fresh signed-in
Work Inbox loaded the exact release JavaScript and showed all five pilot
identifiers as source links, with no duplicate GitHub label or errors.

All 39 browser tests, specification reconciliation/check and branch CI passed;
16 fixture Axe scans have no automated violations. Incomplete/manual variants and
broader installed-host acceptance remain open. See the
[identifier verification record](evidence/2026-10-21-identifier-link.json).
The owner's reported connection error disappeared; the
[Tailscale check](evidence/2026-10-21-tailscale-recovery.json) records earlier network
failures, recovery and passing public OAuth/MCP checks without network changes.

## Original links and independent Settings · 2026-10-21

The source adds a small GitHub link on each inbox row for its original issue or
pull request. Normal activation uses the host browser and keeps selection/read
state unchanged. Configuration moves to its own `open_settings` view in chat;
the inbox/agent UI has no Settings entry, and saves stay in the independent view.
Task and configuration retain separate UI/draft state on the existing connection.

Build **0.1.0-3fe6288ffc29** is installed with both services active, unchanged
routes/authentication/provider paths, recovery backups and four retained runs.
All 39 browser tests and branch CI passed. Sixteen fixture Axe scans found no
automated violations; incomplete nodes remain recorded for manual review.
Fresh signed-in Work chats loaded the exact release JavaScript: Inbox has only
Inbox/Agents navigation and original issue/PR links; Settings has no task
navigation and shows five configuration sections. The host acknowledged a pilot
PR link request without an error, and Inbox stayed selected with unchanged row
states. Its destination page was not observed in the pilot browser targets.
No settings or agent runs were changed in this bounded host smoke. Full lifecycle,
Events, login, soak and manual accessibility acceptance were not repeated.

The [verification and rollout record](evidence/2026-10-21-separated-settings.json),
[reconciliation log](evidence/2026-10-21-separated-settings-reconciliation.log) and
[CI log](evidence/2026-10-21-separated-settings-ci.log) retain the executed scope.
Prior host evidence remains tied to its original build.

## Compact single-page UI revision · 2026-10-21

The source now follows the [single-page design](ui/design.md): Inbox and Agents
are full-width lists; Item and Agent are separate focused pages with contextual
Back navigation. Branding, permanent footer, routine sync/Connected chrome and
unused disabled integration forms are removed. Secondary item controls are
compact disclosures. Query/scroll/focus and per-record drafts survive navigation.

The [compact UI verification record](evidence/2026-10-21-compact-ui.json),
[reconciliation](evidence/2026-10-21-compact-ui-reconciliation.log) and
[CI log](evidence/2026-10-21-compact-ui-ci.log) track verification and rollout for
this revision. Build **0.1.0-125bfd2f9079** is installed with healthy services,
matching release bytes, preserved routes/authentication, recovery backups and all
four retained runs. The fresh signed-in Work view loaded the exact release
JavaScript. Inbox contained one list and no detail/header/brand/footer/split view;
opening already-read pilot issue 1 replaced the list with one item. Back restored
its row focus. Agents list opened one interrupted conversation with its own Back
action, and Settings exposed five relevant sections with no credential form.
The final new Inbox view is open. This bounded navigation smoke is separate from
full lifecycle/provider, soak, Events, login and manual accessibility acceptance;
earlier build evidence retains its original scope.

## Installed workflow upgrade · 2026-10-21

The [upgrade evidence](evidence/2026-10-21-workflow-upgrade.json) records installed
**0.1.0-b3c0fdf03dc5**, matching the verified release binary. Both user services
are active, authenticated diagnostics have no failed checks, and the existing
provider paths, owner token, registered app mapping, gateway profile and Tailscale
routes are unchanged. Consistent database and gateway-profile backups are present.
Four retained runs preserve their workspace and provider-thread identities; the
previously idle session is interrupted after restart and requires explicit Resume.

After refreshing the existing app's tool metadata, a fresh signed-in Work
dashboard reused OAuth and loaded JavaScript matching the installed release. It
showed Connected with no alerts, collapsed Sync details, no zero-attention shortcut,
and no former tagline. Settings showed Repositories open, five other sections
closed, four ordinary priority rows and disabled Jira controls. The configured
HTTPS landing page promoted the registered browser package and collapsed portable
artifacts. This is installed read-only dashboard/Settings smoke evidence; the
full core/soak/CSP results below remain tied to the earlier `e945` build. New-build
full acceptance, Events, coordinated login and manual accessibility remain open.

## Local daily-workflow simplification · 2026-10-21

The source revision preserves item/run drafts within the open app, saves priority
selections immediately with rollback feedback, selects the owning run for global
attention, and provides item-level **Open agent**. Default launches enter prepared
review directly; **Start** remains explicit, durable drafts reopen without
repreparation, and uncertain Start responses locate the same run before retry.
Approval choices keep exact response payloads under readable labels. Settings
uses collapsed sections and priority rows; Jira stays disabled. The installation
page promotes the browser artifact only for a valid observed registration.

Executed checks and local build identity are recorded in the
[workflow verification record](evidence/2026-10-21-workflow-simplification.json),
[reconciliation log](evidence/2026-10-21-workflow-reconciliation.log) and
[branch CI log](evidence/2026-10-21-workflow-ci.log).
These are the fixture/build checks from the implementation checkpoint. The later
installed upgrade and bounded Work smoke are recorded above; prior installed-version
evidence and outstanding host, Events, login and manual accessibility checks retain
their original scope.

## Prior installed Work core (`e945`) · 2026-10-21

The [current core evidence](evidence/2026-10-21-final-work-core.json) identifies
**0.1.0-e945f9020696**, its verified binary hash and the content-fingerprinted UI
resource. After Refresh tools and a fresh Work view, the actual iframe has the
owned `#17201c` dark background on body/root/main while host `html` stays
transparent. This confirms the displayed correction that the earlier `229fc8`
upgrade alone could not establish.

| Current installed checkpoint | Observed result |
| --- | --- |
| Explicit new launch | A new private-pilot run used Plan mode, read-only sandbox and publishing `ask`; its UI answer produced `FINAL_LAUNCH_ANSWER_OK Continue` and returned idle. A repeated Start was rejected by the installed MCP proxy. |
| Final-build inbox/triage | [Synthetic issue 5](evidence/2026-10-21-final-work-triage.json) arrived New/unread. Opening marked read but retained New; Mark unread then Acknowledge kept unread without action. Record action appeared immediately in history without consuming unread. |
| Priority and revision | P2 label → P0 override → cleared override/P2; removing P2 exposed Pending/running then real P3 inference with 0.99 confidence. Override P0 superseded inference and clearing restored P3. A later comment showed unread without New; journal running/complete events prove reassessment of the new revision. Its brief Pending UI state was not captured. |
| Local relationships | Adding issue 5→1 displayed the link and cleared the submitted draft; Remove removed its row without reselection. Test link/overrides were removed, P2 restored; final issue 5 was not New, unread, with two action-history records. Synthetic issues 4/5 remain open. |
| Recovery and follow-up | The retained run's question survived Work reload; fresh answer and follow-up markers appeared. Dirty cleanup refused and showed interrupted state while retaining draft/file/provider thread. Explicit Resume preserved the thread; only the synthetic sentinel was removed before clean cleanup, which retained history. |
| Gateway recovery | Gateway-only restart was followed by four authenticated polling responses, no UI alerts and no relogin. |
| Keyboard and contrast | Actual launch/review/attention focus remained contained and attention dismissal restored its opener. Eight actual app Axe scans reported zero automated violations. Six overlap-related contrast nodes require manual review. Light checks changed only the app data-theme attribute and restored the host's dark theme; no full WCAG or screen-reader conformance is claimed. |
| Browser attachment | The approved persistent profile retained its own sign-in; controller reattachment required no new native Allow prompt. |
| Public protocol | [Raw](evidence/2026-10-21-final-public-connection.json) and [SDK](evidence/2026-10-21-final-sdk-connection.json) checks passed both ingress peers, with ephemeral credentials revoked. These probes remain separate from the observed Work interaction. |

**Readiness remains blocked.** Work discovered the event definitions, but a
creation attempt stalled for more than fifteen minutes without a response, error
or subscription ID, then retries were stopped by instruction. Stored subscription
count was zero at `2026-10-21T04:40:55Z`; the cause remains undetermined. No signed
callback, host event delivery, filtering or subscription lifecycle is claimed.
The [interim observations](evidence/2026-10-21-work-live-interim.json) preserve the
earlier cache and Events investigation without turning discovery into success.

The [one-hour connected soak](evidence/2026-10-21-connected-soak.json) is
**complete and passed** for the actual final-build iframe: 04:33:49.700–05:34:57.611Z,
3667.911 seconds on the same document, 178 UI samples (maximum gap 30.0004 seconds),
2304 successful polling responses and 121 distinct sync times, with zero observed
disconnects, alerts, connection transitions or unloads. Independent sampling covered
61.02 minutes with 123 healthy version-matching observations of daemon, gateway and
pilot browser. Normal pilot activity occurred during the interval. This proves the
recorded interval rather than indefinite availability.

The owner-authorized [CSP enable/test/restore](evidence/2026-10-21-work-csp.json)
**passed within its `connect-src` scope**. An unlisted reserved `.invalid` fetch
with no credentials or application data was denied with an enforced policy event
both before and after enabling the global setting. Enabled state persisted after
Settings reload; a fresh Work view had no “CSP off” warning, remained Connected
and produced three bridge responses with no alerts. The global setting was
restored to **off**, verified after Settings reload at 05:37:01Z. The earlier off
label alone did not establish that this app lacked CSP enforcement; that inference
is superseded by this actual policy observation. Other CSP directives were not
probed and are not claimed as complete coverage.

A real desktop logout/login remains **pending owner timing confirmation**. No
logout result is claimed, and the current remote session is preserved. Events,
manual accessibility review, other current rollback and remaining behavior checks
stay open. Frozen local source/fixture summaries remain unchanged historical
checkpoints; current operator evidence above is separate.

## Prior installed browser core · 2026-10-20

On `0.1.0-850eb63cda16`, the agent controlled the owner's signed-in Chrome using owner-approved local
Chrome DevTools auto-connect. Authentication values, headers and browser profiles
were not exported. The actual ChatGPT browser creation, OAuth callback, tools,
embedded UI, installed setup skill and real Codex interaction were observed.
[Versioned evidence](evidence/2026-10-20-work-browser-acceptance.json) and
[complete automated verification](evidence/2026-10-20-final-verification.log).

| Prior-version browser checkpoint | Observed result |
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

At this earlier `850` checkpoint, Events were reported unavailable, zero
subscriptions existed, and soak/login/current-version matrix/CSP checks were
incomplete. Those dated observations remain historical; the current status and
undetermined Events outcome are stated above. The dedicated private fixtures and
useful retained worktrees remain available while acceptance is incomplete.

Use [the operating guide](operations.md) for setup, daily use, diagnostics,
upgrade, compatible-profile rollback and uninstall. The agent can repeat browser
tests through [the browser controller](browser-lab.md).

## User-flow specification baseline · 2026-10-20

[User-flow contracts](user-flows/README.md) now separate behavior descriptions,
implementation references, test assertions and known gaps for 14 product/operator
flows and one contributor flow. Existing product behavior is adopted from initial
source commit `53ff8be`; the new contributor flow implements the reconciliation
gate. Local fixture verification and per-flow reconciliation records are separate
from installed-binary and host acceptance evidence. No installation, service
restart or new live-host acceptance is implied by this source/tooling change.

`CODE_FACTORY_REAL_CODEX=0 make ci FLOW_BASE=main` passed the branch reconciliation
gate, build, vet/types, race tests, three browser fixtures, packaging, installer
recovery, smoke and 20 tooling tests. The [local CI log](evidence/2026-10-20-user-flow-ci.log)
records the initial source baseline and unchanged product package version. All 15
flows have fingerprinted local reconciliation records; 47 scenarios include 19
explicit partial or missing coverage entries.

The [UI entity specification](ui/README.md), added on 2026-10-20, inventories the
current widgets/elements, panes, logical pages, dialogs and auxiliary owned pages.
It includes containment/navigation/state diagrams and separates actual test
assertions from missing coverage. It was derived from source and checked as
documentation; no additional rendered or live-host result is claimed.

The existing readiness blockers above remain open. In particular, cataloged
fixture tests and an explicit test reference do not establish complete scenario
coverage; individual flow documents identify partial or missing assertions.

## Layered specification adoption · 2026-10-20

The [maintained specification system](specs/README.md) now owns 379 canonical
terms, 109 existing UI entity IDs, 42 displayed widget instances and structured
Mermaid graphs for all 15 flows. Generated tables, glossary entries, containment
and relationship diagrams are reproducible. UI-to-behavior mappings now live in
the behavior catalog; validation reports zero forbidden foundation dependencies.
The original 14-product-flow adoption list/base remain unchanged at `53ff8be`.

`CODE_FACTORY_REAL_CODEX=0 make ci FLOW_BASE=main` passed the unified gate, build,
vet/types, race/native-browser/protocol tests, three browser fixtures, packaging,
installer recovery and compiled smoke. All 38 tooling tests passed (20 existing
flow tests and 18 unified specification tests), including isolated Git snapshots,
source/coverage metadata freshness and failed-rerun invalidation. The four project
skills passed Skill Creator validation; all five standalone TOML agents validated.
A separate check verified 747 local documentation links/anchors with zero errors.
[Full local CI](evidence/2026-10-20-specs-ci.log) and
[required reconciliation verification](evidence/2026-10-20-specs-reconciliation.log)
record the successful commands. `make specs-ci` and compatibility flow commands
also pass against the current working tree.

The unified gate reads 17 fresh fingerprinted review records under
`docs/specs/reconciliations/`: glossary, UI and each flow. Historical flow-only
records remain retained separately. The generated package remains
`0.1.0-850eb63cda16` with plugin `0.2.2`; no product implementation, installed
binary, authentication, live service or browser session changed. The new agent
definitions are project instructions; no agents run in CI and no model credentials
are needed by the gate.

Existing real-host, callback, one-hour soak, login-cycle, current-version behavior
matrix, CSP and accessibility gaps stay explicit. This is source/tooling and
fixture evidence, not additional live-host acceptance or full pilot readiness.

## Review hardening and fixture evidence · 2026-10-20

The maintained registries now contain **386 terms, 109 UI entities and 15 flows**.
The original fourteen-product-flow adoption list and `53ff8be` baseline remain
pinned. Review fixes enforce behavior cochanges per directly changed UI contract,
retain authored foundation text outside owned generated blocks, normalize forbidden
backlinks and choose comparison policy from both intended Git snapshots. Current
verification commands execute related replacement suites; historical directory
selectors still cover newly added files in either comparison snapshot. Ordinary
Go checks use `-count=1` to exercise current subprocess-built provider helpers.

The [final branch CI](evidence/2026-10-20-specs-final-ci.log) and
[required reconciliation suites](evidence/2026-10-20-specs-final-reconciliation.log)
passed with fixture mode forced: **86 specification tests, 20 legacy flow tests
and three browser fixtures**, uncached Go race tests, vet/types, packaging,
installer recovery and compiled smoke. `make specs-ci` and the legacy Make/CLI
aliases also [passed](evidence/2026-10-20-specs-final-aliases.log). All seventeen
review records are fresh; twenty-two generated documents remain unchanged on
render. Four skills, five standalone TOML agents and local documentation links
validated. The [verification summary](evidence/2026-10-20-specs-final-summary.json)
records source hashes, record fingerprints, commands and the retained limits.
The assembled package remains `0.1.0-850eb63cda16` with plugin `0.2.2`.

The strengthened controlled-host browser fixture reloads its host and iframe
while a question is pending, reopens the original run and choices, answers the
original request once and follows up without another launch. Its retained state
lives in the Node fixture; this does not establish daemon restart, failed-poll
recovery or a real-host/tunnel round trip. A final independent targeted review of
historical/current selector handling reported zero findings after mutation and
isolated Git/CLI checks. Earlier intermediate passing logs remain preserved with
their pre-correction scope; they did not expose that historical-input defect.

No installed runtime, authentication or owner browser session changed. Real
provider checks remained disabled; the installed-provider handshake and real-model
lifecycle tests are opt-in. Offline ingress-negative regressions intentionally
print simulated failure messages while their assertions pass; no deployed public
probe was run. Required real-host callbacks, accessibility, one-hour soak,
logout/login and the other existing acceptance gaps remain open.

## Render, schema and verifier closure · 2026-10-20

This supplements the earlier fixture checkpoint above. The
[required reconciliation](evidence/2026-10-20-specs-round9-reconciliation.log) and
[full branch CI](evidence/2026-10-20-specs-round9-ci.log) passed with fixture mode
forced: **119 specification tests, 20 legacy flow tests and three browser
fixtures**, uncached Go race tests, vet/types, packaging, installer recovery and
compiled smoke. Fast CI and the legacy Make/CLI aliases also
[passed](evidence/2026-10-20-specs-round9-aliases.log). All seventeen records are
fresh and twenty-two generated documents remain byte-identical on render; the
[final freshness check](evidence/2026-10-20-specs-round9-freshness.log) verifies
those results. The registry remains **386 terms, 109 UI entities and 15 flows**;
the original fourteen-product-flow adoption list and `53ff8be` baseline are
unchanged. Four skills, five standalone TOML agents and local documentation
links validated. The [current summary](evidence/2026-10-20-specs-round9-summary.json)
records source hashes, record fingerprints, commands and retained limits.

The thirty-three new assertion mappings cover safe render preflight and complete
candidate validation, UI element/connection schema enforcement, owned verifier
cleanup, stale owned-body repair and cancellation received during timeout
cleanup. Their actual assertions map to the fail-closed contributor scenario;
committed/index comparison coverage remains in its existing dedicated tests.
Obsolete owned output can be repaired while authored references, current
registries and the generated candidate remain checked. Unknown marker content
outside registered owned bodies remains authored input.

A final independent targeted review reported zero findings for encoded and
nested obsolete owned render content, authored-byte preservation, rejection of
unowned/wrong-path references and real SIGINT/SIGTERM during cleanup. It reported
24 targeted tests passing; this scoped review does not establish the absence of
every possible defect. The verifier cleans its original owned process group on
success, failure, timeout and cancellation; deliberately detached descendants
remain outside that guarantee. Child-subreaper state is confined to isolated
short-lived test probes and is not changed by production verification.

The assembled package remains `0.1.0-850eb63cda16` with plugin `0.2.2`.
No installed runtime, authentication or owner browser session changed. Real
provider checks, the installed-provider handshake and deployed public probes
were not run. Offline ingress-negative regressions intentionally print simulated
failure messages while their assertions pass. Real-host callback lifecycle,
accessibility acceptance, one-hour soak, coordinated logout/login and the other
existing acceptance gaps remain open; full pilot readiness remains blocked.

## JSON declarations and evidence validation · 2026-10-20

This supplements the retained round9 checkpoint. The JSON
[full branch CI](evidence/2026-10-20-specs-round11-ci.log) passed with fixture mode
forced: **126 specification tests, 20 legacy flow tests (146 tooling tests) and
three browser fixtures**, uncached Go race tests, vet/types, packaging, installer
recovery and compiled smoke. That full-CI snapshot preceded the documentation-only
settings corrections below; checker, test and runtime sources remained unchanged.
The [JSON checkpoint summary](evidence/2026-10-20-specs-round11-json-checkpoint-summary.json)
retains its original source and record hashes.

After those corrections, fresh
[required reconciliation](evidence/2026-10-20-specs-round11-settings-reconciliation.log)
reran every declared fixture suite and regenerated all seventeen records. Final
fast CI and compatibility Make/CLI aliases
[passed](evidence/2026-10-20-specs-round11-settings-aliases.log), and the
[final freshness check](evidence/2026-10-20-specs-round11-settings-freshness.log)
confirms fresh records and twenty-two byte-identical generated documents on render.
Four skills, five standalone TOML agents and local documentation links validated.
The registry remains **386 terms, 109 UI entities and 15 flows**; the original
fourteen-product adoption list and `53ff8be` baseline are unchanged.

Seven new assertion mappings belong only to contributor scenario S3. They reject
duplicate JSON members, including nested or escaped-equivalent names, across
registries, supporting foundation JSON, reviews and receipts. Declared
collections require arrays and compatible item types: false-valued object keys
and strings cannot become membership or coverage. Actual invalid render/check
probes preserve every repository byte; ambiguous reviews fail before verification
or prior-record removal. Valid current arrays and repeated names in separate
objects remain accepted.

The settings corrections describe existing behavior: saving a skill checks its
name and required opening header, without establishing complete content validity;
full validation and focused invalid-input assertions remain gaps. The relationship
form submits target information without an optional evidence input; its backend
unlink assertion does not establish form interaction coverage. Independent targeted
JSON and documentation reviews each reported zero findings. The
[round11 summary](evidence/2026-10-20-specs-round11-summary.json) distinguishes
prior full CI from final documentation verification and records current hashes,
record fingerprints, exact commands and review limits.

The compiled package remains **0.1.0-850eb63cda16**, with plugin **0.2.2**.
No installed runtime, credentials, authentication or owner browser session changed.
Real-provider checks stayed disabled; no deployed public OAuth/MCP probe or new
live-host acceptance ran. Offline ingress-negative regressions print simulated
failure messages while their assertions pass. Real-host subscription/callbacks,
accessibility, one-hour soak, coordinated logout/login and the other existing
acceptance gaps remain open.

## Reference, maintenance and snapshot boundaries · 2026-10-20

Fresh [required reconciliation](evidence/2026-10-20-specs-round12-reconciliation.log)
and one final [full branch CI](evidence/2026-10-20-specs-round12-ci.log) passed for
the frozen round12 source and documentation with fixture mode forced:
**145 specification tests, 20 legacy flow tests (165 tooling tests) and three
browser fixtures**, uncached Go race tests, vet/types, packaging, installer
recovery and compiled smoke. Fast CI and compatibility Make/CLI aliases also
[passed](evidence/2026-10-20-specs-round12-aliases.log). The
[final freshness check](evidence/2026-10-20-specs-round12-freshness.log) confirms
seventeen fresh tool-generated records and twenty-two byte-identical generated
documents on render. Four skills, five standalone TOML agents and local
links validated. The registry remains **386 terms, 109 UI entities and 15 flows**;
the original fourteen-product adoption list and `53ff8be` baseline are unchanged.

Nineteen new method mappings distinguish actual failure and selection assertions.
Seven Markdown tests cover escaped foundation paths and resolved shortcut labels
inside supported nested quote/list continuations, with ordinary text and relative
indented-code controls. Seven maintenance tests enforce required role ownership
and constrained skill descriptions while allowing unrelated agent settings. Five
Git/CLI snapshot tests cover owner-execute canonical identity, mode-only impact,
independent worktree/index/committed selection, embedded repository rejection,
ordinary untracked/ignored scope and unsupported tracked gitlinks. Only methods
that exercise selections carry the corresponding snapshot scenario mapping.

Source-fidelity corrections describe bounded notification retry/drop/disable
behavior and disabled-event skipping without replay, the fixed five-minute delay
for unchanged failed assessments, and separate item-detail retention after shared
snapshot refresh. Add explicitly rereads relationship detail; Remove and item
actions can leave displayed rows or history stale until a later detail read or
remount. Focused assertions for these notification classes, retry timing and UI
refresh distinctions remain gaps. No product behavior was redesigned.

Independent source and final integration reviews reported zero findings within
their recorded scope. The [round12 summary](evidence/2026-10-20-specs-round12-summary.json)
records source hashes, record fingerprints, commands, precise assertion mappings
and retained limits. Earlier round9 and round11 evidence remains preserved with
its original source scope.

The compiled package remains **0.1.0-850eb63cda16**, with plugin **0.2.2**.
No installed runtime, authentication, credentials or owner browser session changed.
Real-provider checks stayed disabled. No deployed public OAuth/MCP probe or new
live-host acceptance ran; simulated failure output in offline ingress-negative
tests is expected. Real-host subscriptions/callbacks, accessibility, one-hour soak,
coordinated logout/login and other existing acceptance gaps remain open.

## Registry ownership and interrupted-upgrade recovery · 2026-10-20

Fresh [required reconciliation](evidence/2026-10-20-specs-round13-reconciliation.log)
and one final [full branch CI](evidence/2026-10-20-specs-round13-ci.log) passed for
the frozen round13 source and documentation with fixture mode forced:
**160 specification tests, 20 legacy flow tests (180 tooling tests), three browser
fixtures and six installer tests**, plus uncached Go race tests, vet/types,
packaging and compiled smoke. Required fast CI and compatibility aliases
[passed](evidence/2026-10-20-specs-round13-aliases.log). The
[final freshness check](evidence/2026-10-20-specs-round13-freshness.log) confirms
seventeen fresh tool-generated records and twenty-two byte-identical generated
documents on render. Four skills, five standalone TOML agents and local links
validated. The registry remains **386 terms, 109 UI entities and 15 flows**;
the original fourteen-product adoption list and `53ff8be` baseline are unchanged.

Fifteen new contributor mappings cover NFC-aware scoped names and aliases,
entity-owned assertion coverage, displayed-instance field invariants and exact
integer schema/receipt/success values. Historical ownerless inventory cannot
silently disappear from review: existing interface subjects and consumers, or
existing flows when no interface entities remain, retain historical definitions
and input freshness without granting those tests behavioral ownership. Ordinary
owned assertions retain precise consumer impact. Actual legacy/unified receipt
CLI checks reject boolean and floating-point coercions without rewriting evidence.

The six installer tests comprise five process-fixture methods and one inert
supervisor method with timeout, caller SIGINT, caller SIGTERM and success modes.
The new recovery assertion hard-kills an isolated upgrade at one service-restart
gate after release activation and before installation-record commit. Fresh
rollback must use retained pending state; it restores the pre-upgrade database
and old-version health, archives a post-kill mutation, preserves the fixture token
and clears pending state. Private probe supervision reaps owned detached fixture
children; this does not broaden the production verifier's process-group guarantee.
Other interruption stages, power-loss/storage durability and real-user-service
recovery remain unverified.

Source-fidelity corrections describe rejected repeated Start requests, interruption
before dirty-cleanup refusal, and the reusable Jira password input separately from
temporary tunnel-form expiry/single-use checks. Their dedicated missing assertions
remain explicit. Independent bounded closure reviews reported zero findings.
The [round13 summary](evidence/2026-10-20-specs-round13-summary.json) records current
hashes, fingerprints, commands, actual assertion scope and retained limits; earlier
evidence remains preserved with its original source scope.

The compiled package remains **0.1.0-850eb63cda16**, with plugin **0.2.2**.
No installed runtime, authentication, credentials or owner browser session changed.
Real-provider checks stayed disabled. No deployed public OAuth/MCP probe or new
live-host acceptance ran; simulated failure output in offline ingress-negative
tests is expected. Real-host subscriptions/callbacks, accessibility, one-hour soak,
coordinated logout/login and other existing acceptance gaps remain open.

## Modular specifications and verified UI behavior · 2026-10-20

The [final required reconciliation](evidence/2026-10-20-specs-features-final-reconciliation.log)
and one [full local branch CI](evidence/2026-10-20-specs-features-ci.log) passed for
the frozen feature source with fixture mode forced: **201 specification tests,
20 legacy tests (221 tooling tests), thirteen browser cases, six installer tests
and seven pure controller-option tests**, plus uncached Go race tests, vet/types,
packaging and compiled smoke. Required fast/legacy checks and the new read-only
Make report/gap views [passed](evidence/2026-10-20-specs-features-aliases.log).
The [freshness check](evidence/2026-10-20-specs-features-freshness.log) confirms
seventeen fresh tool-generated records and twenty-two byte-identical generated
documents. Four skills, five scoped TOML agents and local links validated.

The registries now contain **391 terms, 109 UI entities and fifteen flows**.
Five new meaning-only terms describe gaps, their types, keyboard focus and modal
dialogs. UI/behavior registries use version 2, with **163 retained stable gap IDs**:
seven known limitations and 156 unverified behaviors, each with severity, layer
owner and next verification. Covered portions were narrowed without inventing
closed host acceptance. Version-1 historical reasons remain supported. The
fourteen-product adoption list/base is unchanged. The UI receipt explicitly mixes
29 behavior decisions with 80 eligible baseline-adoption decisions; changed product
flows receive behavior reviews with related source/tests.

Compact reports retain actual old/current anchors, deduplicated reasons and exact
next argv; typed gap inventory remains separate from successful verification.
Focused modules preserve the old public/CLI interfaces and are fingerprint inputs.
New browser assertions cover immediate action-history and relationship refresh,
independent drafts/indicators, obsolete-selection protection, modal keyboard focus,
local launch errors, one accepted run after a lost Start response and explicit
same-run/thread Resume after dirty cleanup refusal.

Fourteen owned-iframe Axe 4.14.0 scans across seven core states in light/dark themes
reported **zero automated A/AA-tag violations**. The
[scoped scan log](evidence/2026-10-20-specs-features-axe.log) and sanitized reports
listed in the [feature summary](evidence/2026-10-20-specs-features-summary.json)
retain **six manual-review incomplete Close-icon nodes**. A contrast fix raised
selected muted metadata from the observed 4.18 to 4.77 ratio. Automated scans and
keyboard fixtures do not establish full WCAG, screen-reader, every state or
installed-host accessibility acceptance.

The locally compiled package is **0.1.0-06d6ed8a13f0**, plugin **0.2.2**; this is
build/fixture evidence, not a claim that this binary has been installed or observed
in Work. The root operator owns any later upgrade and live acceptance. Approved
persistent pilot-browser setup is recorded separately in
[pilot setup evidence](evidence/2026-10-20-pilot-browser-setup.json); debugger or
controller readiness alone does not prove sign-in, attachment or app acceptance.
The local verification runner performed no owner-browser inspection, deployed
OAuth/MCP probe, real-provider call, installed service change, soak or login cycle.
Real-host callbacks, manual/assistive-technology acceptance, one-hour soak,
coordinated logout/login and the other existing readiness gaps remain open.

## Installed feature build and public transport preflight · 2026-10-20

The operator [upgraded the installed release](evidence/2026-10-20-pilot-upgrade.json)
from **0.1.0-850eb63cda16** to **0.1.0-06d6ed8a13f0** using the extracted new binary.
All 156 release-manifest entries and the binary checksum were verified; upgrade
and doctor returned 0, and daemon/gateway remained active. The known idle pilot
run was retained as interrupted after restart, requiring explicit Resume. Existing
unrelated Tailscale routes were preserved. This records installation and process
health, not a new Work/browser recovery observation.

The [raw public OAuth/MCP check](evidence/2026-10-20-upgraded-public-connection.json)
and [independent SDK check](evidence/2026-10-20-upgraded-sdk-connection.json) each
passed against both public ingress addresses. The SDK verified each actual peer,
OAuth discovery/registration/PKCE with owner consent, transport negotiation,
24 tools, `open_factory` and the embedded resource. Ephemeral credentials were
revoked. These are deployed protocol checks from this machine, not signed-in
ChatGPT acceptance or a connected soak.

Initial ChatGPT sign-in in the approved persistent pilot profile is still pending
owner confirmation. Browser inspection remains paused during that sign-in. No
updated-version Work interaction, Events/callback delivery, assistive-technology
acceptance, one-hour soak or coordinated logout/login result is claimed. The
[frozen local feature summary](evidence/2026-10-20-specs-features-summary.json)
retains its earlier source/fixture checkpoint; it has not been rewritten to claim
these later operator results. Full readiness remains blocked by the outstanding
checks in this ledger.

## Host-transparent document contrast correction · 2026-10-21

Actual signed-in Work inspection on **0.1.0-06d6ed8a13f0** exposed a serious
contrast defect in the dark review-launch state: the host injected a later
transparent `html` background, while the application foreground remained dark-theme
colored over a white host canvas. The
[before observation](evidence/2026-10-21-live-contrast-before.json) supersedes any
inference that earlier opaque-host fixture scans covered this host condition.

The source correction paints the owned body, root and main surfaces with the
active theme background and makes the body cover the viewport, preserving the
host's transparent document rule. Existing light/dark browser cases now force the
observed transparency, assert that `html` remains transparent and all three owned
surfaces have the expected computed colors, then run the same seven-state scans.
The author negative control without owned paint fails the dark body assertion.

Fresh [required reconciliation](evidence/2026-10-21-specs-live-background-reconciliation.log)
and one [full local CI](evidence/2026-10-21-specs-live-background-ci.log) passed:
201 specification tests, 20 legacy tests, 13 browser cases and six installer tests,
plus existing build, race/type/vet, packaging and smoke checks. The
[freshness check](evidence/2026-10-21-specs-live-background-freshness.log) confirms
seventeen fresh records and twenty-two unchanged rendered documents. Fourteen
fresh [scoped fixture scans](evidence/2026-10-21-specs-live-background-axe.log)
have zero automated violations and six manual-review incomplete Close-icon nodes.

The new local package is **0.1.0-229fc8debabc**, plugin **0.2.2**. The
[local correction summary](evidence/2026-10-21-specs-live-background-summary.json)
records source/binary hashes, assertions and limits. Installation and actual-host
confirmation of this new build remain root-operator stages; no correction closure,
full accessibility conformance, Events, soak or login result is claimed here.
Prior feature evidence remains unchanged with its original source/host scope.

## Content-specific UI identity for host refresh · 2026-10-21

The background-correction upgrade to **0.1.0-229fc8debabc** completed and remained
healthy in [operator evidence](evidence/2026-10-21-live-background-upgrade.json),
but subsequent Work views still retained the original static resource identity.
Correct local resource bytes were not sufficient evidence that the host rendered
the updated application. Updated actual-host contrast confirmation remained open.

The transport now advertises a content-specific URI derived from the full embedded
HTML bundle SHA256. Every tool resource reference and output template uses that
same current identity. Identical content stays stable; changed bundled HTML, JS
or CSS changes its identity. The original exact static URI remains a read-only,
unadvertised compatibility alias. Unknown or previous fingerprint URIs and query
variants are rejected instead of returning current bytes under arbitrary names;
CSP connection/resource domains remain empty.

Fresh [required reconciliation](evidence/2026-10-21-specs-ui-resource-reconciliation.log)
and one [full local CI](evidence/2026-10-21-specs-ui-resource-ci.log) passed for
**0.1.0-e945f9020696**. The actual in-memory MCP SDK regression checks stable and
revised identities, exact advertisement/tool/template/body agreement, compatibility
and rejected variants; the authenticated transport fixture checks every real tool.
The existing 201 specification, 20 legacy, 13 browser and six installer cases passed,
along with build/race/type/vet/package/smoke checks. The
[freshness result](evidence/2026-10-21-specs-ui-resource-freshness.log) confirms
seventeen fresh records and twenty-two unchanged generated documents.

The [local resource-identity summary](evidence/2026-10-21-specs-ui-resource-summary.json)
records current hashes, transport assertion scope and limits. Installation,
metadata refresh and actual Work retrieval/rendering of this new identity remain
root-operator stages. No host-cache, corrected-contrast, Events, soak, login or
full-readiness closure is inferred from these local checks. Historical background
and feature summaries retain their original source/host scopes.

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
