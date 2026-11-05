# Code Factory

Code Factory routes GitHub changes to retained native Codex conversations. Each PR
has one chosen agent and worktree. Configured rules contribute instructions and
arbitrary installed skills; those skills decide how to review, implement, publish
and verify work. Issues can also have manually started conversations.

Open an issue or PR to inspect it without launching work. Send a message to start
or steer its conversation. Execution state, explicitly reported work status and
queued events are displayed separately. Native questions and approvals remain
attached to the exact conversation and request.

Configure repositories, event rules, native agents and priority policies through
the persistent Configuration chat. The agent can read and propose changes. Every
edit requires explicit confirmation of its concrete diff. Confirmed changes wait
until active turns finish; stale proposals must be regenerated.

Automatic delivery uses a 120-second quiet period by default, with repository
overrides. Every matching event resets that deadline. Busy turns, questions,
approvals and explicit interruption hold automatic dispatch. Chat, answers and
explicit run/resume bypass the timer. Uncertain sends are reconciled against
retained history before an explicit retry or discard.

GitHub webhook signatures are checked and deliveries persisted before acceptance.
Polling recovers observable changes. Initial import establishes a baseline without
launching historical work. Existing GitHub CLI authentication, conditional caching
and request budgeting are reused. Priority precedence remains manual override,
mapped labels, then a configured read-only priority agent/skill.

The application starts clean in `factory-v1.db`; it imports no old application
settings, runs or history. Native Codex settings, authentication and retained
native history, connection credentials and unrelated work remain in place.
Pipeline authoring, built-in review execution/publication, question classifiers,
repair turns and the issue-report ledger are retired.

## Build and verify

Development requires Go 1.26, Node 22.12+, Python 3.11+, locked npm dependencies and
matching Playwright Chromium. Ordinary checks use fixtures and require no model
credentials.

```sh
python3 -m pip install -r scripts/requirements-dev.txt
cd web && npm ci && npx playwright install chromium && cd ..
make build
make ci FLOW_BASE=main
```

Use the [layered specification guide](docs/specs/README.md) and
[final verification handoff](docs/ci.md#final-verification-and-pr-handoff).
Reconciliation reviews real assertion anchors and writes receipts with the CLI.
`make test-real-provider` opts into the installed Codex handshake, account read and
skill discovery; it starts no model turn. Real model lifecycle, public connection
and installed-host acceptance require separately authorized verification. No
default CI command runs agents or accesses model credentials.

## Run and connect

The installed Linux binary needs Git, authenticated GitHub CLI and native Codex.
Codex must satisfy the stable version boundary and the experimental app-server
client-tool compatibility probe. Native profiles and agent `config_file`
definitions are read directly; Code Factory has no second agent-authoring format.

```sh
bin/code-factory serve --data-dir /tmp/code-factory-dev
```

The daemon listens on loopback (`127.0.0.1:8787` by default). Local MCP calls require
the generated private owner bearer token. The embedded React application talks
only through its MCP Apps host bridge; no owner token enters the iframe.
Installation uses user services and preserves existing provider programs, sign-in,
shared network routes and login/lingering policy.

Existing portable and browser plugin packages, owner consent, Tailscale gateway and
tunnel linking remain available. See [plugin setup](plugin/README.md),
[operations](docs/operations.md), [CI and release checks](docs/ci.md) and the
[browser runbook](docs/agent-e2e.md). Building and fixture verification install or
restart no services. Merge, release and real-host acceptance require their own
explicit instructions.

To receive GitHub events, provision a private mode-0600 `github-webhook-secret`
file in the daemon data directory and configure the repository webhook to the
existing public base plus `/github/webhook`. HMAC validation protects that route;
the remaining MCP and owner-consent authentication boundaries are preserved.
