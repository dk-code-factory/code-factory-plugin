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

Local `make ci`, `make verify` and direct `scripts/verify.sh` runs retain a
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

Each phase has a stable command identity, outcome, optional parent ID, monotonic
start/end offsets and `seconds`, using the same duration unit as reconciliation's
existing per-command receipt evidence. Missing durations and exit codes are JSON
null; planned phases that did not execute remain `unstarted`. Failed commands stop
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
