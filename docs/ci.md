# GitHub Actions checks and releases

[CI](https://github.com/dk-code-factory/code-factory-plugin/actions/workflows/ci.yml)
runs on pushes to main, pull requests to main or sync/local-development,
and manual dispatch on main. Hosted Ubuntu
installs Go from go.mod, Node24, Python3.12, locked npm/Python dependencies and the
matching Playwright Chromium. All actions are pinned to full commit SHAs.

`make verify` and the direct `scripts/verify.sh` entry point invoke one Make
worker dependency graph with `--no-keep-going`, so a failed phase stops further
verification even when `-k` or `--keep-going` was requested. The locked npm
installation, frontend compilation, plugin embedding and
shared browser fake-provider preparation targets each run once in that invocation.
Standalone `ui`, `build`, `test`, `test-performance`, `package` and `plugin`
commands prepare their own required inputs. `check` installs npm dependencies but
requires existing embedded frontend and plugin outputs for Go vet; run `make build`
first if those outputs were deleted. These prerequisites are phony: each independent
invocation prepares current inputs again, including changed manifests, lockfiles,
build tooling, sources and deleted outputs. There are no persistent freshness
stamps. Go compilation caches remain usable; race tests still use `-count=1`.

Hosted verification selects exact comparisons before entering `hosted-verify`,
which adds locked Python and matching browser installation to the same graph.
The restricted reviewed-root path uses `hosted-verify-bootstrap`, omitting only
the nonexistent comparison. Targeted ordering serializes verification phases
even with inherited parallel Make flags. Performance runs after heavy suites;
any failed phase stops downstream verification and package production.
Standalone goals retain the requested Make keep-going behavior.

The job validates specifications, checks Go/TypeScript, runs uncached Go race and
browser/accessibility fixtures, tooling, packaging, installer and compiled smoke
tests. After the heavy suites, the required isolated `make test-performance` gate
serves the production embedded UI through a fixture MCP host. One worker and zero
retries retain all 20 fresh and 10 warm launch samples, each limited to 100 ms.
Empty, cold-server and synthetic retained-state cases use provider operations
delayed by at least 100 ms, with measured delays, request counts, payload sizes,
render timings and long tasks stored in `web/performance-results/timing.json`.
CI uploads that JSON, environment details and failure traces on every run.
The fixture serves both the launch host and the exact embedded resource directly
over HTTP; timed requests do not pass through Playwright routing, which
[disables HTTP caching](https://playwright.dev/docs/api/class-page#page-route).
These measurements do not establish real ChatGPT sidebar latency. The launch endpoint uses [text ElementTiming](https://w3c.github.io/element-timing/#report-text-element-timing) for all six visible navigation labels, corroborated by [first contentful paint](https://www.w3.org/TR/paint-timing/#sec-PerformancePaintTiming). Action handlers must be usable before those paint entries. Browser [shared monotonic timestamps](https://www.w3.org/TR/hr-time-3/#examples) translate the entries to the parent click origin. FCP, every navigation paint/intersection, animation-frame callbacks, parent notification elapsed and raw message-delivery lag remain in the evidence; callbacks are diagnostics rather than presentation proof. It forces fixture mode and needs no model credentials or installed service.
Successful builds upload Linux x86-64 and portable plugin packages, SHA256SUMS and
a CI manifest bound to the exact source commit. Failed browser evidence is kept
separately. Verification has read-only repository permissions.

Main pushes compare their exact before/after commits; PRs compare their event
base/head SHAs. Manual checks on main use the current parent. If checkout lacks
an event-pinned base after a branch rewrite, CI fetches that exact SHA from
origin and validates it again. Invalid, unavailable or mismatched commits fail;
there is no guessed-ref fallback. Feature branches are verified through PRs to
main or sync/local-development. A branch without such a PR receives no automatic
checks or package artifacts. Manual feature-branch verification fails explicitly before running
checks or creating artifacts. A successful manual check cannot replace the
main-push verification required for release delivery.

## Public source import

Personal pilot history, private runtime state and live evidence are not published.
The public repository starts with a reviewed source snapshot, so its root has no
publishable predecessor. The administrator pins that one reviewed root SHA in the
nonsecret CI_BOOTSTRAP_SHA repository variable. Only that exact root on main may
run full structural/build/test/package verification without a nonexistent Git
comparison. It does not perform a fabricated HEAD-to-HEAD check or generate review
decisions. Every later revision uses the ordinary fail-closed specification gate.
The original fourteen-flow historical adoption pin remains unchanged and is not a
public CI bypass; its private historical commit is not published.

Concurrent local checkouts can set `CODE_FACTORY_TEST_BASE_PORT` for fixture
listeners, for example `CODE_FACTORY_TEST_BASE_PORT=43173 make ci FLOW_BASE=main`.
The daemon uses the following port, 43174 in this example.
Defaults remain 4173 and 4174. Both listeners stay on loopback; strict port
ownership, fresh servers, health checks and direct-connection assertions use the
selected ports. Installed service routes are unchanged.

## Browser correctness isolation and comparison

Browser correctness tests are partitioned by feature under `web/tests/`.
The mocked project creates a fresh browser context and host state for each test;
it can run tests concurrently. The assembled project keeps its three scenarios
in their original file order with one worker because they share a daemon,
database and repository fixture. Neither project retries failed assertions.
Accessibility evidence uses Playwright's per-test output paths. Both projects
share only the read-only Vite server; mocked tests never mutate the daemon.

Compare the same source with `npm --prefix web test -- --workers=1` and
`npm --prefix web test -- --workers=2`. Each invocation starts fresh fixture
servers. Run invocations sequentially in a checkout, with the documented port
override when other checkouts are active. Correctness uses a bounded default of
two workers, independent of CPU count; `--workers=1` selects serial execution.

The separate Browser correctness benchmark workflow compares both counts twice
on Ubuntu when the worker configuration or benchmark workflow changes, and can
also be dispatched explicitly. It retains complete JSON test results, elapsed
seconds and GNU time's maximum per-process RSS in KiB. That RSS is not aggregate
simultaneous memory across browser processes. Samples use prepared dependencies;
alternating worker counts makes warm-cache ordering visible. The benchmark runs
on its own runner and never replaces the complete CI verification job or the
isolated one-worker, zero-retry launch-performance gate.


To qualify a fixture or concurrency change, prepare current inputs, then run:

```sh
make ui plugin-embed fake-provider browser-deps
python3 scripts/benchmark_browser.py --output web/benchmark-results
```

The output directory must be new. The command runs the entire discovered
correctness inventory with worker counts 1, 2, 2, 1, with retries disabled and a
fresh testhost per invocation. It rejects missing, duplicated, skipped, failed or
retried scenario executions. For a file move, save the original discovery with
`cd web && npx playwright test --list --reporter=json`, then pass that JSON as
`--baseline-inventory FILE` to compare complete title multisets across the move.
Stable specification IDs remain in the registries; update their assertion paths
and regenerate references with `scripts/specs.py render`.

The versioned summary records source identity, tracked-change status, Node and
Playwright versions, elapsed seconds, GNU time's maximum individual-process RSS
in KiB, and peak sampled concurrent workload RSS in KiB. The individual-process
metric does not measure aggregate browser/worker memory. On Linux, the latter
sums `/proc` resident pages for the command's new session (GNU time, npm,
Playwright, browsers, workers and fixture servers) and descendants observed via
parent links, including children that start new sessions. An ancestry edge is accepted only while a pidfd-backed parent identity stays
live across a fresh child-parent read and parent start-time revalidation. A
vanished or replaced parent cannot supply new descendants. Observed descendants
remain tracked by PID and start time after reparenting. The original session ID
is trusted only while its direct leader remains unreaped; pidfds for other
processes do not reserve their numeric PIDs after exit. The sampler waits 50 ms
between scans; scan time and scheduling can lengthen intervals, and the summary
records the maximum observed gap and sample/process counts. This is sampled RSS,
not an exact peak or unique physical memory: shared pages count once per process,
and short-lived processes or children that escape the session and reparent before
observation can be missed. No unrelated host processes are intentionally included.
Collection requires readable Linux `/proc`, observed root and concurrent child
processes, and positive RSS; unreadable/malformed process data or missing coverage
fails qualification instead of accepting zero or partial results.

The benchmark also requires Linux pidfd support for safe workload cleanup.
SIGINT and SIGTERM are recorded while launching, measuring and cleaning up the
owned workload. Cleanup keeps the direct command unreaped until the final signal
to its original process group, preventing reuse of that group ID. Other observed
members, including descendants in new sessions, are targeted by pidfds after
checking their PID/start-time identities. Cleanup allows 200 ms after TERM, then
sends KILL and waits at most two more seconds for observed targets and the command.
A final scan includes newly visible owned descendants; the same unobservable
escape limitation stated above still applies. Missing safe targeting or incomplete
cleanup fails qualification and retains diagnostics. Cancellation restores prior
signal handlers and retains an interrupted, nonzero summary entry. This only
cleans up the benchmark-owned workload; it does not change daemon teardown.

Discovery stdout and stderr are retained even when discovery exits nonzero;
failure messages identify both artifact paths. Dependencies and generated
inputs are prepared; browser contexts and daemon state are fresh, while operating
system and toolchain caches remain available. Compare equivalent conditions,
retain raw reports and avoid extrapolating these measurements to real hosts.
This is an explicit correctness qualification, not part of ordinary local CI.


## Final verification and PR handoff

Use this sequence when a change is ready for final review. The
[specification guide](specs/README.md) owns reconciliation rules; the active
`dk-workflows:dk-pr-iter` skill owns PR preparation, review, publication and
completion gates. Read the installed skill and its stage contracts. This procedure
uses existing commands and does not replace either contract.

1. During editing, run focused checks for each affected area as soon as they can
   reveal a regression. Use the catalog's actual assertion anchors and suite
   commands to choose checks. Inspect impact early with
   `python3 scripts/specs.py report --base main --merge-base --json`.
   Fix failures promptly; batching final verification must not postpone serious
   regressions. Focused success is development evidence, not final acceptance.
2. Before final handoff, settle the intended scope and comparison. Fetch the
   target branch, ensure local `main` names the intended target tip, and record
   its full SHA and `git merge-base main HEAD`. Preserve unrelated work in an
   isolated worktree. For a different PR target, use that intended comparison
   consistently and record why; never narrow the comparison to pass a gate.
3. Run the complete reconciliation sequence below from the repository root.
   Review every affected subject from the report against both snapshots and
   actual implementation/assertions. Put substantive subject-specific
   `description`, `implementation` and `tests` decisions, with their review
   `kind`, in a private JSON file. Keep stable IDs and the pinned adoption policy.
   The review path shown here is an example; retain it outside Git.

   ```sh
   python3 scripts/specs.py render
   python3 scripts/specs.py validate
   python3 scripts/specs.py report --base main --merge-base --json
   # Complete the subject reviews before executing reconciliation.
   python3 scripts/specs.py reconcile --base main --merge-base --review /tmp/spec-review.json
   python3 scripts/specs.py check --base main --merge-base
   make ci FLOW_BASE=main
   ```

   Reconciliation executes required fixture suites and writes receipts only
   after success. Never edit receipts by hand. Retain command exit statuses,
   logs and receipt paths. `make ci` uses the merge base and runs full fixture
   verification, including the isolated performance gate; it does not replace
   the reviewed reconciliation. Real providers, public connections and installed
   hosts remain explicit opt-ins under the [browser runbook](agent-e2e.md).
4. Commit the relevant inputs, generated sections and CLI-written receipts
   together. Record the resulting source SHA and confirm the committed
   comparison with
   `python3 scripts/specs.py check --base main --merge-base --head HEAD`.
   Supply the handoff below to one final PR-iteration coordinator. That owner
   runs the requested DK lifecycle and keeps one current evidence index. Keep
   the workflow's configured independent reviewers, separate mandatory series
   cleanup worker and separate completion agent; one coordinator does not combine
   these roles or waive their gates.
5. The coordinator pins the remote PR head/base and gathers complete feedback,
   verified Decisions, instructions, rules, reviewer descriptors and verification
   inputs into the workflow's preparation. Require readiness before dependent
   work. Refresh after source, comparison, scope, governing decisions,
   reviewer configuration or verification inputs change, selecting the active
   artifact with `prepare --previous-preparation FILE`. Let the workflow decide
   whether bindings can be reused, incrementally reassessed or need full analysis.
   Retain both `preparation_id` and `engineering_id`, original evidence, review
   sessions and budgets. Reuse never grants new review coverage. Follow the
   required refresh and coverage checks after cleanup, including a no-op cleanup.
6. After subsequent edits or a changed comparison, recompute impact, reconcile
   affected subjects and rerun affected verification before claiming freshness.
   A previously successful full run is final evidence only while its inputs
   remain valid. Require successful hosted checks for the final published head,
   completed configured review coverage and both engineering/completion gates.
   Red checks accepted by a workflow exception are still red; a request for green
   CI requires success. Recheck the live head and target immediately before an
   authorized merge, guarding it with the verified head SHA. Merge only when the
   user requested it; PR iteration alone does not authorize merge. Release and
   deployment require their separate authorization and delivery evidence.

While children work, do independent work or wait for agent events. Use native
`wait_agent`, respecting host timeout limits and required user-update cadence.
A timeout supplies no new evidence: quietly wait again without agent-status
queries, file inspection or restart merely to obtain a status. Send completion,
blockers, failures, required decisions and material progress that changes the
next action. Consolidate child updates; do not relay unchanged worker status.
Keep required user communication using verified information already available.

For CI, use the active skill's single `ci_wait.py --pr PR_URL --head EXACT_SHA`
process for each published head; resume that process after tool yields. Do not
start parallel check polling or a second waiter while it runs. A terminal failure
permits diagnosis and the workflow's bounded repair/rerun path. Stop dependent
work on changed-head or incomplete evidence; never turn a timeout into success.

### Completion handoff template

Store full private evidence outside Git. Paths below are an index to retained
artifacts, not substitutes for fresh workflow gates. Mark unavailable evidence
and pending decisions explicitly instead of implying completion.

```text
Repository / worktree / PR URL / issue:
Head repository and branch / exact source SHA / source tree:
Target repository and branch / fetched target-tip SHA:
Effective comparison-base SHA / selection flags (worktree, index or committed):
Scope and governing instructions / active Decisions and exact user choices:
Affected subject IDs / review kinds and rationale / private review JSON:
Verification commands / input identity / exit results / private log paths:
CLI-written receipt paths / committed comparison check:
Outstanding gaps and gates / blockers / pending user decisions:
Coordinator / retained DK runtime / invocation and CLI-reference paths:
Preparation artifact / preparation_id / engineering_id / run record:
Configured reviewer sessions and results / remaining review and repair budgets:
CI waiter result path / exact checked head / hosted check links:
Series-cleanup assessment / engineering verification / completion evidence:
Push authorization (exact branch and source instruction):
Merge authorization (explicit source instruction, or absent):
Release/deployment authorization and outstanding acceptance:
Measurement boundary / agent, tool-call, wait and preparation-refresh counts:
```

For a representative trial, record the start/end events and counting rules with
private evidence: agents spawned (including nested roles), tool invocations
(distinguish coordinator and child calls), agent-event waits, CI-waiter processes
and preparation attempts/refresh outcomes. Count waits as overlapping coordination,
not recoverable elapsed time. Report actual counts and limitations; fewer messages
alone do not establish a speedup.

## Delivery

[Release](https://github.com/dk-code-factory/code-factory-plugin/actions/workflows/release.yml)
runs when an existing vMAJOR.MINOR.PATCH tag is pushed. Manual dispatch on main has
a dry-run option enabled by default; a real delivery requires an existing version
tag. Tags must resolve to main-branch history with a successful **push** run of
CI for that exact SHA and an unexpired package artifact. PR checks or a similarly
named workflow cannot authorize delivery.

The publisher downloads that CI artifact, verifies its commit, filenames, sizes,
checksums and complete asset set, then uploads those same bytes to a GitHub release.
It does not rebuild or overwrite existing releases. Version0 and suffixed tags
publish prereleases. Only the publisher job has contents-write access, using the
built-in short-lived GitHub token. No personal token, SSH key or cloud secret is
needed. CI artifacts expire after30days; rerun their original CI run if necessary.

Deployment to an operator's Linux machine remains manual. No workflow installs
services, changes Tailscale, signs into providers, starts agents or runs live-host
acceptance. A green build does not establish that an installation is ready.

The checked CI manifest also identifies daemon and plugin component versions
from the promoted package bytes. Explicit fixed issues in
`release/fixed-issues.json` must name full ancestor fix commits with matching
fixing/closing issue references. Report delivery tracking verifies the published
release, uploaded asset digests, tag commit, checksum list and component ancestry;
a tag, closed issue or merged PR alone does not establish release availability.
