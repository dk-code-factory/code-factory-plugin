# Retained PR review workflows

Settings → System defaults → Session execution selects `workflow` or
`single-codex` for newly prepared work. Existing installations without this field
retain workflow behavior until the owner selects a mode. The selected mode is
saved with each run; changing it never reroutes existing proposals or sessions.

Single Codex mode starts one retained native app-server session directly for
tasks and PR reviews. Select the installed `dk-workflows:dk-pr-iter` skill in
PR iteration skill; unavailable, disabled or unrelated skills are rejected.
Review preparation adds this skill and snapshots its complete package resources.
The session follows dk-pr-iter's review/CI loop, lifecycle and native child-agent
contracts, subject to the selected profile's sandbox and publishing authority.
Its messages, questions, interrupt and resume use the existing session controls.
This mode does not use Factory's retained review artifacts or automatic posting
policy; dk-pr-iter owns its private evidence and publication procedure. It is an
explicit selection and does not activate after an engine failure. Read-only
profiles can inspect but cannot perform dk-pr-iter fixes; choose an appropriate
writable profile explicitly when iteration is intended.

Workflow-mode PR reviews use `code-factory-pr-review-v1`. Existing retained runs
without that marker keep the original native-turn execution and recovery path.
No installed skill or unrelated profile is replaced.

The Go review subsystem follows the procedures pinned at DK commit
`1b0019767a29142bbb4b3855c2fc9f1e4af75b71`:
[review](https://github.com/dk-code-factory/dk-workflows/blob/1b0019767a29142bbb4b3855c2fc9f1e4af75b71/runtime/dk-pr-review/procedure.md)
and [posting](https://github.com/dk-code-factory/dk-workflows/blob/1b0019767a29142bbb4b3855c2fc9f1e4af75b71/runtime/dk-pr-post-review/procedure.md).
Pinned parity fixtures retain source hashes, reviewer responses, Decisions,
preparation analysis, JSON hashes and inline locations; their generator takes a
separate local checkout and never downloads source or invokes providers in CI.

## Production engine integration

Code Factory pins agent-workflow merge commit
`0606a1b9d16e8741bab684233ab49b96f34ea02e` (PR #36). The SDK exposes registered
Go actions, ordered dynamic iteration and durable external-input waits. The
production adapter verifies all three capabilities before evidence capture or
model work. Missing runtime dependencies fail before capture; fresh reviews never
fall back to a legacy parent model turn.

Embedded workflows register typed application actions for capture, preparation,
review, assessment and publication. Reusable preparation workflows suspend with
SDK `input/wait`; reusable reviewer children run through ordered `for-each` with
stable configured IDs. Provider failures and usage exhaustion remain classified,
so later reviewers execute while guard failures stop dispatch. Application
artifacts preserve partial findings even when the engine stops a dependent job.

Default production workers use independent Codex threads through the existing
YAML model-turn transport, retaining selected model, effort and profile controls.
Skill workers use captured package resources. Command workers receive pinned
`git-diff-v1` handoffs and bounded correction instructions through private stdin,
executing through the existing native permissions and environment controls.
Captured package bytes remain in private snapshots rather than being copied into
every model prompt. Command cancellation uses the native process identity to
terminate and join its separate process group. Model cancellation sends a native
turn interrupt and joins terminal delivery before reaping the provider; missing
admission or shutdown proof stops dispatch with an explicit retained error.
Native reviewer usage-limit failures retain exhausted-skip accounting; the same
failure in preparation, material checks or completion remains a stage failure.
Command cancellation also bounds termination requests waiting behind blocked
request writes; provider teardown joins local calls while missing child shutdown
proof keeps the durable admission barrier in place.
Individual session captures are readable; shared engine journals are not granted
to bounded workers. Child notifications cannot change parent run status.

`FixtureAdapter` remains an explicit deterministic test option. The regression
fixtures also run the production SDK adapter with fake workers, GitHub fixtures
and a fake Codex transport; they exercise the production default worker wiring.
Installed-host and real-provider acceptance remain separate opt-ins.
There is no automatic fallback for a workflow-mode review. Single Codex mode is
selected explicitly before preparing a new run.

## Execution and retained state

The workflow resolves configuration, captures and validates remote evidence,
collects preparation inputs, asks for model analysis, runs ordered reviewers,
validates and aggregates results, evaluates posting policy, then publishes and
reconciles. The application owns parent completion. Child reviewer completion
cannot finish the parent run.

Evidence contains remote base/head, full inventory, normalized final merge-base
diff, isolated checkout/tree, linked scope, published Decisions, private rules and
owner constraints. Retained skill/command bindings freeze their package resources
and execution arguments. Invalid ancestor Git markers cannot expand a standalone
command into an unrelated directory capture. Commands receive the flat pinned `git-diff-v1` handoff,
with output/correction instructions on stdin and bounded native attempt metadata.
Preparation captures are revalidated around each reviewer and final assessment.
Guard failures stop the workflow; provider failures
continue to later reviewers and keep results incomplete. A malformed response gets
one correction. Exhausted reviewers remain explicit skips; completion requires at
least one completed reviewer, no unresolved failure and an engineering completion
assessment.

Private content-addressed captures, preparation, checkpoints and result artifacts
live under the application data directory. Sessions retain the DK session-v2 and
result-v3 identity envelopes, reviewer response contract and configured-reviewer
accounting. Preparation retains DK v3 indexed captures, reviewer descriptor maps, source/tree
fingerprints, conclusion watches, lineage and recovery references. Its compact
preparation and engineering hashes follow the pinned upstream algorithms; the
session/result hashes retain the upstream spaced JSON representation. Exact human
answers are indexed governing sources and reach every reviewer. The optional
local pinned-source conformance test validates a Go-produced artifact with the
unmodified upstream validator. It requires the pinned revision, retained source
hashes and a pristine runtime tree, including no ignored bytecode or untracked
modules, before isolated Python execution. Ordinary CI checks this identity guard
with offline fixtures and never fetches the optional source checkout. Old runtime
files are not silently upgraded.
Action and model-worker implementation identities, exact head, engineering inputs and ordered bindings
must match before reuse. Only completed reviewer checkpoints can be reused.

Material questions appear through the existing attention controls. Waiting releases
execution; answers are correlated with the gate and its engineering identity and
claimed together with attention resolution. Each actual preparation, material check,
reviewer and completion-worker call first commits an execution-bound admission.
Command delegation stays inside that same worker boundary. Failed admission writes
start no worker. A matching verified worker return clears its admission before
checkpoint, gate, result or parent-finalization saves; uncertain cleanup and failed
proof persistence retain it and stop later workers. Session saves preserve this
protection. All replacement paths reject unmatched admissions, including after an
abrupt daemon crash or a failed cleanup-state write. Orchestrator exit supplies no
shutdown proof. Finalization failures remain bound to the execution joined by each interrupt
waiter, even when a later verified Resume has already replaced the live execution. No automatic clearance or recovery
override is provided. Verified shutdown, no-child waits/answers and saved finished
reviews retain their recovery paths. A
completed review with unfinished publication resumes only its remaining policy
and publication stages. Execution identities fence stale saves and finalizers.
`send_message` accepts an operational `continue` or `resume`; changed engineering
instructions require a fresh prepared review. Restart preserves unanswered gates
and immutable artifacts without replaying answers or publication. Each review
retains its SDK origin/generation and gate references beside the application
session. Its private per-session SQLite journal reopens under the session lock;
immutable admission identity recovers an interrupted origin-ID write. Accepted
answer identity and continuation intent first commit with attention resolution in
the app transaction, then reconcile into the SDK journal. A restart between these
writes preserves exactly one continuation. Evidence is revalidated before answer
consumption. Explicit interruption joins active children and prevents automatic
continuation; ordinary interrupted work requires explicit Resume.

## Settings and authority

Global and repository settings manage ordered skill/command reviewers and private
review rules. An explicitly empty repository list blocks review; null inherits.
No reviewer configuration blocks before fetching a diff. One-time DK import reads
private settings, preserves existing Factory values, reports unresolved bindings
or rule conflicts and does not modify DK or installed skills.

`autoPostReview` defaults to false globally. Nullable persistent PR and repository
overrides and an optional launch override resolve in this order:

```
launch → PR → repository → global
```

Omitted updates preserve values. Null removes the corresponding override. The
prepared view shows effective value and source. Publication checks current policy
again, including updates made during a review. For migrated reviews the server
owns publication; profile/model publishing prose cannot supply that authority.

`post_retained_review` accepts a run ID, its complete artifact reference and an
optional matching target. Automatic posting uses COMMENT, inline findings and an
empty review body. Explicit structured requests can select another event or a
summary. An empty COMMENT is skipped. Invalid inline locations are retained as
omissions; an approval with unpublished actionable findings is rejected.

Publication validates the exact head/base/body, target, current authenticated
identity, effective configuration and inline paths/sides/ranges. It retains the
approved payload, full existing-review snapshot including pending drafts,
operation identity and verified receipt. Submission intent is durable before the
mutation. An uncertain response enters reconciliation; changing an event, summary or
placement cannot create a second submission for that review. Unsubmitted stale
snapshots can be refreshed after the complete previous intent is archived.
Neither posting again nor reconciliation repeats an uncertain mutation. Unrelated pending drafts remain untouched.

Fixture verification exercises production integration only through fake external
boundaries. Real providers, installed-host acceptance and real GitHub publication
remain unverified until explicit opt-in acceptance. This change does not install
or update the running service.
