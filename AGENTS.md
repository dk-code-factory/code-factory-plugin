# Code Factory contributor instructions

Use the [layered specification guide](docs/specs/README.md) and
[reconciliation skill](.agents/skills/reconcile-specs/SKILL.md) for source, tests
and behavior changes. Preserve glossary → meaning, UI → glossary, and flows →
UI/glossary dependency ownership. Review real assertions before claiming coverage.

Run `make ci FLOW_BASE=main` locally. Hosted checks and release delivery are
explained in [docs/ci.md](docs/ci.md). Ordinary verification forces fixture mode;
real provider, public connection and installed-host checks are explicit opt-ins.
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
