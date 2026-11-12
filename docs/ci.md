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
tests. The Python CI and specification phases use
`python3 scripts/python_suites.py ci` and `python3 scripts/python_suites.py specs`.
Each command discovers the complete ordinary unittest inventory and partitions
unique test IDs across two separate processes. `--workers 1` selects serial
execution; the explicit upper bound is four. Global patches and environment
changes remain local to each worker. Missing or duplicate executions, a missing
worker report, or any failed worker makes the phase fail. Standard unittest skip
and expected-failure outcomes remain visible in the worker logs and summary.
Workers rediscover and select real case objects against the same full inventory.
Class/module fixture skips explicitly account for each affected case ID once,
including when setup and cleanup emit multiple legitimate skip events. Ordinary
skip-event counts remain visible without fabricating individual executions;
omitted non-skipped cases still fail.
The enclosing owned process group includes the coordinator, workers and ordinary
descendants, including when the coordinator exits abruptly. Already admitted
Make/reconciliation calls reuse that supervised group; direct CLI calls create
one through the existing verifier. Child bootstraps require that admission;
there are no internal coordinator/worker CLI modes. Per-test durations,
inventory identity, completion counts and outcomes are retained in ignored
`verification-results/python-ci.json` and `python-specs.json`; they never authorize
reuse. Phase order and the isolated performance gates remain unchanged.

After the heavy suites, `make test-api-performance` checks authenticated
MCP tools and protocol methods, local OAuth, health, signed webhook, artifact and
onboarding handlers through their response boundaries. The five Go packages run
sequentially. Every measured finite response and initial subscription-stream
acknowledgement is limited to 100 ms; an acknowledged stream can remain open until
cancellation. Successful responses, accepted pending work, callback rejection,
deadline rejection and terminal response-size rejection are separate outcomes.
A fast rejection is never counted as successful completion. Immediate and 125 ms
provider fixtures retain 1,000 saved sources. Additional controlled callback,
database, lock, filesystem, audit and service delays exercise the response boundary
and actual caller recovery without running real services.

Finite handlers have a 75 ms response allowance, 32 admitted workers and an 8 MiB
buffer limit. Capacity remains owned until a handler and its audit actually exit.
Completed business responses can finish before slow audit storage; records retain
the committed wire status when available; an uncommitted panic records an explicit
unknown status separately from its handler failure. Requests rejected before admission use a separate bounded audit lane: one sink call, 32 queued records and one overflow aggregate per observer. The aggregate retains exact rejection counts and their UTC interval, with individual request identities omitted. Diagnostic sinks receive the service lifetime; drain waits for their actual exit. A delayed or saturated handler reports a sanitized 503;
an admitted action can have an unknown outcome and must not be replayed blindly.
An oversized response reports a terminal error. Only a real flushed SSE data
acknowledgement opens a long-lived stream; headers or priming comments cannot
bypass the response allowance. Callback verification has a separate 50 ms bound
and never activates a canceled or late proof. Callback I/O holds no shared event
state lock, and obsolete delivery results cannot recreate or overwrite subscriptions.

Plugin API v2 handlers perform memory reads and bounded operation admission. They
use an 80 ms internal budget within the 100 ms per-sample gate, with the existing
75 ms finite-HTTP allowance as a defensive backstop. Openers return bootstrap and
the UI resource immediately. Saved reads return pending until immutable projections
match the domain and selected-item revisions observed at dispatch. Transcript
traffic does not invalidate unrelated inbox pages. Saved state is distinct from
upstream GitHub freshness, which remains governed by synchronization. Read selectors
for item detail, transcripts and conversation state are limited to1,024 UTF-8 bytes
and explicitly rejected before retaining projection entries. The isolated gate
measures near-limit rejected selectors as well as useful accepted reads.

Submitted operations require a client request ID and daemon instance ID. A repeated
identity returns its original work without executing again. Acceptance into memory
can lose queued work on process failure. The pending queue holds 256 operations
with a 64 MiB payload ceiling, eight ordinary workers and two reserved control
workers; full admission returns busy immediately. Workers persist safe intent
metadata before effects, omitting command payloads and answer bodies. Restart
never replays mutations: unfinished retained intent is uncertain and absent
memory-only work is lost. Completed result retention is bounded separately;
unavailable result bodies require saved-state inspection. Duplicate receipts use
the same current document-availability checks as status polling; completed queue
slots release their captured inputs rather than retaining them after accounting drains. Session locks, stale
question and proposal checks, safe apply boundaries and dirty-worktree checks
remain owned by the factory.

Tool responses are bounded to 64 KiB. Larger exact JSON documents use immutable
versioned chunks of at most 32 KiB of base64, reconstructed without truncation.
Projection preparation, result encoding, providers, subprocesses, redaction and
log writes run outside tool dispatch. API logging uses bounded background queues with a shared 8 MiB retained-buffer
ceiling across handlers; jobs retain consumed bytes and minimal URL metadata,
without retaining replay readers, headers or request contexts. It reports API version, outcome, response bytes, internal budget breaches and
dropped-log counts without tool arguments. The all-tool gate dynamically checks
the exposed catalog, retains every sample plus per-tool maxima and percentiles,
and requires useful current reads and healthy operation completion alongside
pending/busy responses. It exercises empty/cold/history, large documents,
concurrent clients, checked committed writes before and between client read batches,
near-limit RPC bodies with blocked logging, blocked database/logging, saturated queues
and providers/native subprocesses delayed for several seconds.

UI feedback precedes bridge calls. Pending work polls every 250 ms while visible;
idle reads use one second and hidden polling ten seconds. Drafts and owning-scope
action locks last through delivery, including temporary status-read outages.
Host round trips, operation completion and local click-to-feedback measurements
are distinct from the local handler budget. Arbitrary OS suspension and external
host/network delays do not have an absolute wall-clock guarantee.

Measurements are saved in `verification-results/` as `api-latency.json`,
`protocol-api-latency.json`, `gateway-latency.json`, `event-api-latency.json`, `onboarding-latency.json`,
`webhook-latency.json` and `http-boundary-latency.json`, uploaded as `api-latency`.
These controlled server-response measurements do not establish public-network,
installed-host or arbitrary scheduler/filesystem latency guarantees.
`make test-performance` also includes this API gate when invoked separately.

After the heavy suites, the required isolated `make test-performance` gate
serves the production embedded UI through a fixture MCP host. One worker and zero
retries retain all 20 fresh and 10 warm launch samples, each limited to 100 ms.
Empty, cold-server and synthetic retained-state cases use provider operations
delayed by at least 100 ms, with measured delays, request counts, payload sizes,
render timings and long tasks stored in `web/performance-results/timing.json`.
CI uploads that JSON, environment details and failure traces on every run.
The fixture serves both the launch host and the exact embedded resource directly
over HTTP; timed requests do not pass through Playwright routing, which
[disables HTTP caching](https://playwright.dev/docs/api/class-page#page-route).
These measurements do not establish real ChatGPT sidebar latency. The launch endpoint uses [text ElementTiming](https://w3c.github.io/element-timing/#report-text-element-timing) for all seven visible navigation labels, corroborated by [first contentful paint](https://www.w3.org/TR/paint-timing/#sec-PerformancePaintTiming). Action handlers must be usable before those paint entries. Browser [shared monotonic timestamps](https://www.w3.org/TR/hr-time-3/#examples) translate the entries to the parent click origin. FCP, every navigation paint/intersection, animation-frame callbacks, parent notification elapsed and raw message-delivery lag remain in the evidence; callbacks are diagnostics rather than presentation proof. It forces fixture mode and needs no model credentials or installed service.
Successful builds upload Linux x86-64 and portable plugin packages, SHA256SUMS and
a CI manifest bound to the exact source commit. Failed browser evidence is kept
separately. Verification has read-only repository permissions.

Main pushes compare their exact before/after commits; PRs compare their event
base/head SHAs. Manual checks on main use the current parent. If checkout lacks
an event-pinned base after a branch rewrite, CI fetches that exact SHA from
origin and validates it again. Invalid, unavailable or mismatched commits fail;
there is no guessed-ref fallback. Feature branches are verified through PRs to
main or sync/local-development. A branch without such a PR receives no automatic
checks or package artifacts. Manual feature-branch verification fails explicitly before
source checks or verified-build/package artifacts are produced. Partial timing summaries
may still be retained for rejected runs. A successful manual check cannot replace the
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

Verification and standalone Make build/test entrypoints own an advisory lock in
the checkout's Git directory before preparing mutable outputs. A competing invocation
fails with a separate-worktree instruction while the initiating process is live.
If native Make has exited while its guardian still drains managed helpers, a new
Make invocation can wait up to 35 seconds before attempting fresh ownership; it
may succeed after cleanup releases the lock. Kernel ownership automatically expires
if the lock owner dies. Linked worktrees have independent locks and build, browser
and package outputs. Keep arbitrary direct build commands out
of an active verification checkout. Reconciliation, browser npm scripts and the
correctness qualifier share this ownership, including authenticated nested calls.
The actual caller Make graph keeps its recipes, prerequisites, goal order and
file-defined default. A pidfd-bound guardian retains ownership for that Make
process, including native re-execution. Authenticated nested calls reuse it.
Repository-managed helpers register lifetime leases, including across exec;
the guardian drains their cleanup and timing before releasing checkout ownership.
The owner holds a non-inheritable directory descriptor through that drain.
Make reads framed private field files through its safe `/proc` alias, preserving
literal whitespace, newlines and Unicode without shell-output normalization.
Managed phases reuse an enclosing worker session only when the live runner's
identity and its pinned direct-child group/session belong to the current
ownership lifetime. Private owner-bound grants stay active through group cleanup,
so admitted cleanup can retain sharing after Make exits. An unproved group marker
selects a separate owned session. Admission commits at a final live-Make pidfd
observation after the identity handshake, before acknowledgement. An exit observed
there rejects pending admission; committed leases still drain when Make exits
after that observation, including before acknowledgement arrives.
The phase lease lasts through command and timing finalization; normal shared
phases incur no additional session-cleanup wait. Direct hosted verification retains
ownership through cancellation and manifest timing. Caller-authored supplemental
recipes retain native Make shell and cancellation semantics. This does not claim
control of arbitrary background descendants launched by those recipes.
Cleanup uses a 10-second timing-worker budget inside a 15-second enclosing
command budget, beneath the guardian's 30-second escalation deadline. Normal
completion, including an ordinary nonzero Make exit, retains the short group
cleanup: native Make waits for foreground managed helpers to finish. Signal death,
timeout, wait errors and cancellation retain the full enclosing cleanup budget
even if the direct leader has already exited.

Local-verifier regression probes use an independent subreaper supervisor. Its
signal handler records cancellation, allowing child assignment and cleanup entry
to complete. A deterministic SIGINT at the first cleanup instruction asserts
exit130 and no surviving detached descendants before fixture disposal. The child
acknowledges creation of both descendant PIDs through a pipe before the inner
cleanup deadline starts; startup has its own bounded readiness wait.

Each invocation creates a private temporary root, propagates it through `TMPDIR`
and bounds fixture Git discovery there, so an unrelated ancestor `.git` cannot
become a fixture repository. A caller's `TMPDIR` selects the parent of this root.
For a colon-containing path, Git resolves the live owner's directory-descriptor
alias to the exact private boundary before discovery; the colon is never inserted
as a list separator. Ordinary source and nested fixture repositories still resolve
normally. GNU Make 4.3 and 4.4 retain the parsed temporary-parent value through
escaped input and framed output, including literal dollars and CR/LF data. Timed
phase commands keep their recipe working directory when owning a new session.
Managed verification entrypoints compose the private Git ceiling with the
environment actually exported by their caller. Authenticated borrowed entries
also compose it, retain an already-first private boundary, and restore the
previous missing, empty or literal value when they leave. Native Make keeps
caller Git values, expansion flavors and export decisions. Raw supplemental
recipes use `python3 scripts/verification_run.py -- COMMAND` or the managed Python
context when they need automatic fixture Git bounds; raw recipes otherwise keep
their native Git environment, as they keep native shell and cancellation rules.
The explicit real-provider target enters the same helper and sets its opt-in
only for its child command. The connection probes retain native execution:
their installed-service requests create no temporary Git fixture work.
The hidden `.code-factory-verify-*` directory names are reserved for private
verifier state and ignored at every temporary parent, including inside the
checkout. Other untracked source remains visible to specification checks.
Explicitly selected Makefiles retain their order, includes and ordinary
command-line overrides in the native caller graph. Supplemental recipes and
shared prerequisites execute once there, including with parallel/keep-going flags.
Standalone stdin uses GNU Make's native source. The existing timed verification
worker and `specs-ci` still recurse. Stdin is retained before native removal so
caller-defined aliases can reach those boundaries; only those managed boundaries
replay selected files under the same owner. Capture prefers the caller parent outside the
checkout, using a private external fallback otherwise, and a live Make
process identity; a pidfd-bound helper removes it when Make exits. No source bytes
enter command output or environment variables. The returned pathname is encoded
across Make evaluation so caller dollar/hash characters remain literal.
Ambiguous source identity fails
before verification. Copy/startup cancellation removes owned capture state before
guardian handoff; allocation and cleanup defer signal propagation until their
owned directory is bound or removed. Native dry runs retain no timing journal.
Normal completion, failure and cooperative cancellation remove only owned state;
an uncatchable kill can leave a private directory, which future runs never reuse.
Do not delete another run's directories or persistent lock inode.

Browser npm scripts and correctness qualification start fresh loopback listeners
on kernel-assigned ports, retain them and communicate their actual origins before
Playwright starts. No free-port probe or unknown existing server is used. For a
fixed adjacent pair, set `CODE_FACTORY_TEST_BASE_PORT`, for example
`CODE_FACTORY_TEST_BASE_PORT=43173 make ci FLOW_BASE=main`; the daemon then uses
43174. An occupied requested listener fails before assertions, without retries.
Direct low-level Playwright commands retain 4173/4174 defaults; use the npm
entrypoints for automatic isolation. Installed service routes are unchanged.
Fixture-mode selection uses the installed Playwright test command's option
parser, including consumed option values and the `--` positional boundary.
Browser fixtures preserve caller signal listeners. Cancellation keeps a nonzero
status while owned children, HTTP listeners and private state are cleaned up;
an unresponsive owned child is killed after a bounded cooperative shutdown.
Go compilation and daemon readiness share the former Playwright startup budget:
60 seconds for correctness and 120 seconds for performance. Correctness then
starts the owned UI child under its separate 60-second readiness guard; performance
has no UI child. UI startup errors and unexpected exit fail verification. Expiry
reports failure and drains owned children before removing private fixture state.

Executing local `make ci`, `make verify` and direct `scripts/verify.sh` runs retain a
version-1 JSON phase summary in ignored `verification-results/<run-id>.json`.
Hosted verification starts the same journal after checkout, measures toolchain
and prerequisite setup, and always attempts to finalize and upload that one file
as `verification-phase-timings` with seven-day retention. Checkout and artifact
transfer time are outside the measured verification interval. A runner killed
before its final steps can prevent upload; the last atomic journal still records
started work as running rather than successful. The journal holds at most 256
phase records and fixed, allowlisted metadata; it includes no raw command arguments,
environment dumps, command output or local paths. A timing-write failure emits a
fixed warning and preserves the actual command or cancellation status.

Hosted startup exports its reserved journal path and recovery state before creating
the file. Initial publication replaces the selected path only after a complete
snapshot has been written; interrupted staging is cleaned up. Setup timing starts
before metadata collection and preserves the reservation's monotonic origin. If
startup stops before publication, a surviving setup or overall finalizer can
materialize the matching hosted reservation with unknown metadata and unstarted
phases. Reconstruction requires a valid matching reservation and does not replace
an existing path, including a broken symlink. Setup finalization finds running setup
records without a separate exported phase ID. If setup never started, its duration
remains unavailable. Ordinary timing-write errors remain optional warnings.

Each phase has a stable command identity, outcome, optional parent ID, monotonic
start/end offsets and `seconds`, using the same duration unit as reconciliation's
existing per-command receipt evidence. Missing durations and exit codes are JSON
null; planned phases that did not execute remain `unstarted`. Newly automatic
local startup reserves one journal identity, path and monotonic origin in the
parent. An executing native worker materializes its partial before resolving
metadata. Successful native dry runs execute no verification tools and create no
timing file. If the child cannot start, fails or is interrupted before native
context is observed, the parent retains and finalizes an unknown partial at that
reserved identity and origin, even when the attempt was dry. Direct timing-helper
calls and supplied or hosted journals retain their immediate startup behavior.
Interruption retains unknown fields as null or unavailable. Source identity is
collected early in the executing worker. Local comparison remains unavailable until
the specification check supplies its actual argument vector, including recursive
and target-specific variables, automatic variables, shell quoting and the `ci`
override. An early prerequisite failure or a bootstrap without a comparison keeps
that field unavailable. Hosted event-pinned comparison identity is preserved.
Native Make determines whether ordinary worker initialization executes; timing
adds no Make argument detector and preserves caller variables. Overriding both `MAKEFLAGS` and `MFLAGS` retains Make's existing recursive
forwarding limitations. Failed commands stop
the pipeline, and SIGINT/SIGTERM preserve partial results and clean up the owned
process group. Intervals may overlap or nest, especially under caller-supplied
Make parallelism: use `wall_seconds` for elapsed time, never a sum of phase
durations. The timed browser samples and their authoritative performance evidence
are unchanged; instrumentation runs around their command, outside those samples.
Reconciliation receipts retain their existing timings and freshness rules;
these summaries describe each separate verification invocation.

For a repeatable baseline, retain the summary with its source commit, working-input
SHA-256, exact comparison, toolchain versions and declared cache condition. Set
`CODE_FACTORY_TIMING_CACHE=cold` only after removing generated frontend, dependency
and helper outputs in an isolated workspace; keep the package-manager and compiler
cache policy explicit in the measurement notes. Immediately repeat with
`CODE_FACTORY_TIMING_CACHE=prepared` against identical source and toolchains.
These labels describe the operator's declared conditions, not measured cache hits.
Use multiple samples on the same environment before comparing optimization results;
this instrumentation itself establishes no speed improvement. Hosted setup failures
retain event-pinned comparison identity even when verification cannot begin.

## Browser correctness isolation and comparison

Browser correctness tests are partitioned by feature under `web/tests/`.
The mocked project creates a fresh browser context and host state for each test;
it can run tests concurrently. The assembled project keeps its native session scenario
in their original file order with one worker because they share a daemon,
database and repository fixture. Neither project retries failed assertions.
Accessibility evidence uses Playwright's per-test output paths. Both projects
share only the read-only Vite server; mocked tests never mutate the daemon.

Compare the same source with `npm --prefix web test -- --workers=1` and
`npm --prefix web test -- --workers=2`. Each invocation starts fresh fixture
servers. Use separate worktrees for concurrent invocations; one checkout accepts
one invocation at a time. Correctness uses a bounded default of
two workers, independent of CPU count; `--workers=1` selects serial execution.

The separate Browser correctness benchmark workflow runs the same qualifier on
Ubuntu when browser, fixture, build or qualification inputs change, and can also
be dispatched explicitly. Checkout fetches full history before qualification so
Playwright can resolve the PR base locally when collecting Git metadata.
Its existing artifact retains summaries, inventories,
reports and failure diagnostics for seven days. It runs on its own runner and
never replaces complete CI verification or the isolated one-worker, zero-retry
launch-performance gate.

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


## Combined local reconciliation and verification

For the standard repository Make graph, one explicit local invocation accepts complete
review decisions and performs reconciliation plus complete branch verification without
rerunning an identical check:

```sh
python3 scripts/verify_local.py --base main --merge-base --review /private/spec-review.json
```

Review all affected subjects before running it. The command validates those decisions
first, inspects the native Make dependency plan, prepares current frontend/plugin/helper
and release artifacts once, then executes the union of local checks and required catalog
suites. It records ordinary CLI reconciliation receipts and runs the final comparison
check. The default ignored `verification-results/` summary retains command counts,
individual elapsed seconds and owned-call wall time (preflight through required cleanup,
excluding interpreter startup and optional summary preparation/writing); `--summary FILE` can select a
destination outside source inputs.
Launched checks remain counted when timeout or cancellation interrupts the runner;
their elapsed duration is retained and an unavailable exit code is JSON null.

Sharing is in memory for this invocation only. Exact command arguments and working
directories must match; different npm invocation forms are intentionally separate.
Preparation schedules retain first occurrences inside one environment- and
checkout-owner-bound session. Pure duplicate preparation entries do not repeat
full input scans; actual commands, reconciliation reuse and final boundaries
retain their input guards.
Required `make build` and `make test-performance` commands expand through the same
inspected native graph. Source, ignored generated inputs and helpers, resolved
executable bytes/versions, effective environment, comparison and review changes fail
verification. Source monitoring begins before the initial snapshot and native Make
planning, so a changed-and-restored Makefile cannot supply an unbound plan.
The checkout's pathname ancestry is bound before the initial watched tree traversal.
Temporarily substituting an ancestor directory invalidates verification even when
the original checkout and Makefile inodes are restored before the next checkpoint.
The selected Make executable and every PATH candidate are monitored before any
native plan is captured; a temporary planner cannot leave an accepted plan after
removing itself or restoring the selected executable.
Linux mutation monitoring also rejects writes restored before digest
comparison, including writes through external hard links. Inode watches complement
directory watches. Read leases reject existing writable file descriptors/shared
mappings and invalidate the invocation before permitting a later writer; restoring
mapped bytes does not restore eligibility. Close editors that retain writable
descriptors before starting. The command requires the main Python thread, unused
and unblocked SIGIO, available file/watch descriptors and Linux read-lease support
for user-controlled inputs. Overlapping watches share leases until their last
owner releases them; teardown restores the caller's signal state. Unsupported
admission or lost lease coverage fails closed.
The host administrator and kernel remain trusted. An unprivileged invocation
cannot lease root-owned tools: only root-owned files without group/other write
permission on ext2/3/4, XFS, Btrfs, overlayfs or tmpfs may use the administrator
trust boundary instead. They retain inode watches and persistent metadata checks;
root changes to the installed toolchain must not overlap verification. Other
unleaseable inputs reject, including any conflicting writer. Monitoring also rejects
directory replacement, lost event coverage and a nonquiescent event stream. Report directories, unused default Python
bytecode caches, transient Vite config bundles and reconciliation receipts are excluded from
input identity; the prepared invocation cache is explicitly included, and release archives remain inputs. The combined command prepares packages
before running checks so every check sees the same packaged binary. The explicit `local-verification-prepare` Make target also prepares Vite's development dependency bundles
before their contents become verification inputs; browser-created cache changes
therefore cannot be silently accepted after checks. Ordinary Make verification retains
its existing late packaging order. A failed or cancelled required run invalidates selected
success records, even when failure follows receipt creation. No previous summary or
receipt supplies a new execution. Independent Go race runs retain `-count=1`.
Artifact symlink lookups and targets must remain inside admitted checkout inputs;
chains crossing Git metadata, report/cache/owner outputs or external paths reject.
Worktree source snapshots, regular artifacts, resolved executables, ELF discovery
prefixes and review decision files use descriptor opens without last-component symlink following or blocking
on FIFOs, then checked by descriptor type, metadata and pathname binding before
and after consumption. Failure and cancellation close the opened descriptor while
retaining the original error.
Review lookup watches begin before the first read. Checkpoints repeat typed reads,
while reconciliation decodes the already-admitted immutable bytes instead of
reopening the external review pathname. Replacement still rejects after required
work or receipt creation and retains the original cancellation outcome.
Worktree snapshots reject present nonregular inputs; committed comparisons still
read exact Git blobs. Initial analysis, source identity and reconciliation's
post-check snapshot use this admission. Makefile, browser configuration, npm
manifest and loader/Git discovery payloads use the same typed read primitive.
The selected npm installation, Go installation and tool directory, GCC compiler
helpers/resources and Go environment configuration are also fingerprinted and
mutation-monitored. Git participates in executable identity as an indirect helper.
The maintained npm launcher also binds its `/usr/bin/env` interpreter.
Go fixture runs also bind the selected `setpriv` executable and every earlier
PATH candidate, so a transient wrapper cannot supply reusable success.
The selected Playwright Chromium, headless-shell and FFmpeg installation roots,
actual npm user/global configuration in both repository and web contexts, and
default npm shell also participate. Admitted helper lookup edges are monitored,
including intermediate symlinks and their selected targets. A nonempty
`ESBUILD_BINARY_PATH` is unsupported and rejects before ownership or discovery;
the standard graph uses the locked package's bundled esbuild executable. Go/GCC discovery
retains admitted root, compiler and reported-resource spellings until monitoring; unrelated ancestor
entries remain outside that scope.
After installing the complete selector and lookup watches, the command repeats
helper discovery and requires identical selected paths and alias closure before
recording its identity baseline. Changed selected paths/closure or selector writes
observed by installed watches fail before required work. A prewatch change that
retains the same fully covered selected set may be adopted by the current baseline.
Each helper-identity traversal reopens selected regular files and retains every
alias, lookup and mode row. Stable opened device/inode identities can share one
content digest within that traversal only. Metadata and lookup bindings are
checked before and after fresh reads and memo hits; access-time-only effects are
excluded. Changed or replaced targets reject, and the next checkpoint reads
content afresh. File descriptors close on success, error and cancellation;
secondary close failures retain an existing error or cancellation.
Selected Go module build-list source trees and module metadata, and the selected
Python interpreter's admitted import search directories and package data, are
also inputs. A selected Python search path that intersects omitted runtime or
`__pycache__` trees is unsupported, even when it contains used package source.
Nonempty `PYTHONPATH`, `PYTHONHOME` and `PYTHONUSERBASE` selectors must contain
only absolute paths, with no empty entries. Relative selectors can mean different
imports in root and browser-helper working directories and reject before ownership
or planning. The public CLI checks these selectors using its bootstrap `os`/`sys`
modules before importing application code or parsing arguments. Interpreter
startup and its hooks precede this application preflight; admitted absolute roots
retain normal Python behavior. Empty selectors retain ordinary interpreter behavior.
Admission checks original paths, resolved paths and intermediate alias lookups;
unused tagged automatic cache files beneath ordinary search roots retain their exclusion.
Nested directories named `__pycache__` remain traversed and watched: they can
contain importable namespace source and legacy sourceless modules.
External directory aliases retain both lookup and content watches.
Verification prepares fresh invocation-local checked-hash Python bytecode from
inventoried sources without importing those packages. The cache is then an
explicit generated input: all entries, metadata and lookup bindings are hashed
and watched, including when the owner namespace is otherwise excluded. Automatic
bytecode writes are disabled in subprocesses and the current interpreter. Missing
entries compile from inventoried source without writing; unused default
CPython-tagged automatic cache files are excluded. A cache write, replacement or write followed
by restoration rejects evidence before work or reuse. No cache survives the invocation.
Cache entry reads use nonblocking, no-follow regular-file descriptor admission
with metadata and pathname binding checks before and after hashing. A replacement
FIFO rejects without waiting for a writer. Opened files close on success, error
and cancellation; an earlier failure takes precedence over a secondary close error.
The cache directory descriptor and independent watch remain live through summary
publication and outer owner teardown, with binding checks before final success.
The temporary parent must be outside the checkout so the cache can outlive the
owner's private namespace; caller environment and interpreter settings are restored.
This controls normal source-backed automatic bytecode caches. Frozen runtime code
and inventoried sourceless modules retain their ordinary behavior; non-cache
module sources and data remain inputs. Custom effective Go flags (including
overlays), external workspaces and nonempty effective GOCACHEPROG selections are
rejected before module discovery. The cache-program check reads Go's effective
environment, including GOENV configuration.
The combined invocation replaces environment- and GOENV-selected GOCACHE with a
fresh cache inside its authenticated owner's temporary namespace. Each required
command starts with that directory empty; completed cache outputs are discarded
before the next command. Retained external archives and an earlier command's
compilation artifacts cannot supply a later check. Within one command, cache
files remain that command's temporary outputs, including Go's normal mutable
index records. Exact completed command results can still share under the source,
toolchain, environment and comparison guards. Compilation across different
required commands starts fresh, adding overhead compared with ordinary Make.
The cache directory's selection, lookup and inode binding remain guarded through
summary publication. The guard closes before owner teardown. Failed disposal
invalidates successful publications while preserving an existing verification
error or cancellation. The cache is removed and caller selection is restored.
Go retains ordinary VCS stamping. Its Git revision, timestamp and status queries
use watched repository/worktree selection files, HEAD, refs, packed refs, object
stores, index, shallow state, configuration and ignore/attribute inputs, including
linked-worktree private `refs/worktree`, `refs/bisect` and `refs/rewritten`. Discovery
is repeated under those watches. Git metadata traversal has no Python-cache
exclusion, including valid refs whose names contain `__pycache__`. Optional index refresh writes are disabled with
`GIT_OPTIONAL_LOCKS=0` inside this invocation; status semantics and stamping remain
unchanged, and the caller's setting is restored. Custom Git selection variables,
configuration includes, fsmonitor, configured external ignore/attribute paths,
alternate object stores, grafts and nonstandard ref storage are unsupported.
Default system/global configuration candidates and ignore/attribute paths are
included even when absent, including Git's reported system attributes file.
Tracked-file attribute selection is inspected before required work; selected
clean/process filters reject because status may execute external helpers. Unused
filter definitions remain supported except clean/process drivers named `set`,
`unset` or `unspecified`: Git's attribute query renders those literal names
identically to its special states, so their definitions are unsupported even
when currently unused. Ref/object writes restored before a checkpoint still
reject evidence. Reflogs and lock files do not supply reused build evidence.
Nonempty `LD_PRELOAD`, `LD_AUDIT` and `LD_LIBRARY_PATH` are unsupported because
they select external ELF loader code or library search inputs. The local entrypoint
rejects them before ownership, planning or helper discovery; empty values are admitted.
Nonempty `NODE_OPTIONS` is unsupported, including external startup modules.
Effective npm `node-options` in both checkout and web contexts is also checked;
nonempty selections through npm environment configuration or npmrc files reject
before required work can launch lifecycle children.
Effective cgo preprocessor/compiler/linker flags are read through `go env`, so
both environment and GOENV selections are checked. Only Go's standard empty
CPPFLAGS and `-O2 -g` CFLAGS, CXXFLAGS, FFLAGS and LDFLAGS are admitted; custom
external includes, libraries or compiler arguments fail before required checks.
GCC's reported default C/C++ header search roots, including currently absent
and duplicate lookup candidates, are hashed and watched. Directory aliases retain lookup and
resolved-content coverage. Nonempty CPATH, C_INCLUDE_PATH, CPLUS_INCLUDE_PATH,
OBJC_INCLUDE_PATH, LIBRARY_PATH, COMPILER_PATH and GCC_EXEC_PREFIX are unsupported;
custom search contexts fail before compiler discovery.

The combined command supports the maintained x86-64 glibc ABI. It inventories the
canonical system native-library tree, direct files in built-in and cached lookup
directories, hardware-capability candidates, selected cache entries, loader/cache
configuration and absent preload/search paths. This includes native modules loaded
by Python, Node and Chromium as well as startup dependencies. The native ABI tree
also covers ordinary system plugin and dlopen modules; unrelated SDK trees are not
recursively selected. The full helper/package inventories resolve nested directory
aliases before scanning bundled native modules, so extensions beneath aliased
packages receive the same loader admission and monitored dependency coverage.
The maintained executable inventory includes the pinned Playwright registry's
`chrome-headless-shell`, alongside the older `headless_shell` spelling.
ELF search admission checks original and resolved ORIGIN parent
spellings. Interpreter and RPATH/RUNPATH admission rejects alternate
loaders, relative searches, unsupported tokens and existing search directories
outside these inventories. Path-qualified dependencies and ELF audit/filter
indirection are unsupported. Absent search directories are watched; creation fails
rather than broadening an active invocation. Nonempty LD_* or GLIBC_TUNABLES
contexts and system preloads are unsupported. ldconfig and readelf, their aliases
and selected library inputs participate in repeated discovery and identity checks.
PATH entries must be nonempty absolute directories. Nonempty case-insensitive
PATH carriers with a spelling other than `PATH` reject
before ownership or discovery, since npm merges those variables into its search.
Empty alternate carriers are admitted. The maintained graph's selected
launchers, fixed compiler/shell lookup names and discovered helper filenames have
lookup watches, including earlier unselected candidates. Browser npm scripts enter
Python ownership, start the Node fixture runner and compile the Go fixture host.
Their helper lookups include npm's ancestor node_modules/.bin prefixes, node-gyp
prefix and inherited PATH, with candidates watched even when absent. Aliases to
the inventoried tools require matching Python import roots; adjacent virtual
environment configuration remains watched. A different selected launcher rejects
before work so resource discovery cannot describe another executable.
Discovery and watches derive each working directory's npm prefixes from the same
inventory, preserving selection order without maintaining a second safety rule.
This inventory supports the maintained scripts and admitted tool contexts; it does
not infer the dependencies of arbitrary subprocess scripts. Recursive watches
are installed before enumerating each directory, so a newly admitted child cannot
escape the observation interval. Nonempty BASH_ENV is rejected before ownership,
planning or helper discovery; ordinary Bash and Make behavior is unchanged.

Source-only attempts of the complete `python3 scripts/test_ci.py` command reached
600- and 900-second deadlines. Those observations do not isolate interpreter
startup cost. This combined command prepares its guarded bytecode cache
and retains bounded 900-second deadlines for the exact repository-root
`python3 scripts/test_ci.py` and
`python3 -m unittest discover -s scripts -p 'test_specs*.py'` commands, plus the
maintained `python3 scripts/python_suites.py ci` and
`python3 scripts/python_suites.py specs` commands. Cache
preparation has a 120-second deadline and records elapsed time, entry count and
byte volume as an additional command phase. Other commands retain 600 seconds; a
shorter explicitly requested session deadline remains shorter. Lookalike
arguments and other working directories receive no extension. Independent
reconciliation uses the same 900-second bounds for these exact catalog commands
from the repository root; supplied executors retain ownership of their deadlines.
Ordinary Make retains its existing deadlines. Timeouts still drain
required owned work and invalidate evidence.
Dependencies must already be available; Go module discovery disables proxy fetching.
This includes every module in the selected build list, even modules a previous
`go test ./...` did not import. Cold or partially populated module caches fail
before verification. This restriction applies to the combined command; ordinary
Make and focused tests retain their existing dependency requirements. Run setup
before preparing review decisions: `go mod download all` may update `go.sum`.
Review and preserve those changes; the combined command never rewrites sums to
admit missing dependencies.
Unavailable helper inventories fail before required work. The combined coordinator
records outer SIGINT/SIGTERM from entry, rejects pending cancellation at command
and reuse checkpoints, and closes all watches and invalidates failed receipts
before propagating cancellation and restoring its caller's handlers. Subprocess
signals retain the existing owned process-group cleanup. Watch-close
failures also invalidate receipts, including after otherwise successful checks.
Digest checkpoints drain all source/helper/artifact watches before and after
traversal. Cleanup attempts a final drain and close for every such watch, preserving
an existing error or cancellation while still invalidating selected successes.
Each monitor's final empty read ends its input-observation interval before that
descriptor is released; later changes are outside that monitor's completed evidence.
The independent Python-cache watch also drains after digest traversal and before
closing. Its drain or close failure propagates to exact-publication rollback on
an otherwise successful invocation. Cleanup-time cache validation and disposal
retain an already-raised verification failure or runner cancellation; a new cache
failure is authoritative only when no earlier failure or cancellation is active.
Optional summary-write failures retain the verification or cancellation status.
Rollback remains active through optional summary publication and enclosing checkout
owner teardown. Checked signal protection covers the owner's handler restoration,
final cache validation and cache disposal. Its final pending-signal check runs
while publication descriptors remain open. Signals observed through that check
trigger rollback. An outer record-only scope protects exception handoff and the
complete rollback body. The outcome then commits; caller-handler restoration and
publication-descriptor release are later bookkeeping, with no further exit-time
verification check. After caller-handler restoration, signals use that disposition;
post-outcome signals do not invalidate completed verification evidence.
Receipts are captured by descriptor while ownership is held;
summaries publish atomically with their descriptor retained before replacement.
Late rollback invalidates those exact objects, leaving empty rejected JSON when
their paths still name them, and preserves a later owner's atomic replacements.
Descriptor release is diagnostic bookkeeping after the outcome or rollback.
The recorded duration ends after check/watch cleanup, before optional summary
work, enclosing owner teardown and final cache disposal; final success also
requires the checked teardown and disposal scope to complete.

Install the locked npm dependencies, matching Chromium browsers and Go module
inputs (`go mod download all`) before using
this entrypoint. Browser discovery uses the installed pinned Playwright registry;
missing or unknown layouts fail before required work. Only the two reviewed repository Chromium configuration file identities are
supported. Configuration edits require reviewing this explicit inventory binding;
custom configuration, browser/channel/executable launch overrides,
in-package browser installations (`PLAYWRIGHT_BROWSERS_PATH=0`), and custom npm
`script-shell` settings are rejected. External browser locations selected through
`PLAYWRIGHT_BROWSERS_PATH` are inventoried from the registry.

The combined invocation retains the existing managed checkout owner across preparation,
reconciliation and every verification command. Nested managed commands share its authenticated
temporary environment. Planning binds the reviewed native ownership prologue and entry
recipes, omitting only that ownership plumbing from its private dry-run copy while
the enclosing owner remains held. Changed bindings fail before Make execution. Only
the authenticated owner namespace is omitted from generated-input monitoring.

This entrypoint deliberately supports the standard graph, without supplemental
Makefiles, inherited Make flags/overrides, custom exported variables/shell modes,
includes, executable Make expansions, selected recursive/forced recipes or unfamiliar
shell recipes. Unsupported graph changes fail closed rather than omit a check. Use
ordinary Make entrypoints for custom native Make behavior. This local command refuses
hosted CI and real-provider opt-ins; hosted CI executes its own fresh graph. Existing
focused checks and this PR's required `make ci FLOW_BASE=main` validation remain
applicable.

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

Use the installed workflow's actual CLI surface. Some workflow versions retain
run/cycle IDs, task fingerprints and claims instead of preparation/engineering
IDs or a `prepare` command. In that case retain those native identities and
explicit review/cleanup/completion evidence, mark unavailable older fields as
unavailable, and refresh the current task bindings after input changes. Do not
invent a compatibility command or omit an independent role because terminology
has changed.

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
