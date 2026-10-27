# GitHub Actions checks and releases

[CI](https://github.com/dk-code-factory/code-factory-plugin/actions/workflows/ci.yml)
runs on pushes to main, pull requests to main or sync/local-development,
and manual dispatch on main. Hosted Ubuntu
installs Go from go.mod, Node24, Python3.12, locked npm/Python dependencies and the
matching Playwright Chromium. All actions are pinned to full commit SHAs.

The job validates specifications, checks Go/TypeScript, runs uncached Go race and
browser/accessibility fixtures, tooling, packaging, installer and compiled smoke
tests. After the heavy suites, the required isolated `make test-performance` gate
serves the production embedded UI through a fixture MCP host. One worker and zero
retries retain all 20 fresh and 10 warm launch samples, each limited to 50 ms.
Empty, cold-server and synthetic retained-state cases use provider operations
delayed by at least 100 ms, with measured delays, request counts, payload sizes,
render timings and long tasks stored in `web/performance-results/timing.json`.
CI uploads that JSON, environment details and failure traces on every run.
The fixture serves both the launch host and the exact embedded resource directly
over HTTP; timed requests do not pass through Playwright routing, which
[disables HTTP caching](https://playwright.dev/docs/api/class-page#page-route).
These measurements do not establish real ChatGPT sidebar latency. The launch endpoint is timestamped in the child at the same second animation-frame callback, using browser [shared monotonic timestamps](https://www.w3.org/TR/hr-time-3/#examples). Parent notification elapsed time and message-delivery lag are retained separately, so host queueing after paint is auditable without entering the click-to-paint bound. It forces fixture mode and needs no model credentials or installed service.
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
