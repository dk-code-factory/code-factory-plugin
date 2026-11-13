# Code Factory contributor instructions

Develop on dedicated branches and submit changes through pull requests. Do not
commit or push development changes directly to main unless explicitly requested.
Run required checks and review before merging; merge only when requested.

Always serve user-facing API reads from cached, background-prepared projections.
Keep database reads, provider calls, native agent work and projection construction
off the request path. On a cache miss, invalidation or eviction, enqueue background
preparation and return the existing pending/busy state promptly. Never serve a stale
revision as current. Mutations and explicit refreshes must return operation receipts
and execute in background workers; subsequent reads use the updated cache. Preserve
authorization, revision checks and the API latency/response-size budgets. Verify
cold-cache and blocked-dependency behavior when changing API handlers.

Use the [layered specification guide](docs/specs/README.md) and
[reconciliation skill](.agents/skills/reconcile-specs/SKILL.md) for source, tests
and behavior changes. Preserve glossary → meaning, UI → glossary, and flows →
UI/glossary dependency ownership. Review real assertions before claiming coverage.

Run focused local checks appropriate to the change. Do not run the complete CI/CD
pipeline locally; use hosted CI for full verification before merging. For source,
test and behavior changes, complete the required specification reconciliation.
Use one final PR-iteration coordinator for review and handoff. Hosted checks and
release delivery are explained in [docs/ci.md](docs/ci.md). When that guide describes
full local CI runs, this policy takes precedence. Ordinary verification forces
fixture mode; real provider, public connection and installed-host checks are
explicit opt-ins.
Never run agents or use model credentials in CI.

Keep credentials, owner tokens, runtime configuration, browser profiles and personal
pilot evidence outside Git. Read [the browser runbook](docs/agent-e2e.md) before
real-host testing or service changes. Use only an explicitly approved private test
repository and browser session. The owner handles sign-in and native browser
consent; pause inspection during authentication. Preserve unrelated user work and
shared network routes, and coordinate any desktop logout/login.

Maintain structured registries and generated sections, preserve stable IDs and the
pinned historical adoption policy, and write review records with the CLI after
successful verification. Do not edit reconciliation receipts by hand. UI changes
must include meaningful implementation and test cochanges when behavior changes.
