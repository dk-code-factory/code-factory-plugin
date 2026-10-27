# Retained PR review workflows

Fresh PR reviews use `code-factory-pr-review-v1`. Existing retained runs without
that marker keep the original native-turn execution and recovery path. No
installed skill or unrelated profile is replaced.

The Go review subsystem follows the procedures pinned at DK commit
`1b0019767a29142bbb4b3855c2fc9f1e4af75b71`:
[review](https://github.com/dk-code-factory/dk-workflows/blob/1b0019767a29142bbb4b3855c2fc9f1e4af75b71/runtime/dk-pr-review/procedure.md)
and [posting](https://github.com/dk-code-factory/dk-workflows/blob/1b0019767a29142bbb4b3855c2fc9f1e4af75b71/runtime/dk-pr-post-review/procedure.md).
Pinned parity fixtures retain source hashes, reviewer responses, Decisions,
preparation analysis, JSON hashes and inline locations; their generator takes a
separate local checkout and never downloads source or invokes providers in CI.

## Engine dependency

The production adapter inspects the pinned SDK's `workflows.Capabilities()`
contract and rejects execution before evidence capture or model work when required
contracts are unavailable. Code Factory now pins agent-workflow merge commit
`b90c72a38b86462730257fac48d2db093f1f8194` (PR #33), which supplies
[registered application Go actions #25](https://github.com/dk-code-factory/agent-workflow/issues/25):
validated action names, typed schemas, implementation identity, durable journaling
and recovery validation. Go action support is available and is no longer reported
as a missing dependency.

The remaining required contracts are still unavailable:

- [Ordered dynamic iteration #26](https://github.com/dk-code-factory/agent-workflow/issues/26):
  reusable reviewer workflows, pinned ordered items, stable IDs, individual
  checkpoints, deterministic aggregation and classified failure handling.
- [Durable external-input waits #27](https://github.com/dk-code-factory/agent-workflow/issues/27):
  correlated once-only answers, stale-answer rejection, restart persistence and
  suspension without occupying a worker.

`prreview.EngineAdapter` isolates these contracts. `FixtureAdapter` is selected
explicitly by deterministic tests and is never enabled by an environment variable
or production configuration. The production execution bridge stays gated until
all required contracts can be integrated; a supported library capability alone
does not establish application action readiness. Missing-capability diagnostics
name only the absent contracts and their upstream issues. There is no fallback to
the old single model turn for a fresh review.

## Execution and retained state

The workflow resolves configuration, captures and validates remote evidence,
collects preparation inputs, asks for model analysis, runs ordered reviewers,
validates and aggregates results, evaluates posting policy, then publishes and
reconciles. The application owns parent completion. Child reviewer completion
cannot finish the parent run.

Evidence contains remote base/head, full inventory, normalized final merge-base
diff, isolated checkout/tree, linked scope, published Decisions, private rules and
owner constraints. Retained skill/command bindings freeze their package resources
and execution arguments. Commands receive the flat pinned `git-diff-v1` handoff,
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
Action implementation identity, exact head, engineering inputs and ordered bindings
must match before reuse. Only completed reviewer checkpoints can be reused.

Material questions appear through the existing attention controls. Waiting releases
execution; answers are correlated with the gate and its engineering identity and
claimed together with attention resolution. Interrupted reviews resume explicitly after the previous execution stops. A
completed review with unfinished publication resumes only its remaining policy
and publication stages. Execution identities fence stale saves and finalizers.
`send_message` accepts an operational `continue` or `resume`; changed engineering
instructions require a fresh prepared review. Restart preserves unanswered gates
and immutable artifacts without replaying answers or publication.

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

Fixture verification covers these contracts only. Production engine integration,
real providers, installed-host acceptance and real GitHub publication remain
unverified until the upstream dependencies and explicit opt-in acceptance exist.
