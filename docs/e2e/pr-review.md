# PR review browser E2E campaign

Created 2026-10-23. Status: **running; readiness repairs and browser execution in progress**.
This is an executable acceptance plan for the agent operating the existing
signed-in pilot Chrome profile. The agent performs setup, browser interaction,
repairs and reruns. It is not a checklist delegated to the owner.

These use cases describe acceptance targets, including work still needed. They
map to existing [behavior contracts](../user-flows/README.md); they do not declare
new product behavior implemented or close existing specification gaps.
[Acceptance](../acceptance.md) remains the ledger of observed results.

## User outcomes

An owner should be able to find the PR that needs attention, ask for the configured
review in ordinary language, understand what will run, get useful findings from
both selected reviewers, discuss those findings, and deliberately decide whether
to publish. Preparation, opening a dashboard and reading results must not start
unexpected work. Failed or incomplete reviews must be distinguishable from clean
reviews. A changed PR, interrupted session or failed connection must not lose the
owner's context or silently repeat actions.

## Actual execution boundary

- Use `node scripts/browser_control.mjs --pilot`, the existing persistent profile
  and existing connected complete Code Factory plugin. Create dedicated campaign
  tabs/chats. Preserve unrelated tabs, authentication and drafts. No profile copy.
- Use only `dkropachev/code-factory-pilot-20261019` for synthetic PRs, changes,
  review comments, policy changes and runs. The three plugin repositories are
  dependency sources; upstream repositories remain unchanged. Required dependency
  repairs use isolated local compatibility checkouts, reviewable patches and the
  native installer. Never edit an installed cache or publish a dependency change
  as part of this campaign.
- Begin each use case through actual ChatGPT Work conversation or its embedded
  Code Factory UI. Observe rendering, actions and the real Codex result directly.
  Owner MCP, local files and GitHub reads may corroborate evidence and prepare
  fixtures; they cannot substitute for the browser path being tested.
- Initial sign-in/native browser consent belongs to the owner if actually needed.
  Reuse the current approved signed-in session. Pause inspection during sign-in.
- Reviews use read-only source, ask-before-publishing and private scratch. Never
  broaden to full access to make a failing integration pass. Tests use the pinned
  authenticated Codex 0.161.0 recorded by the installation.
- Before maintenance/restart, identify unrelated live runs. Scope automatic policy
  changes to the pilot repository; preserve system and other repository policies.
  No desktop logout, account switch, route reset or production action is required.
- No review publication follows merely from “review this PR”. The publication case
  uses a separately explicit instruction and a pilot-only COMMENT review. GitHub
  prevents an author from approving or requesting changes on their own PR; test
  those verdicts only with an independently authorized collaborator identity.

## Integration to make ready

Code Factory owns the inbox, profile/snapshot, isolated run, attention UI and
conversation. [dk-workflows](https://github.com/dk-code-factory/dk-workflows) owns
review context, Decisions, ordered reviewer execution, validation and aggregation.
Its `dk-pr-review` skill invokes these two native Codex reviewer bindings in order:

```json
[
  {"skill": "code-review-ad:code-review-ad", "host": "codex"},
  {"skill": "code-review-matpocock:code-review-matpocock", "host": "codex"}
]
```

Install the three independent marketplaces/plugins through Code Factory Settings
or its administration tools. These identifiers come from their manifests:

| Source | Marketplace | Installed selector | Inspected revision / version |
| --- | --- | --- | --- |
| [dk-workflows](https://github.com/dk-code-factory/dk-workflows) | `dk-workflows` | `dk-workflows@dk-workflows` | `18edf9c7c31e7309e26062e1a069fe038ffe001d` / 1.1.17 |
| [AD](https://github.com/dk-code-factory/code-review-ad) | `code-review-ad` | `code-review-ad@code-review-ad` | `5900df7e96463f0fc68a5c3c0d5e0dcfdf36cd95` / 1.0.7 |
| [Matt Pocock](https://github.com/dk-code-factory/code-review-matpocock) | `code-review-matpocock` | `code-review-matpocock@code-review-matpocock` | `4a7254304ed8984a8028e577863c238ce24b0055` / 1.0.0 |

Re-read installed manifests and record resolved revisions at execution; these
inspected revisions are preparation evidence, not a claim of installation.
Configure reviewers with the installed DK configuration helper at **pilot repo
scope**, never user scope. Inspect current settings first and retain their exact
previous values privately. DK has no implicit default reviewer. Preserve its
lifecycle entry/completion, retained runtimes, complete engineering preparation,
pinned base/head and Decisions requirements. An incomplete provider is not a pass.

Create a dedicated `pilot-pr-review` profile with selected installed
`dk-pr-review`, read-only sandbox and publishing `ask`. Resolve reviewer packages
through their real registry; do not paste reviewer instructions into replacement
skills. The reviewer list belongs to DK configuration, not to a list of unrelated
Code Factory skills. Assign this profile only to the pilot repository initially.
Use the model already selected by the owner/provider; record its actual identity.

Suggested profile prompt:

> Use the installed dk-workflows dk-pr-review workflow for {{url}}. Apply the
> pilot repository's configured ordered reviewers: code-review-ad and
> code-review-matpocock, both on Codex. Read the PR description, linked scope,
> repository standards and active Decisions. Pin base/head, run both reviewers
> and retain their validated results. Report findings with file/line evidence,
> reviewer completion and current-head identity. Do not edit the source, push,
> post a review or resolve threads. If a required input or provider fails, report
> the incomplete stage and recover without inventing a clean review. Use private
> scratch for workflow artifacts and temporary checkouts.

## Readiness work and entry gates

| ID | Observed state or open prerequisite | Work and proof needed before dependent cases |
| --- | --- | --- |
| R1 | Browser attachment and sign-in work; no active app runs at preflight | Record binary/version, baseline 16 retained runs, pilot settings and relevant artifact identities. Recheck immediately before mutation. |
| R2 | Existing complete ChatGPT package visibly reports 0.2.2, Apps 1 / Skills 2; source bundle is 0.2.4 | Update the existing uploaded package using the supported UI, preserving its registered remote app mapping. Refresh tools and start a fresh complete-plugin Work chat. Verify actual installed skill/version, tool recipe and loaded iframe bundle. A server tool refresh alone does not update bundled skill text. |
| R3 | Cached CLI inventory does not contain these three plugins | Refresh real inventory, explicitly add/install the three sources, discover skills and verify installed/enabled versions and real resource paths in Settings. Never interpret an old cache as a fresh install result. |
| R4 | Code Factory snapshots only the selected skill directory; DK native skills reference package-level `common`, `runtime` and `scripts` via relative paths | Preserve the selected package and relative entrypoint layout in durable snapshots. Add focused regressions for package resources, executable modes, bounds, traversal/symlink rejection, changed installed packages and retained resume. Deploy a verified release before claiming browser success. |
| R5 | Full DK nested reviewer runtime has not been exercised in the bounded task sandbox | Verify retained runtimes and lifecycle state can be written privately; registry, reviewer packages, required executables and existing auth can be read; native reviewer execution works without source writes or unrelated-owner-file access. Provide only required scoped access. Do not change global auth/config or permit general localhost/private networking. |
| R6 | Pilot has no dedicated configured two-reviewer profile | Save isolated pilot reviewer configuration/profile/override; confirm ordered bindings resolve to requested installations. Verify prepared snapshots retain those choices and repository instructions. Record previous values for restoration. |
| R7 | Fresh deterministic PR fixtures are needed | Create campaign-owned branches and PRs with explicit scope, standards, known defect oracles and a clean control. Record actual PR URLs/base/head and independently establish expected behavior. Never reuse an unrelated retained run. |
| R8 | Genuine incoming review-request event needs a second eligible actor | Check whether an already authorized collaborator/test actor is available. If absent, keep incoming-request/automatic-launch and non-author verdict cases blocked with an exact setup task. Self-created local hints or fixture notifications are not real GitHub event acceptance. |
| R9 | Review publication needs a completed, current, validated review | Exercise DK's separate publication skill on the pilot only after explicit instruction. Preserve review-session/result evidence; reconcile uncertain publication with GitHub before retry. |

R4 is established by source inspection, not by a failed installed model run.
R5 is a compatibility risk to verify, not a claim that every nested command fails.
No readiness item is complete merely because this document describes it.

## Pilot fixtures and independent oracles

Use one campaign prefix with the UTC date/time. Keep branch/PR/run ownership in the
campaign ledger, and retain useful history rather than deleting fixtures blindly.

- **Defect PR:** a tiny Python or Go example with one clear repository-standard
  violation and one functional boundary defect against an explicit checked-in spec
  and linked pilot issue. Establish failing boundary examples before review and
  record their expected outputs. The PR body defines review scope and a settled
  decision that must be respected. The defect lives on added lines so validated
  findings can point to the pinned patch. The reviewer must provide evidence;
  exact prose or exact finding count is not the oracle.
- **Clean PR:** the equivalent correct implementation with passing boundary
  examples and no seeded rule violation. Both reviewers must actually finish.
  “No actionable findings” is acceptable only for complete current evidence.
- **Changed-head variant:** a new commit fixes the original defect or introduces
  a distinct one. Record old/new heads and separate expected behavior; an old
  result cannot be presented as approval of the new revision.
- **Question variant:** an explicit Plan/read-only review preparation asks one
  bounded scope question through native user input, then continues with review.
  Do not depend on a model spontaneously asking a question.
- **Two-PR isolation:** keep defect and clean PRs open simultaneously, with distinct
  titles and drafts, to detect cross-target launch, transcript or reply leakage.
- **Incoming request:** use the independently authorized actor for a real request
  on a nondraft pilot PR. Do not manufacture notifications in private daemon state.

## Use cases

See the campaign ledger for current case status. Execute in the order below, retaining a result for each
variant. A blocked case is reported with a readiness dependency and next action.

### PR-01 — Connect and recognize the installed review integration

**Want:** Open the app I installed and know it is connected and current.
**Given:** R1–R3 pass; existing approved profile and account.
**When:** Open the complete plugin, choose Try in chat and Work, and ask “Open Code
Factory's PR dashboard without launching an agent.” Open Settings separately.
**Then:** Actual iframe is Connected, pilot PRs render, expected versions/skills
are visible, and the retained-run count does not grow. Settings does not destroy
inbox selection or drafts. Record installed bundle identity and visible controls.
**Recovery:** Repair metadata/package mismatch, reopen a fresh campaign view and
rerun. Keep working connection/account mapping. Maps: install-connect, configure-work.

### PR-02 — Find the right PR and inspect full context

**Want:** Find my PR or one awaiting my review without losing track of the target.
**Given:** R7; two distinct pilot PRs. R8 for genuine Review PRs membership.
**When:** Search by repository/number/title, use My PRs and Review PRs as applicable,
open the PR, inspect scope, head, checks and review state, then go Back.
**Then:** Correct target and external link, full selected detail, preserved query
and row context; lifecycle, CI and approval remain separate facts. Listing and
selection launch nothing. A failed detail refresh remains visible and unread.
**Recovery:** Retry the failed detail read once available; no hidden launch.
Maps: sync-inbox, triage-inbox, relate-items, view-live-work.

### PR-03 — Prepare a review without starting it

**Want:** Inspect the exact work and authority before deciding.
**When:** In a fresh chat ask “Prepare the configured review of <pilot PR URL>.
Show its pipeline, reviewers, pinned head and authority. Do not start it.”
**Then:** One durable prepared snapshot uses pilot profile, selected DK skill,
ordered reviewer configuration, repository instructions and current PR head.
No workspace, running model or GitHub publication is created. Reopen the draft
and observe the same proposal rather than another preparation.
**Recovery:** Missing/unsafe profile or skill produces a readable error and no
start. Maps: launch-agent S1/S3/S6, configure-work S2/S4.

### PR-04 — Launch the configured review in natural language

**Want:** Say “review this PR” without knowing tool names or manual setup steps.
**Given:** R4–R7; configured defect PR, queue-only monitoring.
**When:** Ask “Run the configured Code Factory review pipeline for <pilot PR URL>.
Use both configured reviewers and report here. Do not publish or change code.”
**Then:** ChatGPT discovers the real tools, prepares/inspects the matching proposal
and starts it under this instruction. Exactly one run and worktree appear, pinned
to the correct PR/head, with read-only/ask authority and live browser progress.
No needless second authorization prompt is required for the already requested
matching start. Record tool sequence and actual run identity.
**Recovery:** On uncertain Start, inspect the same run; never replay blindly.
Maps: launch-agent S2/S6/S7, view-live-work.

### PR-05 — Run AD and Matt Pocock through DK and receive useful findings

**Want:** Evidence-backed review rather than a plausible summary with missing work.
**Given:** PR-04; independent defect oracle.
**When:** Wait in the embedded conversation through real preparation and reviewer
completion; inspect normal results and the Details disclosure as needed.
**Then:** DK evidence identifies the same pinned base/head and scope/Decisions;
both requested providers have completed validated result artifacts. AD's actual
passes and Matt Pocock's Standards/Spec axes execute. Reported seeded defects have
correct file/line and consequences, and settled Decisions are respected. Skips,
limits and partial passes are explicit. Source checkout remains unchanged; private
scratch works. A skill name in a prompt alone is insufficient proof of execution.
**Recovery:** Fix package/runtime/provider integration, verify, deploy and rerun the
same failed path through a fresh browser run. Preserve failed evidence.
Maps: launch-agent S3/S7, configure-work S2/S5, protect-secrets, view-live-work.

### PR-06 — Receive a completed clean review

**Want:** Distinguish a reviewed clean change from an incomplete review.
**When:** Review the clean control via the same configured browser path.
**Then:** Both reviewers complete against its current head and explicit scope.
No seeded defect from the other PR leaks into the result. A clean summary carries
completion evidence; no missing-spec/provider error is relabeled as clean.
**Recovery:** Investigate false findings against the independent oracle; rerun
only after resolving the cause. Maps: launch-agent, view-live-work.

### PR-07 — Honor pipeline overrides and keep review targets isolated

**Want:** Use my repository's review setup while keeping work on two PRs separate.
**When:** Inspect repository-over-system precedence in Settings and a prepared
snapshot; open both PR conversations, edit separate drafts, and return between
them. An explicit reviewer-specific diagnostic run may isolate AD or Matt Pocock.
**Then:** Pilot override wins, both bindings remain ordered in the ordinary
pipeline, drafts/results/attention belong to the correct PR/run, and existing
prepared work retains its original choices after settings change. No unrelated
system/repository setting changes. Restore temporary pilot overrides.
**Recovery:** Missing default requires explicit selection; never silently swap
reviewers or profiles. Maps: configure-work S2/S4, launch-agent S3/S4, interact-agent.

### PR-08 — Answer a real review question in the embedded UI

**Want:** Clarify review scope and let work continue without switching tools.
**Given:** Question variant using explicit Plan/read-only/ask authority.
**When:** Request a native Continue/Stop scope question before review; navigate to
Messages, answer Continue in the actual form and return to the PR conversation.
**Then:** The real question appears once on its owning run, reply is delivered to
real Codex, attention clears for that question, and review continues with the
selected scope. Stop variant stops that requested work rather than authorizing
publication. Secret answers, if used, must stay out of evidence.
**Recovery:** If native form fails, exercise bounded repair/free-text fallback and
record which path actually worked. Maps: answer-attention, interact-agent.

### PR-09 — Discuss findings and continue the same review

**Want:** Ask “why does this matter?” without launching a new review accidentally.
**When:** Send an embedded follow-up asking for a boundary example for a finding;
leave and reopen the item. Interrupt the campaign run, then send a specific
continuation from that conversation.
**Then:** Answer cites correct PR/head/finding. Same run, thread, workspace and
history persist, user drafts survive navigation, and one submitted message causes
one continuation. Inactive restoration does not create a substitute run.
**Recovery:** Uncertain Send preserves draft and reconciles accepted delivery
before retry. Maps: interact-agent, recover-cleanup, view-live-work.

### PR-10 — Recover a page reload and a transient read failure

**Want:** Keep my review and pending answer when the browser connection hiccups.
**When:** Reload/reopen only the campaign chat with a question pending. Use one
scoped, reversible read-reply fault in the campaign widget if needed to exercise
transport recovery; remove the probe immediately afterward.
**Then:** Same retained question/run and history return; a recoverable read retries
and its alert clears after success. Start, Send, answers and saves are never
replayed by the read recovery. Record simulated fault and actual outage separately.
**Recovery:** Check the same operation's persisted outcome before explicit retry.
Maps: answer-attention, recover-cleanup, view-live-work.

### PR-11 — Handle a changed head or changed review scope

**Want:** Know whether findings still apply after the author pushes a fix.
**When:** Advance only the campaign PR branch after preparation/during review;
refresh the PR and request review of the new head. Separately revise the fixture's
scope/Decisions and request a fresh review.
**Then:** Old artifact remains attributed to old head/context. Fresh execution
pins new evidence; old completion is not reused across changed head/scope/Decisions.
Any publication of a stale result is blocked pending reconciliation. A stale
prepared target is surfaced honestly; it must not silently claim the new head.
**Recovery:** Refresh context, inspect the new proposal and run the requested
current review. Maps: launch-agent, sync-inbox, relate-items.

### PR-12 — Reject unavailable, unsafe or incomplete review paths

**Want:** A clear recoverable failure instead of “no issues found”.
**When:** Use a campaign-only invalid profile/disabled skill variant; test missing
reviewer binding, missing scope, provider error/limit and invalid reviewer output
with isolated fixtures or a dedicated campaign configuration. Never revoke global
auth or remove an installation used by other work to manufacture failure.
**Then:** Failure stage is visible, no unsafe start/publishing occurs, and
incomplete/skipped reviewers are accurately reported. At least one completed
reviewer never conceals the other provider's infrastructure/validation failure.
**Recovery:** Repair/configure the exact dependency; rerun and observe both real
providers. Controlled fixtures support failure coverage but do not replace the
successful live integration cases. Maps: configure-work, launch-agent, view-live-work.

### PR-13 — Publish only the explicitly selected completed review

**Want:** Deliberately share useful findings on GitHub after reading them.
**Given:** R9 and a completed current-head review. All preceding cases publish
nothing. Use a distinct explicit instruction at this case's start.
**When:** Ask “Post this completed review as a COMMENT review on <pilot PR URL>.
Do not approve, request changes or resolve threads.” Inspect the intended content
and satisfy any concrete publication choice required by the selected workflow.
**Then:** DK's publication workflow checks current provenance and emits one
pilot review with correct line anchors. GitHub/browser observation corroborates
content and identity. Reopening results posts nothing. An uncertain result is
reconciled against remote reviews before retry; no duplicate publication.
**Recovery:** Stale/invalid evidence blocks posting; refresh/review before retry.
Non-author approval/request-changes variants require R8.
Maps: launch-agent authority, protect-secrets; DK post-review contract.

### PR-14 — Read feedback and reconcile settled decisions

**Want:** Include existing discussion without repeating settled objections.
**When:** Add identifiable campaign-only feedback/decision evidence, ask the
configured workflow to read unresolved comments and review the PR again. Request
reply publication only as a separate explicit pilot action.
**Then:** Current threads and settled decisions appear in preparation; superseded
history is distinguished. The review does not resolve threads, edit PR body or
post replies without the corresponding request. Published reply, if requested,
is in the intended thread exactly once. Code fixing/iteration is a separate
workspace-write workflow, not an implicit review side effect.
**Recovery:** Reconcile incomplete/stale discussion reads; preserve pending work.
Maps: interact-agent, relate-items; DK read-comments/Decisions/reply contracts.

### PR-15 — Intake a real incoming request and optionally review automatically

**Want:** See a newly requested review and choose whether review should start.
**Given:** R8; authentic collaborator request on a nondraft pilot PR.
**When:** Observe request in queue-only mode, then enable automatic review only
for this pilot repo and observe one new eligible request episode/head. Poll,
reopen and, when safe, restart without creating another request.
**Then:** Queue-only never launches; opted-in eligible request starts once using
the same configured read-only/ask pipeline and both real reviewers. Repeated
poll/restart does not duplicate its claim. Draft, pause, stale metadata, removed
request and other active work prevent the inappropriate launch. New head/request
episode is separately attributed. Do not enable automatic policy globally.
**Recovery:** Restore exact prior pilot policy after observation. Without R8,
retain this case as blocked, never substitute synthetic local notifications.
Maps: sync-inbox, configure-work S4, launch-agent S5.

### PR-16 — Respect request budgets and show unknown provider facts

**Want:** Keep access to saved reviews when GitHub is unavailable or quota is low.
**When:** Inspect Settings accounting and cached PR data. Exercise a campaign-only
controlled provider failure/cooldown; do not deliberately exhaust shared GitHub
quota or repeatedly force manual polling.
**Then:** Saved rows/results remain usable, inaccessible checks/review state are
unknown or visibly stale, retry timing is explained and cached reads do not
consume requests. Detail failure does not mark an unseen revision read. Independent
skill/reviewer requests are not misrepresented as part of the daemon's API cap.
**Recovery:** Retry after the actual permitted interval and confirm current data.
Maps: sync-inbox, configure-work S6, triage-inbox, relate-items.

### PR-17 — Recover the same review after a safe service interruption

**Want:** Continue retained work after the daemon or gateway reconnects.
**Given:** No unrelated live sessions; identify all campaign runs first.
**When:** Interrupt/restart only the intended service with a campaign question
pending; reopen the existing signed-in widget and send an explicit continuation.
Test gateway restart separately. Never restart Tailscale for this scenario.
**Then:** History/workspace remain; stale question answers are rejected and a fresh
question is actionable. Same retained run resumes, old Start is not replayed,
widget reconnects without sign-in, and both reviewers retain valid runtime context
or report the exact stage needing a fresh review.
**Recovery:** Diagnose/recover owned service while preserving data/auth/routes;
rerun dependent cases on the repaired installed version.
Maps: recover-cleanup, operate-installation, answer-attention.

### PR-18 — Finish work without losing evidence or changing unrelated state

**Want:** Stop safely, revisit findings later and clean only work I choose.
**When:** Interrupt only campaign runs, revisit their retained history and perform
explicit clean cleanup on one identified campaign workspace. Exercise dirty
cleanup refusal in an isolated fixture or a campaign-owned writable diagnostic.
**Then:** Correct history remains visible, clean scratch/worktree cleanup is scoped,
dirty work is preserved, and all prior noncampaign runs/settings/auth/routes are
unchanged. Restore temporary pilot policy/configuration and remove fault probes.
Record which added plugins/profile/fixtures remain intentionally available.
**Recovery:** Restore a mistakenly changed campaign setting from its private
baseline; never delete dirty work or unrelated runs to achieve a clean finish.
Maps: recover-cleanup, operate-installation, protect-secrets.

## Execution, repair and evidence

Run R1–R7 first, then PR-01–PR-12, PR-13–PR-14 under their explicit publication
instructions, PR-15 when R8 is available, and PR-16–PR-18. Keep incoming-event and
non-author verdict variants visibly blocked until their actor exists. Each case
is incomplete until every stated variant has an observed result or an explicit
blocked reason. Keyboard operation, focus after navigation/dialog close, readable
errors and narrow-view layout are checked during the exercised browser paths;
this is not complete manual accessibility acceptance.

For every failure: retain the original symptom/version and evidence; reproduce
with a focused local regression when practical; fix the responsible integration;
run appropriate static/spec/test checks; build/package and install a verified
release if runtime code changed; refresh tool metadata and reopen a fresh view;
rerun the failing browser path plus dependent cases. Continue the campaign after
a successful repair. Never report an unexecuted rerun as a pass. Separate failures
in Code Factory, DK workflows, reviewer providers and host/transport.

Store dated nonsecret evidence under `docs/evidence/` with:

- Case/variant ID, timestamps, status (`planned`, `running`, `passed`, `failed`,
  `blocked`), failure stage, blocker owner and exact next verification.
- Installed daemon/build and actual widget/package identity; pinned provider and
  resolved plugin versions/revisions; repository, PR, base/head and campaign run ID.
- Chat path, browser-observed action/result, actual reviewer completion/result
  references and any corroborating GitHub review/notification IDs.
- Before/after assertions, original failure, fix/source revision, verification and
  installed rerun evidence. Keep both failure and recovery, rather than overwrite.
- Scope of limitations and restored/preserved state. Mask URL queries/fragments;
  never retain credentials, cookies, authorization headers or secret answers.

Use [the campaign ledger](pr-review-results.json) for the compact case inventory.
It contains dated preparation and execution observations. Reports and fixtures do not
supply browser passes. Link dated actual results from the runbook and acceptance
ledger; narrow existing gap records only for assertions actually verified.

Done means both real reviewers completed on defect and clean PRs through the
installed ChatGPT integration, core manual/recovery/publication cases passed,
every planned variant has a result, and remaining blocked checks have a concrete
owner/readiness action. Full application readiness remains separate: event
callbacks, soak, coordinated desktop login and complete accessibility acceptance
are not closed by this PR-focused campaign.
