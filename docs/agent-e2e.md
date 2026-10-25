# Continue development through the real Chrome session

## Durable follow-up receipt · 2026-10-25

`send_message` now saves an encrypted message and returns `delivery: accepted`,
a message ID and queued delivery state before contacting its agent. A worker
restores the same retained session from saved context without a GitHub preflight;
task-required GitHub reads belong to the agent. Tracking, run detail and the
conversation expose later delivery or uncertainty. Queued messages survive a
restart; an interrupted delivery claim is never replayed automatically or replaced
with a generic continuation. Failed acceptance retains the draft; accepted text
stays saved even when later restoration fails.

The message queue race fixtures cover a blocked agent, caller cancellation,
concurrent pending submissions, same-thread recovery with zero GitHub requests,
stale targets and interrupted delivery. Browser fixtures distinguish saved receipt
from activation and replace queue feedback with uncertainty without replay. These
fixture results do not establish installed ChatGPT acceptance for this correction.


## Fast saved-state tracking installed · 2026-10-25

Installed **0.1.0-fd9095162bd6** with plugin **0.2.7**. Routine conversational
lookups use `track_work` with a run ID, canonical item ID, GitHub URL or repository
shorthand. Saved item/run/question/progress/receipt state returns immediately;
stale or missing GitHub observations refresh in the background with timestamps,
errors and retry feedback. Fresh host markers preserve monitoring pauses, budgets
and cooldowns. Missing evidence stays unknown. History, questions and receipts
have independent bounded pages; full detail and transcripts remain available.

`open_factory` explicitly opens the dashboard and returns a compact overview.
The app loads its own state through the MCP bridge. Rendering metadata belongs
only to the dashboard/settings openers, so conversational status reads do not
create a widget. Successful starts, answers and messages return local acceptance
or delivery plus pending attention; completion is established separately.

The [version-bound evidence](evidence/2026-10-24-fast-reads-tracking.json) separates
synthetic server/caller timings, installed loopback timings and real Work calls.
Installed cached caller p95 was **97 ms / 4.7 KiB** for tracking and **37 ms** for
item detail. Dashboard output fell from **4.89 MB to 465 bytes**; the earlier
installed detail read took **8.7 seconds** when refreshing. These measurements
exclude ChatGPT orchestration and WAN latency. Synthetic status p95 was 22.5 ms.
The original parallel group membership was unavailable; the evidence labels its
reproducible dashboard/detail/all-six benchmark groups explicitly.

Fresh Work conversations and a names-only loopback audit observed these normal
Code Factory model-visible sequences:

| Task | Calls |
|---|---|
| Saved review/agent lookup and publication receipts | `track_work` |
| First queue page | `track_work` |
| Configured launch acceptance | `prepare_pr_review` → `start_run` |
| Question discovery and local answer delivery | `list_attention` → `answer_request` |
| Retained-thread follow-up delivery | `track_work` → `send_message` |
| Explicit dashboard | `open_factory` |

Completion reads are separate from local-delivery confirmation. Earlier observed
extra verification reads and an automatically rendered widget are retained in the
evidence alongside the corrected catalog/guidance and successful repeats.

The private **Code Factory Tracking E2E 0.2.7** package uses the existing observed
registered connection. The host exposes no in-place update for the older private
packages, so they remain installed. The native pilot used the existing question-only
Plan/read-only profile; temporary private-repository defaults were restored.
All **33 original runs**, threads, workspaces, profiles and pending-request metadata
remain intact, with two explicit test runs added and left idle. Runtime/provider
selection, protected files and complete Tailscale routes matched upgrade preflight.

Actual Work delivered the native Continue choice. A later follow-up restored the
same retained thread after the daemon upgrade. The installed widget loaded the exact
source JavaScript hash, retained the same unanswered request after a full page
reload, delivered its UI answer once and displayed `TRACKING_UI_ANSWER_OK Continue`.
No code review or publication was performed by these bounded question tests.

Broader host ambiguity/pagination/changed-head/failure variants retain explicit
gaps. Callbacks, current-build soak/login and broader manual accessibility acceptance
remain open; these successful normal paths do not establish full readiness.

## Source-aware continuation installed and observed · 2026-10-24

Installed **0.1.0-8a8a9e76654d** from source commit `6bf7f98`, after the complete
fixture/specification, 87-browser, Go race, static, packaging, installer recovery
and smoke checks passed. Both services are healthy. All 32 run identities, threads,
workspaces and profiles were preserved, as were runtime/provider configuration,
authentication files, global Codex configuration and complete Tailscale routes.
The [rollout evidence](evidence/2026-10-24-source-aware-continuation.json) records
the checksummed release and nonsecret observations.

The owner-requested legacy **python-driver #1128** run continued in the same native
thread. Its installed source check verified the unchanged open head; the agent
confirmed unchanged description, base, Decisions and rules, reused Matt Pocock's
completed zero-finding checkpoint, and completed only the interrupted AD pass.
Both reviewers and independent verification found no actionable defect. The
[verified COMMENT receipt](https://github.com/scylladb/python-driver/pull/1128#pullrequestreview-5640926437)
targets that same head with zero inline comments. The session finished idle with
no pending request. Retained focused checks were 486 passed and 33 skipped;
integration, full-suite, docs and native-extension checks were not rerun.

The existing **#725** scope request remains unanswered, with its P2 batch-race
evidence and 30 passing focused tests retained. It has not published a review.
This was an explicit continuation of an old run lacking startup-pending metadata;
it proves installed same-thread continuation and checkpoint reuse. Actual automatic
pending-run startup scheduling and real closed-source termination remain separate
from the passing fixtures; no production item was closed for testing. Existing
host callback, accessibility, soak and login gaps remain open.

## Source-aware continuation and terminal lifecycle · 2026-10-24

Before initial execution or restoration of a retained session, a targeted GitHub
read verifies its source state without trusting cached lifecycle data. Structured
previous/current context identifies title, description, head, draft, update,
labels, assignment and review-request changes for the agent to assess. Unknown
legacy baselines remain explicit. Saved checkpoints are reused only if still valid.
Failed source reads leave work stopped. Closed issues and closed/merged PRs
permanently terminate their started sessions, kill their provider process groups,
retire pending questions and preserve history and workspaces. Synced closure also
stops active work. Dirty cleanup cannot make a terminated session resumable.

Source fixtures cover these boundaries, fresh-cache revalidation, same-thread
context delivery and changed-head validity, with Stopped/disabled-send/cleanup
browser assertions in both themes. Deployment and real-provider observations are
recorded separately after installation; fixture success does not establish them.


## Automatic continuation after daemon startup · 2026-10-24

The source now distinguishes unfinished sessions from completed idle work and
explicit user interrupts. Startup continues pending work through its retained
thread, workspace, authority, saved artifacts and workflow/reviewer checkpoints.
It checks retained turn completion before adding a continuation, leaves unanswered
questions pending and expires lost native approvals without replaying their answers.
An unavailable workspace fails visibly without creating a replacement task.
Prepared drafts remain unstarted; already accepted work without a provider thread
starts once, including when workspace creation had already completed.

This change is local source with fixture verification. The installed daemon and
historical review runs have not been changed by this work. Real-provider checkpoint
reuse across an installed-daemon restart remains an acceptance gap. Check unrelated
live work before deploying or restarting any service.

## Conversational question queue · 2026-10-24

Plugin 0.2.6 exposes `list_attention` to both the model and app. It lists the
answerable Messages requests with compact owning run/item/profile context and
the original question IDs/options. Read pages with `limit` (default 25, maximum
100), optional `runId`, and `cursor` from `nextCursor`. Use a new `freshness`
marker for each fresh read and omit the cursor to refresh from the start.
Listing is local and read only; it neither starts work nor answers anything.

For ordinary questions, `answer_request` takes the attention `request.id`, not
the run ID, and `answer: {answers: {questionId: {answers: [userAnswer]}}}`.
The daily skill presents the actual choices, waits for the user's response,
sends once, and reads a fresh queue plus owning run to verify continuation.
Keep secret fields in the embedded form. After uncertain delivery, inspect
current state rather than replaying. The existing Messages UI, native waiting,
retained question recovery and original once-only delivery remain in use.

The [queue evidence](evidence/2026-10-24-attention-queue.json) separates fixture,
installed-version and actual ChatGPT results. On-demand queue discovery is not
evidence of background `attention.required` callbacks. Actual Work presented the
real pilot question, delivered Continue, removed the request and resumed the same
native agent. Host activity did not identify the selected RPC method; dedicated
`list_attention` attribution and multi-agent host cases remain explicit gaps.
The retained pilot profile and manual initial-sign-in requirement are below.

## Posted review visibility · 2026-10-23

The PR conversation includes a visible **Posted reviews** section with actual
GitHub receipt links, author/verdict, reviewed commit, submission time, body and
inline comments. Pending drafts are excluded. Current and earlier commits are
distinct. Failed reads retain the last successful observation with explicit error
feedback; a completed run never establishes publication by itself.

Plugin 0.2.5 documents the model-visible `read_pr_reviews` tool and saved
`read_run.postedReviews` observations. Reviews include all authors and are not
attributed to a particular run. PR turns refresh receipts in the background;
routine observations share a one-minute cache, the existing request budget and
pause policy. Source fixtures cover receipt/comment pagination, draft exclusion,
post-turn refresh, failure retention and both narrow themes with scoped Axe.
Installed-version and real-host checks are recorded separately in the
[visibility evidence](evidence/2026-10-23-review-publication-visibility.json).

## Owner dependency API and fork upgrade · 2026-10-23

Installed **0.1.0-7e6b16d6fee1** exposes the [private dependency administration API](dependency-admin.md)
on the loopback daemon, outside public OAuth/MCP and iframe access. Owner request
`owner-fork-dependency-upgrade-20261023` succeeded across 28 result records.
Codex now runs **dkropachev/codex 0.163.0**; the installed binary matches the
independently checksum-verified fork release. Startup, existing authentication,
skills, config and plugin checks passed. Stock upstream 0.164.0 was rejected
without activation before the owner clarified the required fork. The final updater
uses only `dkropachev/codex` and tests reject stock upstream assets.

All nine installed plugins were refreshed. Caveman's renamed marketplace was
repaired through native management, with Caveman 4.0.1 and Sites 1.1.2 observed.
DK's local compatibility 1.1.18, AD 1.0.7 and Matt Pocock 1.0.0 remain selected.
Fourteen legacy workflow/reviewer skill directories were archived with private
backups and replaced in configured profiles by native plugin skills. Untracked
local skills and externally managed OS tools remain explicit and preserved.
The API accepts source registration for standalone GitHub skill updates.

Fresh #1134 run `d047d5a187b9598fb02a26eeabd9210a` is running with native CLI
0.163.0 and both native DK review/post skills; no approval is pending. Publication
is authorized, but review completion/publication are not yet established by this
startup observation. [Evidence](evidence/2026-10-23-dependency-admin.json) separates
live upgrade/startup from the passing 83 browser fixtures, Go race/static,
packaging/installer and reviewed specification checks. General real-host,
accessibility, callback, soak and login acceptance gaps remain open.

## Owner review-and-publish instructions · 2026-10-23

The live standard `review` profile now explicitly invokes
`$dk-workflows:dk-pr-review`, independently validates every candidate finding and
its severity, then invokes `$dk-pr-post-review` with publication authorized.
Both selected skills are retained in new launch snapshots. The exact
[configured prompt](e2e/pr-review-instructions.md) includes current-head and
scope/Decisions checks, incomplete-review handling, duplicate prevention and a
verified publication receipt. Dedicated pilot profiles keep their campaign text.

The owner explicitly requested a new review of `scylladb/python-driver#1134`.
Fresh run `f9c68a8d260db610bd18dc9e81777c32` was prepared with both skills and
started on remote head `be3ab558f3ae5d149ea6615e12221300e023eabf`; the earlier run
is preserved. [Configuration/start evidence](evidence/2026-10-23-pr-review-instructions.json)
records exact saved-profile readback and retained unrelated settings. An accepted
start is not evidence of review completion or GitHub publication.

Installed **0.1.0-cb5f1c36fd04** fixes a launch regression: explicit full access
returned before seeding existing owner reviewer rules and Decisions into private
data, causing an empty native reviewer configuration. Both execution modes now
initialize those stores before selecting authority; continuation retains run-local
updates and leaves owner stores unchanged. The focused regression, 83 browser
fixtures, Go race/static, packaging, installer and specification checks passed.
Only #1134 was active; it was interrupted for the upgrade and resumed in the same
thread with its existing artifacts, both reviewers and publication authorization.
Both services are active. Review completion/publication remain pending.

## Owner-authorized autonomous access · 2026-10-23

The owner explicitly replaced the earlier pilot read-only/ask policy with full
AI access. All six stored profiles now use `danger-full-access`, command approval
`never`, and publishing `authorized`. An explicit recorded maintenance grant applies
the same authority to 17 retained, uncleaned runs while preserving their history,
workspaces, models and skills; only the previously blocked review was resumed.
Routine execution/network/publishing permission prompts are disabled. Agents ask
only for material decisions or missing information they cannot reasonably resolve.
Automatic-review intake was not enabled by this permission change.

Installed **0.1.0-996a39e0e1e1** also includes the cached inbox repair. Actual deployed
review-list reads returned 11 PRs in **79–84 ms**, compared with 1.3–1.4 seconds.
The original review resumed in its existing thread, with no pending command
approval, and its GitHub/Decisions read completed successfully. Earlier read-only
campaign evidence remains historical evidence of the policy used at that time.
See [the authority record](evidence/2026-10-23-owner-autonomy.json).

## Cached inbox latency repair · 2026-10-23

A live cached review-queue lookup returned 11 PRs in 1.402 seconds. Profiling a
synthetic copy of the record counts identified repeated relationship decoding,
per-item state queries and retained history/skill bundles on the read path.
[Latency evidence](evidence/2026-10-23-inbox-latency.json) records a 966 ms baseline
with 775 MB allocated per read. Bulk keyed reads and compact database projections
measure 27 ms and 6.6 MB for summary reads on that fixture. Full source/detail
records, assessment hashes, queue rules and unread state are preserved.

These are local fixture timings. The later explicit owner grant authorized deployment;
actual deployed reads now measure 79–84 ms. The existing signed-in browser and
public routes are preserved.

## Completed PR review campaign · 2026-10-23

The [18-case ledger](e2e/pr-review-results.json) records **16 passed and 2 blocked**.
Genuine incoming Review PR membership and request-driven automatic review remain
blocked because the owner confirmed there is no second test account. Non-author
APPROVE/REQUEST_CHANGES variants are also blocked; the explicit COMMENT case passed.
All executable cases used the same approved persistent pilot Chrome profile and
private pilot repository. Existing sign-in and authentication were reused.

[Audited defect and clean controls](evidence/2026-10-23-pr-review-audited-controls.json)
completed both ordered Codex reviewers through DK workflows: AD's independent
Toolkit/Standards/Spec and Matt Pocock's independent Standards/Spec agents. The
[changed-head](evidence/2026-10-23-pr-review-changed-head.json) review dropped the
fixed arithmetic defect. A [same-head scope/feedback review](evidence/2026-10-23-pr-review-scope-feedback.json)
used new context/preparation fingerprints and found the newly required error-value
contract alongside the existing stdout objection, with both reviewers fresh.
Original-head native publication preflight returned STALE_HEAD without a mutation.

[COMMENT publication](evidence/2026-10-23-pr-review-publication.json) produced one
current-head, correctly anchored inline finding with an empty review body.
A later [explicit reply](evidence/2026-10-23-pr-review-reply.json) was posted exactly
once inside that unresolved thread after retained-session service recovery.
GitHub's review wrapper for replies was reconciled without a duplicate retry.
Source, PR body and thread resolution stayed unchanged during those workflow steps;
the separately authored scope clarification and synthetic feedback are identified.

Installed **0.1.0-b7acba63551a** repairs full plugin-resource snapshots, selected
native providers/delegation state, prepared-view updates, Markdown summaries,
[read freshness](evidence/2026-10-23-pr-review-read-freshness.json) and
[sealed scratch cleanup/retry](evidence/2026-10-23-pr-review-cleanup.json).
The actual module hash matches the installed release. An existing view received a
new proposal and running state automatically, and cleaned history lost its cleanup
control. The temporary audit drop-in is removed. All original 16 runs, profiles,
global settings, unrelated repository policies, recorded provider paths, mapping
and complete Tailscale routes match their private baseline. Pilot automatic review
is false; the installed integration, two pilot profiles and fixtures remain available.

DK **local 1.1.18** is a compatibility build from upstream 1.1.17, with its
[private-head patch/provenance](e2e/patches/dk-workflows-private-pr-head.json)
retained; it is not an upstream release. AD is 1.0.7 and Matt Pocock 1.0.0.
82 browser fixtures, Go race/static checks, packaging, installer recovery and smoke
passed. Final reviewed specification evidence lives in `docs/specs/reconciliations/`;
require `make specs-check` for the current comparison. [Failure](evidence/2026-10-23-pr-review-failure-matrix.json)
and [quota](evidence/2026-10-23-pr-review-budget.json) records distinguish isolated
provider/SDK/assembled-browser failures from actual ChatGPT controls. Shared quota
was not exhausted and global authentication was not revoked. Host event callbacks,
one-hour soak, coordinated login cycle and manual accessibility remain open.
Earlier entries preserve failed attempts and intermediate deployment evidence.

## Native review delegation and tracing · 2026-10-23

The first completed defect aggregate has validated AD and Matt Pocock results on
head `d46fd715eaccf53ab3a9a72669058bcbfac6c637`, both identifying stdout at line 5
and off-by-one arithmetic at line 6. Its [evidence](evidence/2026-10-23-pr-review-defect-result.json)
keeps internal independent-pass tracing explicitly unverified for that attempt.
The [discussion evidence](evidence/2026-10-23-pr-review-discussion.json) records
same-thread finding explanation, draft navigation, actual native Interrupt with a
question pending, and explicit continuation without another review or publication.

The repaired native child launcher uses a task-private Codex state context for
persistent delegation. Existing authentication/configuration are reference links;
the context base is read-only. Only sessions, logs, temporary files, shell snapshots
and writer-lock directories are writable there. The nonsecret installation ID is
referenced from separate private state. No owner session directory is exposed.
Selected-provider PATH is enforced again inside native child commands. Compatible
direct tools and private SQLite state avoid inheriting unrelated experimental
routing/workflow behavior. These native-client flags do not disable the selected
DK workflow plugin. Global Codex settings/executable and browser profile are unchanged.

Only the SDK's recognized noninteractive invocation inside an already enforced
read-only task reuses that outer boundary. The inner CLI uses its externally
sandboxed execution mode; inherited NoNewPrivs and seccomp are required first.
The task source, unrelated owner files and local/private endpoints remain protected.
Other invocation shapes and workspace-write tasks retain their original permissions;
initialization errors never retry outside the task boundary. Real delegated-command
checks verify actual child completion, source/read/local-access denial and selected
gh identity. This separates runtime support from complete reviewer acceptance.

`CODE_FACTORY_NATIVE_CHILD_AUDIT=1` enables task-private, metadata-only traces for
the campaign. The scoped runtime unit drop-in is recorded in the private continuation
checkpoint and must be removed at PR-18, followed by a safe daemon reload/restart.
It stores IDs, completion, known role hints and hashes, excluding creator/account
fields, prompts, messages, commands and URL content. CLI JSON in pinned 0.161.0 omits
V2 spawn activity; the trace also projects only descendants of that invocation from
its own private native sessions. Owner sessions and credentials are not copied into
evidence. Tracing changes CLI analytics availability; unknown metrics stay unknown.
Required public Codex content access includes the observed exact host
`sdmntprcentralus.oaiusercontent.com`; unrelated/private destinations stay denied.

## Current PR review execution · 2026-10-23

Continue from the [campaign ledger](e2e/pr-review-results.json), which records the
installed daemon version and separates complete cases from passed variants. The
same approved pilot browser remains signed in. Actual Work preparation now creates
one durable proposal without a workspace or thread, and a second existing widget
receives/reopens that same draft. Distinct PR conversation drafts survive navigation.
The native Plan scope question survived a campaign-page reload; answering Stop in
Messages reached Codex and cleared attention. See the [preparation evidence](evidence/2026-10-23-pr-review-preparation.json)
and [question/recovery evidence](evidence/2026-10-23-pr-review-question.json).

The pilot uses the local DK 1.1.18 compatibility build described in the retained
[patch provenance](e2e/patches/dk-workflows-private-pr-head.json), with installed AD
1.0.7 and Matt Pocock 1.0.0. Upstream and native installed caches were not edited.
Exact selected provider launchers preserve gh 2.86.0 and Codex 0.161.0. Bounded
packaged tasks add an invocation-local child Codex launcher that disables ambient
MCP connections and apps; existing login and installed reviewer discovery remain
available. Owner configuration is unchanged. The exact Codex feature service
`ab.chatgpt.com` is required by the observed nested client. Required OpenAI docs
hosts are separately allowlisted; unrelated connectors and destinations stay denied.
An opt-in real nested-model smoke returned its exact requested marker. This is
not evidence that either full reviewer completed.

A real Work prepare-only request with the pilot temporarily set to the existing
workspace-write Implementation profile failed with INVALID_ARGUMENT and created
no work. Existing runs kept their frozen read-only profile. Actual Settings has
restored `pilot-pr-review`; automatic review remains false. The owner confirmed
there is no second account: retain genuine incoming requests, automatic launches
from them and non-author verdict variants as blocked. Do not ask again or invent
notification evidence. Full reviewer completion/publication and the other pending
case variants still require execution; callback, soak, login and accessibility gaps
remain separate.

## PR review browser campaign · 2026-10-23

The first real review started exactly the inspected proposal, then reported both
reviewers unstarted because its retained lifecycle runtime disappeared between
sandbox commands. [Failure evidence](evidence/2026-10-23-pr-review-first-run.json)
keeps that original result. Follow-up repairs announce externally prepared drafts,
choose useful Markdown body summaries, persist workflow state under private
`XDG_STATE_HOME`, and expose only required native registry and existing Codex
account-store paths read-only. Credential contents are not copied or logged.
**0.1.0-aff495347694** passed 81 browser fixtures, Go race, full reviewed spec
reconciliation, packaging/installer/smoke and native lifecycle/registry/auth checks.
It is installed with the original 16 runs, provider/mapping and routes preserved.
Refresh connection metadata and open a fresh pilot-package view for browser
reruns; a nested-provider diagnostic alone cannot establish review acceptance.

Execution is now active. [Plugin setup evidence](evidence/2026-10-23-pr-review-plugin-setup.json)
records browser-driven installation of DK workflows 1.1.17, AD 1.0.7 and Matt
Pocock 1.0.0, plus verified deployment of **0.1.0-67e47c0c81dc** for complete
package-resource snapshots. All 16 original runs, provider/mapping and routes
remain unchanged. The owner-only pilot uses a dedicated read-only/ask profile
and repository-scoped ordered Codex reviewer bindings; automatic review is off.

Uploading the linked 0.2.4 archive under the existing name returned HTTP 400:
`A plugin named code-factory already exists.` The observed package management
view has no update control. A distinct **Code Factory PR E2E** package,
`Plugin_89dd894a227481919ae7e682fe34c77f`, is now installed with Apps 1 / Skills 3
and the same existing `asdk_app_6ad792b279348191b76d3443615b66e0` connection.
Only its package/display names differ; existing 0.2.2 and its chats are preserved.
Use the explicit pilot package for the campaign, not a bare ambiguous name.
Pilot issue #6 and PRs #7/#8 are isolated acceptance fixtures. No incoming-request
or non-author-verdict acceptance is possible with only the currently available
owner account. Actual nested reviewer execution is still pending in the ledger.


The [PR review E2E plan](e2e/pr-review.md) defines 18 user-facing acceptance cases
for the existing pilot browser profile and dedicated private repository. It couples
Code Factory with DK workflows and ordered AD / Matt Pocock Codex reviewers.
The [campaign ledger](e2e/pr-review-results.json) records preparation separately
from execution and lists current case results. Follow its readiness gates before
running the full pipeline, including complete plugin resources in skill snapshots,
bounded nested runtime access and actual installed package/version verification.
The agent owns execution, repairs and browser reruns. Incoming-request acceptance
requires a genuinely authorized second actor; publication uses separate explicit
pilot-only instructions. Existing callback, soak, login and accessibility gaps remain.

## Public connection recovery · 2026-10-22

Installed **0.1.0-ebf7a6cdd84a** showed Reconnecting with a connection-interrupted
alert while local authenticated diagnostics and tailnet HTTPS remained healthy.
Fresh TLS connections to both public Funnel ingress addresses closed before HTTP.
Reapplying only the existing Code Factory handler preserved the exact route set
but did not recover access; one restun and socket rebind also did not recover it.
One Tailscale daemon restart renewed the control session. Recovery was partial
before both public peers and the existing widget recovered; the precise upstream
cause and causal contribution of the restart remain unproven.

Before any shared Tailscale restart, privately snapshot the complete current Serve
configuration. This restart loaded an older handler set that omitted two unrelated
mechanic-helper handlers. Both were restored from the observed snapshot, and the
complete ports/handlers/Funnel configuration was verified exactly equal afterward.
Do not reset Serve/Funnel, disable all of 443, or assume configured routes survive
a restart unchanged. Avoid repeated restarts when public edges need propagation.

The [recovery evidence](evidence/2026-10-22-connection-recovery.json) records passing
raw public OAuth/MCP and independent SDK checks on both ingress peers, with all
ephemeral credentials revoked. The same signed-in widget automatically returned
to Connected without reload or sign-in. App services and binary were unchanged;
no agent work or deployment was performed. This bounded recovery does not replace
soak, event callbacks, coordinated login or manual accessibility acceptance.


This runbook records the working arrangement on this computer as of
**2026-10-22**. Verify live state before changing it. The agent runs commands and
browser tests; the owner supplies initial sign-in and consent for the selected
attachment mode. Default native attachment uses Chrome Allow; the explicitly
approved pilot profile uses private loopback debugging.
Do not turn routine local work into instructions for the owner to execute.

## Current workbench and form recovery · 2026-10-22

Installed **0.1.0-ebf7a6cdd84a** has Working, Issues, My PRs, Review PRs and
Messages. Issues defaults to open, including linked PR summaries. Working retains
started work through idle/failure/interruption until GitHub closure or an
implementation PR merge. Pipeline selection controls one retained Codex session;
review its mode, sandbox, approvals and publishing authority before Start.

Messages includes native, structured-message and recovered textual questions.
A stopped textual ask gets at most two question-only Plan form-repair attempts
in the same thread before free-text fallback and the optional session console.
Automatic repair input is technical activity; actual user replies retain their
ordinary role. No reply needed dismisses only a textual fallback. The console
shares the original session, journal, reply draft and controls.

After upgrading, open the observed app management path
`/settings/plugins-settings/plugin_asdk_app_6ad792b279348191b76d3443615b66e0`,
choose **Refresh tools**, wait for completion, then open a fresh Work chat from the
installed complete plugin. An otherwise healthy old iframe can retain its cached
widget. Verify the actual new tabs, filter and console behavior before acceptance.

Operate only an explicitly identified newly created test run. The controller's
empty `fill` did not clear/focus a controlled textbox in this session: verify the
actual focused element and disabled Send state after native Control+A / Backspace.
Do not batch unverified Tab/Enter navigation through retained run controls. A
navigation error cleaned an older clean pilot worktree during this check; it was
restored at the same branch head/path/thread and prior interrupted state. The
[evidence record](evidence/2026-10-22-workbench.json) retains the incident and
restoration verification. Element-argument script evaluation failed on this
attachment; native click, fill and keyboard operations worked. No sign-in, CSP,
route reset or global browser-setting workaround was used.

The final installed native question/UI answer/follow-up and first-attempt recovery
smoke is bounded evidence. Its Plan snapshot used owner MCP launch after review;
the final Default/read-only recovery pipeline used UI selection and Start. The
earlier `71a9` UI Plan launch is a separate observation. Host callbacks, new-build
soak, login cycle and full manual accessibility remain separate open checks.

## Continue by message; Messages means blocked asks · 2026-10-21

Installed **0.1.0-08c15806095a** removes Resume and the Messages / All agents inventory.
Activity lives only in issue/PR work status. Send to interrupted/failed retained
work restores its original thread/workspace and uses the submitted message as the
continuation turn. There is no generic resume turn before it. Ordinary accepted
sends clear only unchanged text; failed or uncertain sends keep drafts and are not
replayed automatically. A missing retained session/workspace is an error; prepared
and cleaned records cannot silently create substitute work. First Start remains
explicit. Work history / Draft launches belong to Item details; Interrupt, profile,
workspace and cleanup remain secondary Details. Dirty cleanup protection remains.

Messages counts/lists only pending blocking requests for live waiting/running
sessions. Nonblocking requests remain on their owning conversation. Stale,
answered, orphaned or disconnected requests are excluded. Selecting an ask opens
its owning item/run/dialog; answering removes only that ask.

The [message-wake record](evidence/2026-10-21-message-wake.json) separates 62 browser
fixtures/CI from one actual signed-in Plan/read-only/ask run: native question in
Messages, UI interrupt/stale exclusion, inactive UI Send restoring the same Codex
session, new question, UI answer/completion and own clean cleanup/history. Existing
runs are unchanged. The initial snapshot was prepared with the local owner tool,
then explicitly reviewed/started in the installed embedded UI. Native approval
variants, Events, login, soak and full manual accessibility remain version-bound
or outstanding; this check does not replace them.

## Human-facing conversation · 2026-10-21

Installed **0.1.0-9313f82ae73f** defaults to agent progress/status and final messages,
user replies and readable errors. Technical activity (commands, plans, raw changes,
provider summaries and error JSON) stays under the existing Details disclosure.
Question/approval controls remain actionable outside it. Data is retained; only
its normal presentation changes. The prior Agent details & actions label is now
Details; `.agent-details` identifies that disclosure. Native closed details can
retain child layout rectangles: use checkVisibility or the accessibility tree,
rather than rectangle presence alone, to judge actual visibility.

The [concise conversation record](evidence/2026-10-21-concise-conversation.json)
separates 58 browser tests/CI from an installed nonempty retained pilot transcript.
That check recorded only labels, counts and visibility and started no agent work.
Full history/host lifecycle and manual accessibility remain separate acceptance.

## Connection failures and read recovery · 2026-10-21

A connected view can retain an old bundle and its stale error state. The
[investigation](evidence/2026-10-21-connection-investigation.json) observed an old
failure banner through 263 subsequent RPC replies without another protocol error.
Installed **0.1.0-d28d7827a940** fixes that state separation and failed-handshake
recovery. Known reads retry bounded transport failures; Start, Send, answers,
saves and lifecycle actions remain one attempt. The exact host-wrapped
McpServerError connection exception is distinct from ordinary tool validation.

Healthy polling clears polling feedback only; item/transcript reads recover
locally and preserve drafts/output. Use the new content-fingerprinted view after
upgrades; preserve existing open views/drafts rather than silently reloading them.
Gateway upstream warnings record only fixed reason labels (client_canceled,
deadline_exceeded, daemon_unavailable, connection_reset, upstream_eof,
upstream_timeout or upstream_transport). Request audit adds duration_ms. Correlate
these timestamps with the UI; the previous status-only logs cannot reconstruct
all earlier causes. Never add raw error strings, headers, tokens or bodies.

The [recovery record](evidence/2026-10-21-connection-recovery.json) separates 56
browser fixtures/CI from two controlled read-only fault probes in signed-in Work.
Those probes replace one healthy read reply locally and remove their listeners;
they do not establish a real outage cause or full uptime/accessibility acceptance.

## Focused queues and first-message chat · 2026-10-21

`open_factory` now opens Issues (open/no linked PR by default), My PRs,
Review PRs and Messages. Retained runs are under Messages / All agents. A work
row opens its conversation with item summary immediately; no intermediate item
page or Open agent step. First-message drafts remain per item, follow-ups per run.
Start is explicit; changed authority is reviewed again, and uncertain Start checks
the same record before retry. New run under Item details keeps the optional review.
Settings remains its own chat view. Priority badges precede titles.

Installed **0.1.0-c0601b8f3508** passed 48 browser tests and branch CI. The
[work queues record](evidence/2026-10-21-work-queues.json) separates fixtures from
actual signed-in read-only queue/conversation navigation. It does not replace
full provider lifecycle, Events, soak, login or manual accessibility acceptance.

## Repository identifier links · 2026-10-21

The inbox repository/number text is the external issue/PR link; no separate
GitHub label/icon remains. It is outside the row selection button and uses the
same host bridge. Build **0.1.0-87df721c6d6c** passed branch CI and fresh signed-in
Work rendering checks. See the [identifier record](evidence/2026-10-21-identifier-link.json)
for exact source/build/fixture/host scope. The transient connection report and
recovered public connection checks are recorded [separately](evidence/2026-10-21-tailscale-recovery.json).

## Independent settings entry · 2026-10-21

`open_factory` opens the inbox and agent surface; `open_settings` opens its own
configuration view in chat. Main navigation contains only Inbox and Agents.
Settings saves stay in that view and do not close or reset an open task view.
Each inbox row has a separate original-GitHub issue/PR link routed through the
host browser; activating it does not select or mark the item read.

The two documents have independent content-fingerprinted resource identities.
The legacy static resource remains inbox-only; cross-surface/query/stale variants
are rejected. Refresh existing tool metadata and open fresh views after upgrade.
Use the [separate settings record](evidence/2026-10-21-separated-settings.json) for
this revision’s actual fixture/installed-host scope.

Installed **0.1.0-3fe6288ffc29** passed 39 browser tests and branch CI. Fresh
signed-in Inbox and Settings chats loaded matching release JavaScript, with
independent navigation and no settings inside the inbox. The host accepted the
pilot PR link request without an error; destination-page navigation was not
observed in the pilot targets. No config or agent work was changed by this smoke.

## Compact single-page UI · 2026-10-21

The source [layout contract](ui/design.md) uses a full-width Inbox list and a
separate item page, plus separate Agents list and Agent conversation pages. Top
Inbox/Agents navigation always returns to the list. Contextual Back restores list
query, scroll and row focus, or the originating item. Relationships and secondary
triage actions are disclosures; ordinary drafts survive record/page changes.
Brand/footer chrome, normal connection/sync text and unused integration forms are
absent. Use the [compact verification record](evidence/2026-10-21-compact-ui.json)
for this revision’s source/build/rollout scope. **0.1.0-125bfd2f9079** is installed;
a fresh signed-in Work view has matching release JavaScript and passed bounded
list/item/agent/Back/Settings smoke. Older records remain version-bound.

## Local workflow revision · 2026-10-21

The source UI now opens the default prepared review directly; **Edit** exposes
customization and **Advanced** holds model, effort, base and skills. **Start agent**
remains explicit. **Open agent** continues the newest retained run from an item;
**New run** starts a separate proposal. Retained prepared records reopen from the
collapsed **Draft launches** group. An uncertain Start reads the same run before
offering another Start; unavailable status leaves **Check start status**.

Priority selections save immediately and restore the previous value with inline
feedback on failure. Link drafts stay with their item and message drafts with
their run while the app remains open. Reload ends those ordinary drafts. Global
attention selects its run and identifies the item/profile. Workspace/cleanup and
command output are disclosures; cleanup still retires a retained session before
a dirty refusal. Settings uses collapsed sections; Jira stays disabled.

These source changes have a separate [local verification record](evidence/2026-10-21-workflow-simplification.json).
The [installed upgrade](evidence/2026-10-21-workflow-upgrade.json) is now
**0.1.0-b3c0fdf03dc5**, with healthy services and a fresh signed-in Work dashboard
whose JavaScript matches the release. This is read-only dashboard/Settings smoke;
earlier full core and soak observations remain tied to `e945`. Future upgrades
must first inspect unrelated live runs and preserve the approved browser session.

## Machine and access

| Item | Current value |
| --- | --- |
| Workspace | `/extra/dkropachev/code-factory-plugin` |
| OS/account | Ubuntu 26.04.1 x86-64; `dkropachev` |
| Ethernet | `192.168.0.100` |
| Wi-Fi | `192.168.0.101` |
| Tailscale IPv4 | `100.83.203.0` |
| Tailscale hostname | `dkropachev-ai-rig.tail62c9f9.ts.net` |
| Persistent desktop (preferred) | Direct RDP `192.168.0.100:3390` or `100.83.203.0:3390`; user `gnome-remote-desktop-headless.service` |
| Legacy remote login | RDP `192.168.0.100:3389`; system `gnome-remote-desktop.service`, preserved |
| Browser | Google Chrome Stable; installed through Google's official Debian package |
| Node at setup | `~/.nvm/versions/node/v24.16.0/bin/node`; use `command -v node` to recheck |

The owner connects from another computer on the LAN using an RDP client
(Windows Remote Desktop Connection, Linux Remmina, or another compatible client).
For the persistent desktop, connect to **`192.168.0.100:3390`** (or
**`100.83.203.0:3390`** through Tailscale), authenticating as **`remote`** with
the existing private RDP password. This joins the already running `dkropachev`
desktop directly; no second GDM login is needed. Disconnect the RDP client to
leave the desktop and Chrome running. Use the direct endpoint to reconnect;
do not choose to terminate an existing session as a reconnect method.

The preserved legacy port **3389** still uses GNOME Remote Login: authenticate
as `remote`, then use the Ubuntu login screen as `dkropachev`. Neither mode uses
xrdp. The direct persistent endpoint is preferred for browser testing.

The RDP credential record is **`~/.local/state/remote-desktop/credentials.json`**,
mode 0600 in a private directory. It contains `host`, `username`, and `password`.
Retrieve a credential only when needed for authorized authentication; keep it in
memory and use stdin or a protected client mechanism. Never print the file or put
its password in commands, this document, screenshots, chat, or test evidence.
At the owner's last explicit password change, RDP and Ubuntu login passwords were
set equal. This file tracks RDP; do not assume Linux still matches after later
rotation. Do not reset either password without the owner's instruction.
The credential file's `host` field belongs to the preserved legacy endpoint;
use port 3390 explicitly for the persistent desktop. A private nonsecret
connection descriptor is saved at
`~/.local/state/remote-desktop/persistent-desktop.json`.

RDP is enabled at boot. Useful read-only checks:

```sh
ip -j -4 address show scope global
tailscale ip -4
systemctl is-active gnome-remote-desktop.service
sudo -n grdctl --system status
ss -lnt '( sport = :3389 or sport = :9222 or sport = :9223 )'
```

Do not use `grdctl status --show-credentials` in output. A private temporary
password page on port 8446 was used during installation and expired; it is not the
normal access mechanism. Remote desktop control does not itself attach an agent
to Chrome. Use the browser controller below.

## Installed application and durable state

| Item | Current value |
| --- | --- |
| Last verified installed daemon | `0.1.0-aff495347694` (PR campaign package/state/registry repairs; actual reviewer reruns remain in progress) |
| Complete browser package | `0.2.2` |
| Stable launcher | `~/.local/bin/code-factory` |
| Versioned releases | `~/.local/lib/code-factory` |
| Application data | `$XDG_DATA_HOME/code-factory`, normally `~/.local/share/code-factory` |
| Persistent plugin | `~/.local/lib/code-factory/plugin` |
| Daemon | `code-factory.service`, loopback 8787 |
| OAuth gateway | `code-factory-gateway.service`, public loopback 8788 and private loopback 8789 |
| Public MCP | `https://dkropachev-ai-rig.tail62c9f9.ts.net/code-factory/mcp` |
| Owner approval | `https://dkropachev-ai-rig.tail62c9f9.ts.net:8444/approve` (flow supplies its query) |
| Installed complete plugin | `https://chatgpt.com/plugins/Plugin_34caeb5c66048191a1ac0c8f01c31e73` |
| Registered remote app | `asdk_app_6ad792b279348191b76d3443615b66e0` |
| Dedicated private fixtures | `dkropachev/code-factory-pilot-20261019`; issues 1/3/4/5 and PR 2; retain synthetic 4/5 while acceptance is incomplete |

The [prior Work core evidence](evidence/2026-10-21-final-work-core.json)
records the installed `0.1.0-e945f9020696` build and content-fingerprinted resource.
Initial pilot-profile sign-in was confirmed, and the actual refreshed Work iframe
now paints dark owned backgrounds despite transparent host `html`. A new explicit
Plan/read-only/ask launch, question/answer, recovered-run follow-up, dirty refusal,
same-thread Resume and clean cleanup/history were observed. Gateway-only restart
retained authenticated polling without relogin. Controller reattachment retained
the signed-in profile without another native Allow prompt.

The [final-build triage matrix](evidence/2026-10-21-final-work-triage.json)
records synthetic issue 5 arriving New/unread, independent displayed read/New/action
history, immediate relationship Add/Remove and priority override/label/inference
precedence on this installed build. Removing its P2 label showed Pending/running
then real Codex P3 inference; a later comment restored unread without New, and
the journal proves reassessment for the changed revision. The brief comment-time
Pending UI state was not captured. Test overrides/link were removed and P2 restored;
issues 4/5 remain retained synthetic fixtures.

Both [raw public transport](evidence/2026-10-21-final-public-connection.json) and
[independent SDK](evidence/2026-10-21-final-sdk-connection.json) checks passed both
public ingress peers and revoked ephemeral credentials. Actual core observation
is separate from these protocol probes. Eight actual app scans found no automated
violations, with six overlap-related contrast nodes still requiring manual review;
light diagnostic scans changed only the app theme attribute and restored dark.
No complete WCAG or screen-reader acceptance is claimed.

The [connected soak](evidence/2026-10-21-connected-soak.json) passed on the same
iframe from `2026-10-21T04:33:49.700Z` to `05:34:57.611Z` (3667.911 seconds): 178 UI
samples, 2304 successful polling responses and 121 distinct sync times, with no
disconnects, alerts, connection transitions or unload. Independent service sampling
also retained all three expected services and version `e945` for over an hour.
This bounded observed interval is complete; it is not a guarantee of future uptime.

The authorized [CSP check](evidence/2026-10-21-work-csp.json) passed an actual
`connect-src` denial for an unlisted reserved `.invalid` origin without credentials
or application data. The app already enforced this policy with the global toggle
off. Temporarily enabling the global toggle persisted across Settings reload;
a fresh Work view remained Connected and bridged successfully. The setting was
restored to off and verified after reload. Earlier “CSP off” labels alone did not
prove absent app enforcement. Other CSP directives remain outside this probe.

Events creation produced no confirmed response, error or subscription ID after
a stalled attempt; stored subscriptions remain zero, with cause undetermined.
The real desktop logout/login has not run: timing awaits the owner's confirmation.
Preserve the current session and unchanged lingering policy until that coordination.

These IDs are observations, not templates for another account. Reuse working
resources. The complete package has **Apps 1 + Skills 2**, a connected account, and
**Try in chat**. The remote app's standalone wrapper is separate from that
package. A `Plugin_...` package ID cannot be passed as the remote app ID.

Private data includes `runtime.json`, `tailscale.json`, `registered-app.json`,
SQLite/WAL, retained worktrees, the vault, owner token, and OAuth authorization
state. Do not dump this directory. Runtime records the resolved provider paths;
the daemon uses authenticated **Codex 0.161.0**, even when global `codex` is newer.
Do not replace GitHub/Codex sign-ins. Do not enable Jira or switch back to the
OpenAI tunnel without a new task requiring it.

```sh
readlink -f ~/.local/bin/code-factory
systemctl --user is-active code-factory.service code-factory-gateway.service
~/.local/bin/code-factory doctor
tailscale serve status --json
```

Preserve unrelated port 443 `/` → loopback 8644 and private 8443 → loopback 8081.
Code Factory uses `/code-factory` plus its scoped RFC metadata paths on public 443.
Private 8444 is owner consent. Legacy 10000 remains for rollback compatibility.
The separate browser lab, when running, uses private 8445. Never use
`tailscale serve reset`, `tailscale funnel reset`, or whole-port 443 `off` to
repair/remove this app. Keep existing login/lingering policy.

## Persistent desktop and pilot browser · 2026-10-24

The owner explicitly authorized an always-running desktop that can be joined
through RDP without leaving the client connected. The native system service
`gnome-headless-session@dkropachev.service` is enabled at boot and restarts after
exit with a five-second delay. This is a full GNOME desktop with a permanent
1920×1080 virtual monitor; Google Chrome runs normally, without `--headless`.
Closing an RDP client does not stop this desktop or Chrome.

The packaged DynamicUser helper failed system D-Bus authentication. A scoped
`/etc/systemd/system/gnome-headless-session@dkropachev.service.d/10-dbus-identity.conf`
uses the existing static `gdm` account and explicitly retains private temporary
storage, IPC cleanup and strict/read-only filesystem protections. The vendor
unit is unchanged. The virtual monitor comes from
`~/.config/systemd/user/org.gnome.Shell@ubuntu.service.d/20-persistent-monitor.conf`.
Native headless RDP extends the desktop with another monitor. The scoped user
`gnome-persistent-monitor.service` makes that RDP monitor the sole logical primary
at 0,0 while connected, then restores the fixed fallback on disconnect. This
moves existing Chrome windows and native prompts into the client's visible
screen. Its helper is `~/.local/lib/persistent-desktop/monitor.py`; it watches
Mutter display metadata only and never reads browser or credential contents.

Direct persistent RDP uses port **3390** with the existing private RDP username
and password. The original port **3389** and its credentials remain available.
Only port 3390 is constrained by the dedicated nftables table
`inet code_factory_persistent_rdp`: IPv4 loopback, LAN `192.168.0.0/24`, Tailscale
IPv4 `100.64.0.0/10`, IPv6 loopback and Tailscale IPv6 `fd7a:115c:a1e0::/48`.
`code-factory-persistent-rdp-access.service` loads this table before the persistent
desktop. Existing firewall tables, public/private Code Factory routes and all
Tailscale Serve routes are preserved. Its RDP certificate/key are private local
service resources; the private key is mode 0600. Never log credential contents.

The [dated desktop evidence](evidence/2026-10-24-persistent-desktop.json) records
local FreeRDP connections through the LAN and Tailscale addresses using pinned
TLS. Both authenticated desktop streams succeeded, and desktop, Shell and Chrome
PIDs stayed identical during and after disconnect/reconnect. The fixed monitor
remained present without a client. This does not claim an actual host reboot or
off-host reachability, and it is separate from ChatGPT queue/browser acceptance.
A client-side `ERRCONNECT_CONNECT_CANCELLED` after intentionally stopping the test
client is expected; it does not establish that the desktop logged out.

The current approved signed-in profile is
`~/.local/share/code-factory/browser-pilot/profile`, managed by
`code-factory-pilot-browser.service`. The debugger remains loopback-only at
`127.0.0.1:9224`. The user browser unit is enabled under `graphical-session.target`
and automatically starts when the persistent GNOME session starts. The scoped
`~/.config/systemd/user/code-factory-pilot-browser.service.d/zz-persistent-desktop.conf`
selects graphical Chrome, this exact profile, session restoration and a normal
window size. The old runtime `recovery.conf` is preserved; the owned temporary
`zz-e2e-headless.conf` has been removed.

The owner originally requested a fresh profile and confirmed sign-in. Inspection
found that the pre-existing recovery override had selected the original approved
profile instead; that is where the confirmed sign-in was observed. Preserve it.
The unused `browser-pilot-20261024/profile` directory is not a signed-in replacement.
No authentication was copied. Main Chrome and the separate browser lab are not
part of this service.

Reconnect with `node scripts/browser_control.mjs --pilot`, discovering fresh page
IDs and element UIDs. Start a stopped pilot with
`systemctl --user start code-factory-pilot-browser.service`. Start a stopped
persistent desktop with
`sudo systemctl start gnome-headless-session@dkropachev.service`. Do not restart
GDM, the system RDP listener or unrelated services merely to attach the browser.

A manual Cloudflare check had blocked the temporary headless-Chrome fallback.
Do not automate that challenge or infer ChatGPT acceptance from desktop health.
The owner handles any native challenge or sign-in in the persistent desktop;
pause inspection during that manual step and resume after confirmation.
The owner confirmed that the login keyring and existing RDP passwords match and
authorized automatic unlock using that private credential. The enabled user
`persistent-desktop-keyring.service` reads the existing owner-only credential
file and calls GNOME's native `UnlockWithMasterPassword` operation over the
private user session bus. The keyring stays encrypted on disk; no additional
password copy is created, and no secret enters argv, environment or logs.
The helper is `~/.local/lib/persistent-desktop/unlock_keyring.py`. It checks the
actual collection lock state, records only nonsecret status and fails closed if
the credential or native API is unavailable. GNOME's internal API may require
compatibility review after an OS upgrade.

Pilot Chrome starts after this service. Its
`~/.local/lib/persistent-desktop/browser_ready.py` guard waits for both an actual
logical display and the unlocked login keyring. Chrome automatically reopens
after exit and restores the same approved profile. Controlled keyring-daemon
restart/unlock and graceful Chrome exit/automatic restart passed; the existing
private Work conversation and composer remained signed in without another
prompt. The desktop and Shell PIDs stayed unchanged. A complete desktop login
cycle and host reboot remain untested.

Do not reset the keyring, change Chrome's password store, collect its password in
chat or copy browser authentication. If the RDP or keyring password changes,
update the private credential through an owner-controlled local entry, then
restart `persistent-desktop-keyring.service`. A failed unlock leaves Chrome
waiting; the owner can still use the native unlock prompt. ChatGPT may expire
its session or require manual verification independently. Pause browser
inspection during any manual sign-in and resume after owner confirmation.

The first sign-in in the fresh profile is manual. Pause browser inspection until
the owner explicitly confirms sign-in is complete. A listening debugger only
proves browser readiness, not an authenticated ChatGPT session. After confirmation,
discover current page IDs and element UIDs again; never reuse terminal session IDs.

The owner explicitly approved a separate persistent pilot profile to avoid native
Allow prompts on every controller reconnect. Its private profile is
the current path above, with debugging bound only to
`127.0.0.1:9224`. Do not alter the main browser on 9222, the separate
lab on 9223, existing authentication or unrelated tabs.

The owner completes the first ChatGPT sign-in manually in that profile. Pause all
inspection during sign-in and resume only after explicit confirmation. Never copy
cookies, tokens or profiles from the main browser. Once the approved profile is
signed in and running, attach the local controller with:

```sh
node scripts/browser_control.mjs --pilot
```

This explicit mode uses the approved loopback browser URL; it does not launch a
browser or authorize another profile. An optional `--browser-url` is limited to
an already-approved literal HTTP loopback endpoint; aliases, credentials, URL
paths, queries and fragments are rejected without echo. Selecting a URL grants
no new permission to inspect another session. The persistent profile can retain its own
sign-in across controller reconnects. Controller readiness still does not prove
attachment: perform an actual page operation and obtain fresh page/element IDs.
All JSON-line output filtering, URL masking and iframe typing rules below apply.
Keep 9224 private; do not expose it through LAN binds, Tailscale or public hosting.
A real desktop logout/login remains coordinated with the owner and requires its
own observed startup evidence.

## Attach to the owner's signed-in Chrome

The default controller mode retains the existing native attachment flow. The approved pilot mode above is the separate explicit alternative; neither mode copies cookies.

1. Reuse the installed Google Chrome on the remote desktop.
2. The owner signs into ChatGPT manually. Do not inspect during sign-in. Wait for
   explicit confirmation that sign-in is finished.
3. The owner opens `chrome://inspect/#remote-debugging` and enables remote
   debugging. The default Chrome listener is loopback 9222.
4. Start the local controller. Request attachment and let the owner accept
   Chrome's native **Allow** prompt when required. Do not bypass native consent.

```sh
# If dependencies are missing, install them before requesting browser access.
cd /extra/dkropachev/code-factory-plugin
# npm ci --prefix web
# npm install --prefix ~/.local/lib/code-factory/browser-tools \
#   --ignore-scripts --no-audit --no-fund chrome-devtools-mcp@1.11.0
node scripts/browser_control.mjs
```

Keep the process in a persistent interactive tool session (`exec_command` with
`tty:true` and a short yield). Send JSON lines through that session's stdin:

```json
{"id":"attach","name":"list_pages","arguments":{}}
```

The controller prints schemas for its permitted tools on startup. `ready:true`
means the MCP client initialized, not that Chrome attached. Attachment requires
a successful operation against the intended signed-in browser. Use returned page
IDs; do not reuse old terminal session IDs or assume page1 is still the test tab.
Chrome's new auto-connect listener may not offer normal `/json/version` HTTP
discovery; use `--autoConnect` through this controller. Missing
`DevToolsActivePort` usually means the native debugging switch is not enabled.

The controller uses local stdio and disables usage statistics and CrUX reporting.
Never expose 9222/9223 through LAN binds or Tailscale. Outside the explicitly approved pilot profile above, do not restart Chrome with
another profile. Never copy authentication, enable insecure certificate handling,
or change unrelated browser settings to make attachment succeed.

Use only the relevant ChatGPT test tabs and private approval page. Preserve the
owner's unrelated tabs and conversations. Do not repeatedly ask for permission
after the user has signed in and Chrome has approved this connection.

## Control protocol and output handling

Examples below use **illustrative** page IDs and element UIDs. Inspect current
schemas and obtain fresh IDs after navigation, a new tool response, or widget
replacement.

```json
{"id":"plugins","name":"navigate_page","arguments":{"pageId":1,"url":"https://chatgpt.com/plugins"}}
{"id":"snapshot","name":"take_snapshot","arguments":{"pageId":1}}
{"id":"click","name":"click","arguments":{"pageId":1,"uid":"CURRENT_UID"}}
{"id":"read","name":"evaluate_script","arguments":{"pageId":1,"function":"() => ({path:location.pathname,title:document.title})","waitForStableDom":false}}
```

Parse controller JSON lines; ignore echoed commands without `result`. Mask URL
queries **and fragments before returning output to the model or evidence**:
OAuth approval and callback URLs carry secret request/state/code values. Do not
print raw tool results. For text blocks, a useful URL sanitizer is:

```js
function safeURLs(text) {
  return text.replace(/https?:\/\/[^\s"'<>)]*/g, raw => {
    try { const u = new URL(raw); return u.origin + u.pathname; }
    catch { return '[URL]'; }
  });
}
```

This only sanitizes URLs; it does not make arbitrary headers, bodies or input
values safe. Never request those during authentication. Avoid full snapshots of
the chat sidebar. Filter snapshots to the current dialog or the subtree beginning
with `Iframe "Code Factory"`; omit unrelated titles and large asset URLs. Prefer
small explicit DOM summaries. Browser automation can return asynchronously:
drain outstanding responses, match their request IDs, and apply the same filter
to delayed snapshot results rather than accidentally printing the entire page.

ChatGPT keeps hidden cached `<main>` elements. To inspect the current screen:

```js
() => [...document.querySelectorAll('main')]
  .filter(el => el.checkVisibility())
  .map(el => el.innerText.slice(-4000))
```

The widget has nested cross-origin frames. Use a fresh widget element UID in
`evaluate_script.args`; the tool selects that element's frame. Inspect its
`ownerDocument`, not the top-level ChatGPT document. For example, with the
**Message agent** textarea UID:

```json
{"id":"agent-state","name":"evaluate_script","arguments":{"pageId":3,"args":["CURRENT_TEXTAREA_UID"],"function":"(input) => ({status:input.ownerDocument.querySelector('.detail .status')?.innerText,transcript:input.ownerDocument.querySelector('.transcript')?.innerText.slice(-2000),alerts:[...input.ownerDocument.querySelectorAll('[role=alert]')].map(e=>e.innerText)})","waitForStableDom":false}}
```

Chrome DevTools MCP `fill` was observed to report success inside the app iframe
while React reset the value and left Send disabled. Do not diagnose that alone
as an application failure. Native editing worked:

```js
async (input) => {
  input.focus();
  input.select();
  const inserted = input.ownerDocument.execCommand('insertText', false,
    'Reply UI_FOLLOWUP_OK. Do not edit files or contact external systems.');
  await new Promise(resolve => setTimeout(resolve, 100));
  return { inserted, length: input.value.length };
}
```

Then click the current **Send message** UID. For ChatGPT's contenteditable
composer, preserve the selected plugin mention: focus it, collapse a DOM Range
at the end, and use native `execCommand('insertText',false,text)` before Enter.
Replacing the whole composer can remove the plugin mention. Wait for actual
React updates and agent state changes rather than treating a click as completion.

## Build and connection checks

Development changes use the [layered specification gate](specs/README.md),
canonical [glossary](glossary/README.md), [UI registry](ui/README.md) and
[user-flow contracts](user-flows/README.md). The repository `reconcile-specs`
skill coordinates maintenance; layer skills retain separate ownership. Run
`make specs-check` for current edits, `make specs-ci` for fast deterministic
checks, and `make ci FLOW_BASE=<base>` for the full branch comparison. Historical
flow commands remain compatible. Reconciliation records describe local review
and verification and never substitute for installed-version browser evidence.

The UI registry includes widget types, displayed instances, elements, conditional
states, panes/pages/dialogs, endpoints, existing assertions and explicit gaps.
Its generated inventory/diagrams do not establish every visual or accessibility
variant. No agent runs or model credentials are needed by the specification gate.

```sh
make verify
make connection-test
make connection-sdk-test
# Optional real-model suite; consumes the authenticated Codex account:
make test-real-provider
```

`make verify` covers build, vet/types, race/protocol/browser tests, packaging,
installer lifecycle and compiled smoke. Default Make/verify commands force `CODE_FACTORY_REAL_CODEX=0`; an inherited or command-line opt-in cannot activate real model tests. Use the explicit provider target above for those tests. The deployed connection commands test
public TLS, OAuth and MCP with ephemeral tokens that they revoke. SDK tests force
fresh connections and verify each actual public peer address. `--tailnet` is
explicitly local-only and cannot prove internet access or ChatGPT acceptance.
Use live `tailscale.json` as the endpoint source; never default new work to 10000.

The embedded UI is advertised with a full-content SHA256 resource URI. Changed
HTML/JS/CSS must produce a new identity in every tool reference and resource
advertisement. The original exact static URI remains an unadvertised read alias;
arbitrary previous hashes and query suffixes are not compatibility aliases. A
healthy local resource read is not proof that Work loaded it: after upgrade,
refresh tool metadata/open a fresh view and verify the actual content identity
and rendered owned surfaces before recording UI acceptance.

Build source changes into a versioned release, then install that release before
claiming the real Work app exercised those changes. Follow
[operations.md](operations.md) for checksums and upgrades. Use the **new extracted
binary** for the first upgrade from an old installer lacking gateway-profile
snapshot support. Do not overwrite the stable launcher with an unversioned build.
A rollback archives post-upgrade SQLite state: do not casually roll back a live
installation after the owner has created new work. Use fixtures or a coordinated
test before new work begins, and preserve the pre-rollback archive.

## Real browser E2E procedure

1. **Preflight.** Read acceptance and current service/profile state. Confirm the
   owner-approved Chrome session works. Identify only the private pilot repo and
   explicitly identified test runs. Record installed version and test start time.
2. **Reuse the connected plugin.** Open the installed complete plugin URL above,
   choose **Try in chat**, and select **Work**. In a fresh chat, use its starter or
   ask it to call `open_factory` for GitHub issues/PRs without launching work.
   Verify the actual Code Factory iframe says Connected and shows pilot data.
3. **Observe the UI directly.** Verify sync timestamps and visible polling. The
   conversational model may say it cannot see an embedded dashboard while the
   widget is visibly working. Only browser observation establishes rendering.
   Check actual host CSP settings; do not disable CSP to hide failures.
4. **Inbox semantics.** For full acceptance, use identifiable new pilot fixtures
   to check initial unread baseline, later New items, revision-aware unread,
   independent actions, priority override/label/inference, links and PR detail.
   Listing/opening must not launch an agent. Restore test overrides deliberately.
5. **Explicit launch.** From a pilot issue, edit/review a prepared snapshot. Use
   Plan mode, read-only sandbox and publishing authority `ask`. Confirm the
   isolated worktree and only then Start. The setup skill can also explicitly
   launch its own read-only verification; do not accidentally start a duplicate.
6. **Real question and UI answer.** Request: “Use request_user_input to ask me to
   choose Continue or Stop and wait. After my answer, reply UI_ANSWER_OK and the
   option. Do not edit files or contact external systems.” Confirm native
   `item/tool/requestUserInput` reaches attention. Click the widget's attention
   button, choose Continue, then Answer. Verify the actual agent transcript.
7. **Follow-up.** Send a widget chat message requesting `UI_FOLLOWUP_OK` and the
   selected option. Verify the response and idle status. This must reach real
   Codex 0.161.0, not the fake process or just an initialization handshake.
8. **Recovery.** With a new test question pending, reload/reopen the Work chat and
   verify attention persists. Before a service restart, check there are no
   unrelated live/idle app-server sessions. Restart only the intended service,
   verify old attention becomes stale, and verify a duplicate/stale answer is
   rejected. Explicitly Resume the same run; verify retained worktree/history,
   a fresh question ID, and a successful new UI answer. Restart the gateway
   separately and verify authenticated widget polling recovers without new login.
9. **Finish and record.** Leave test runs idle or explicitly interrupted, retain
   useful history/worktrees, and note every result and blocker. Do not delete
   dirty worktrees, close the owner's Chrome, or log the owner out as cleanup.

The successful historical test run is
`09f11628428c72dea969f0f860b6334d`, in
`~/.local/share/code-factory/worktrees/09f11628428c72dea969f0f860b6334d`.
Its question, UI answer, follow-up and restart recovery are recorded in
[the dated browser evidence](evidence/2026-10-20-work-browser-acceptance.json).
Do not assume that run is still a disposable test or blindly reuse its IDs.

## Install or update the complete browser package

Only repeat registration when the existing connection is absent or broken.
The supported flow is **Plugins → Add → Add custom MCP server → Server URL →
OAuth → Create as a plugin**. Use the current 443 endpoint and approve on private
8444 as the Tailscale owner. Never enter keys in chat or disable authentication.
Capture the observed remote app ID, then:

```sh
code-factory link-plugin --app-id OBSERVED_ASDK_APP_ID
python3 scripts/package_plugin.py --registered-app-id OBSERVED_ASDK_APP_ID
```

`/code-factory/plugin-browser.zip` serves the linked browser package. It contains
`.app.json`, both skills and branding; it omits the desktop MCP launch files.
`/code-factory/plugin.zip` is the separate portable/local-host package. Do not
upload the latter expecting it to create a browser OAuth connection.

In ChatGPT choose **Add → Upload plugin archive**. The observed dialog is
**New Plugin**, with `input[type=file][name=plugin-archive]`, accepting ZIP.
The controller does not expose `upload_file`. A proven UI automation alternative
is to load **only the generated intended ZIP** in a temporary 0600 function source
file, construct a `File` from its base64 bytes, place it in `new DataTransfer()`,
assign `input.files`, and dispatch a bubbling `change` event. Invoke this through
`evaluate_script.sourcePath` pointing under `/tmp`. Return only filename and size;
never expose encoded contents or upload private state/profile files. Then click
**Add plugin**, inspect the imported Apps/Skills/version, and **Install plugin**.

Test the actual installed package in a fresh Work conversation, including its
setup skill and starter prompt. A working standalone connector or ZIP validation
alone does not prove the complete package works. Keep creation/installation
private; do not publish to a public directory as part of testing.

## Events, blockers and truthful acceptance

Current observation: after Refresh tools and in fresh Work chats, the host said
MCP Events subscriptions were unavailable; zero subscriptions were stored.
This is not proof of a permanent account restriction or server defect. Recheck
when host capabilities change. Do not replace host callbacks with scheduled
polling and call that successful Events acceptance.

When available, confirm discovery of `inbox.changed`, `attention.required` and
`run.state`. Ask a Work chat to subscribe to `attention.required` with an
`entity` filter for the **current test run ID**, read that run on arrival, and
show its pending question without answering it. The host must create the actual
callback and signing secret. Verify a stored subscription and verification
timestamp before triggering attention. Verify signed delivery, the actual Work
response, other-entity filtering, renewal/restart, and host unsubscribe. Never
manufacture a subscription through a local fixture and present it as Work proof.

Read SQLite only with `mode=ro` when needed. Inspect explicit test run IDs and
only nonsecret subscription fields such as event name, filters, timestamps,
cursor and status. Do not print whole rows: subscription data can contain callback
URLs and secrets. `verifiedAt`/`deliveredAt` alone do not prove a Work response.

The remaining readiness checks are authoritative in [acceptance.md](acceptance.md):
real Events lifecycle, one-hour connected soak, coordinated logout/login,
remaining current-version inbox/priority/cleanup matrix and host CSP enforcement.
Use verified/failed/unverified separately. A short session with deliberate
restarts is not a one-hour soak.

Record nonsecret evidence tied to the installed version:

```sh
code-factory record-evidence --checkpoint question.answer --state verified \
  --evidence 'DATE: Work conversation reference, observed UI result and test run ID'
```

Use only supported checkpoint IDs from `internal/setup/evidence.go`. Update
`docs/evidence/` and `docs/acceptance.md`; never mark the pilot ready because local
tests or the core workflow passed while required host checks remain missing.

## Known failures and recovery rules

- **App creation 500 after 30s/120s on 10000:** the 443 scoped-path A/B succeeded.
  Preserve the current standard 443 arrangement. This does not establish a general
  ChatGPT port ban; do not blame an OpenAI outage without independent evidence.
- **Private form 403, Invalid approval origin:** native forms under `no-referrer`
  can send `Origin:null`; ordinary fetch-based SDK tests can miss this. Keep the
  tested private GET `strict-origin` policy and strict Origin/nonce checks.
- **OAuth 303 blocked in Chrome:** form-action must permit the exact validated
  ChatGPT callback as well as self. Do not allow arbitrary redirect origins.
- **Public TLS EOF/reset:** test every public DNS address with fresh verified TLS
  and SNI. Tailnet DNS can bypass ingress; HTTPS socket pooling can test the same
  IP twice. The independent scripts account for both. A Tailscale restart affects
  all node services; do not loop restarts or change ACLs speculatively.
- **Old answer after restart:** rejection is expected. Resume explicitly and
  answer the newly created request; never replay the expired one automatically.
- **Missing browser access:** require only the missing native enable/Allow or
  owner sign-in. Do not ask the owner to reproduce the whole failing workflow.

The optional isolated browser lab remains documented in
[browser-lab.md](browser-lab.md). It uses a different profile, loopback 9223,
private 8445 UI and its own Xvfb. Prefer the already signed-in real Chrome; never
mistake the lab's Chromium for the owner's authenticated Chrome session.

References: [official OpenAI testing flow](https://developers.openai.com/plugins/deploy/connect-chatgpt),
[official MCP Events tests](https://developers.openai.com/plugins/build/mcp-events#test-in-chatgpt),
[Chrome owner-approved auto-connect](https://developer.chrome.com/docs/devtools/agents/use-cases/auto-connect),
[GNOME remote login](https://help.gnome.org/gnome-help/remote-login.html).

## Admin monitoring and Codex plugin controls · 2026-10-22

The admin feature adds system review defaults, repository policy/pipeline overrides,
appended repository review instructions and explicit plugin maintenance through
Settings, owner tools and the bundled admin skill. Queue-only remains the default;
plugin update checks never install releases automatically. Refer to
[plugin administration](plugin.md#repository-monitoring-and-plugin-administration)
for configuration fields, update-status limits and durable operation behavior.

Use the private pilot only for browser and model acceptance. Do not enable auto
review on unrelated repositories for testing. Before deployment check live runs,
retain the prepared PR-review test and existing drafts, and preserve provider
authentication, plugin choices and the full Tailscale route set. Plugin refresh
is sufficient for read-only inventory checks; installing arbitrary third-party
plugins requires the owner's actual choice. Do not replay uncertain operations
or claim a remote plugin is current when its release metadata is unavailable.

Source verification and any installed-version observations are recorded separately
in acceptance. New-build callbacks, soak, login and manual accessibility remain
open until actually observed.

## Installed admin widget · 2026-10-22

Build **0.1.0-2c910ee0442b** is installed from a checksummed release; reviewed
reconciliation, `make specs-check` and full branch CI against `605b475` passed.
The [admin evidence](evidence/2026-10-22-admin.json) records 74 browser tests,
Go race checks, isolated pinned-CLI installation of all three plugin skills,
and bounded actual-host admin-widget verification. Fresh Work Settings loaded
the exact release JavaScript, remained Connected with no alerts and displayed
repository/system policy controls, custom review instructions, six installed
plugins and 30 skills after a successful read-only inventory refresh.

Provider paths, full Tailscale routes, all retained runs and the prepared pilot
PR-review snapshot are unchanged. No agent, third-party plugin installation or
policy change was started by the live check. Defaults remain queue-only, repo
inheritance and manual metadata checks. Full raw public OAuth/MCP and independent
SDK checks passed both ingress peers and revoked ephemeral credentials.

Hosted package 0.2.3 includes the new admin skill and preserves the registered app
mapping. The existing private cloud package is still 0.2.2 (two skills); it was
not replaced. The admin widget/tools work through its refreshed connection, and
the new admin skill is available in the rebuilt 0.2.3 ZIP. Remote catalog update
versions remain explicitly Unknown when the stable provider does not expose them.
No live third-party skill execution or automatic-request launch, new-build soak,
callback, login or full manual accessibility acceptance is claimed.

## Repository review listing recovery · 2026-10-23

Installed **0.1.0-9a17c48411d7** fixes sequential repository/enrichment work and
oversized dashboard reads. Three repository workers use at most four item workers
each; reference backfill yields after ten seconds and resumes on its next pass.
A failed enrichment keeps the repository checkpoint. Shared reads omit bulk
activity/raw checks and full PR check lists; selected-item detail retains them.

The [recovery evidence](evidence/2026-10-23-repository-review-recovery.json) separates
76 passing browser fixtures, Go race tests, fresh specification records and full
CI against `59d662f` from actual Work observation. The original snapshot was
33,954,766 bytes; the final installed snapshot was approximately 3.8 MB. After
Refresh tools, the real Review PRs view was Connected and displayed **two**
`scylladb/gocql` requests and **nine** `scylladb/python-driver` requests, with the
exact release JavaScript. All 12 retained runs, including the prepared pilot
review, runtime/provider configuration and complete Tailscale routes were preserved.
No agent was started or repository policy changed. Both public peers passed raw
OAuth/MCP and independent SDK checks; ephemeral credentials were revoked.

Initial imports and repeated daemon warmups exhausted the GitHub REST budget.
Imported rows remain visible with a rate-limit alert; the two added repositories
retry after **2026-10-23 04:23:28 UTC / 00:23:28 local**. Successful fresh polling
after that reset remains unverified. Do not force repeated manual polls or change
authentication to bypass the cooldown. Existing callback, automatic/model/skill
launch, soak, coordinated login and manual accessibility gaps remain separate.

## GitHub API quota controls · 2026-10-23

Installed **0.1.0-e6423eaeb939** replaces broad per-PR polling with read-only
GitHub notification hints and periodic lightweight repository reconciliation.
The default shared application allowance is **600 HTTP attempts per rolling
hour**, with a **100 reported quota units reserved**. Notification checks default
to **two minutes**; repository reconciliation runs every **15 minutes**. System
Settings exposes these values and cached accounting. Manual polls and selected
reads share the serialized persistent gate; cache hits make no API request.

The [API-usage record](evidence/2026-10-23-github-api-usage.json) separates 80 passing
browser fixtures, Go race checks, fresh layered-spec records and full CI against
`59d662f` from actual installed Work observation. A 100-PR fixture imports using
two list calls without per-PR history reads. Fixtures cover restart-preserved
budgets/validators, reported reserves, primary/secondary and GraphQL cooldowns,
truncated-body headers, notification polling/cursor changes and deduplication,
authentication-safe redirects, private Git transport and legacy revision identity.

The actual Settings widget was Connected with no alerts, showed 600/100/120-second
controls and cached quota, and loaded the exact release JavaScript. The final
update preserved all 91 still-recent recorded requests (92 after the next daemon
read), all 12 retained runs, runtime/provider configuration and complete Tailscale
routes. A bounded earlier sample recorded 31 application requests in 29 minutes.
Notification reads succeeded using existing credentials; no GitHub notification
read state, repository policy, agent run or plugin installation was changed.

The cap covers the daemon's own API reads. Plugins, skills, agent commands and
other clients remain independent; their usage must be assessed separately. The
reserve uses GitHub's reported remaining quota. Earlier unaccounted calls are not
reconstructed, and the admin counter is a cached Settings snapshot. No new pilot
notification was manufactured and no production PR action was performed. Actual
new-event intake, model/automatic/skill review execution, callbacks, soak, login
and complete manual accessibility remain separate acceptance checks.

## Discoverable manual PR review · 2026-10-23

The model-visible `prepare_pr_review` tool selects repository, system and built-in
review defaults, retains selected skills/model, pins the PR head and appends the
repository prompt. It prepares read-only/ask work without GitHub reads, a workspace
or an agent start. The daily skill now spells out discovery, preparation, snapshot
inspection, authorized `start_run`, monitoring and uncertain-start recovery; it
also documents the older generic-launch path. Package 0.2.4 includes the updated
skills and a PR review starter prompt.

Refresh tools on the existing registered connection after deployment, then use a
fresh Work conversation. Refreshing tools exposes the server recipe and dedicated
preparation tool; replacing the uploaded cloud archive is separately necessary
to replace its bundled skill text. Preserve existing cloud mapping and sessions.
Queue-only monitoring permits a requested manual review; posting to GitHub still
requires its publishing authority. Do not enable automatic policy as a workaround.

Focused application and MCP fixtures cover pipeline precedence, pinned target,
profile/skill retention, explicit-only Start, repeated Start rejection, unsafe
profiles and invalid targets. See the [review discoverability evidence](evidence/2026-10-23-pr-review-discoverability.json)
for version-bound verification and installation results. Existing real-host
callback, soak, login and accessibility gaps remain open; fixtures alone do not
establish that a conversational model chose the intended tool.

Build **0.1.0-ead7ae360111** is installed from the checksummed release after
`make specs-check` and full CI against `59d662f` passed (80 browser fixtures, Go
race, packaging, installer recovery and compiled smoke). Local authenticated
discovery exposes all 30 tools, including model-visible `prepare_pr_review`.
All 14 retained runs, provider paths, registered app mapping and complete Tailscale
configuration are unchanged; both services are active. The installed source/hosted
bundle is 0.2.4. The existing uploaded cloud package remains 0.2.2; tool refresh
updates server capabilities without replacing its bundled skills. The evidence
record separates this installation check from conversational tool-choice checks.

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

The [sandbox evidence](evidence/2026-10-23-review-sandbox.json) separates fixture
checks, real pinned-provider command-only checks and installation observations.
Native tests verify GitHub TLS without an HTTP API request, denied other/private
destinations, blocked direct localhost access, read-only review files, protected
Git metadata, workspace-only implementation edits, inaccessible unrelated files,
unchanged host files and persistent private scratch. They also verify existing
GitHub authentication and local reviewer configuration without printing tokens.
Linux may provide private synthetic parent directories inside the namespace;
writes there are checked against the unchanged owner host files. These command
checks do not establish a full model-led third-party review pipeline or close
callback, soak, login, nested runtime or accessibility acceptance gaps.

Installed **0.1.0-16c572cbc34d** passed a real `gpt-6.1-sol` check through the
private pilot run: Git and existing GitHub auth read, source write denial,
unrelated-file read denial, writable scratch and GitHub TLS all succeeded
without escalation. A forbidden domain was separately observed blocked. Native
checks also cover direct localhost/private destinations and protected Git
metadata. Shell `TMPDIR` and zsh `TMPPREFIX` both use private scratch; the first
candidate exposed a heredoc-temp failure, now covered by a regression test.
Only the two owned pilot test workspaces/scratch were cleaned. Original 14 runs,
global Codex configuration, provider paths, registered mapping and Tailscale
routes remain unchanged. Full PR review and third-party nested-runtime acceptance
remain separate from this bounded model-backed command check.
