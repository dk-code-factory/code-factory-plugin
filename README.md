# Code Factory

[![CI](https://github.com/dk-code-factory/code-factory-plugin/actions/workflows/ci.yml/badge.svg)](https://github.com/dk-code-factory/code-factory-plugin/actions/workflows/ci.yml)

Hosted checks and package delivery are described in [CI and releases](docs/ci.md).

A single-user Linux service for GitHub issue/PR triage and interactive local Codex sessions. The React/TypeScript UI is embedded in the Go binary and runs inside ChatGPT Work through MCP Apps. Tailscale Funnel connects Work Cloud through an OAuth gateway; a private Tailscale Serve page limits connection approval to the owner. The OpenAI Secure MCP Tunnel adapter remains an alternative.

The personal pilot has local automated coverage and opt-in real Codex lifecycle tests. **Readiness requires the dated real-host evidence in [docs/acceptance.md](docs/acceptance.md).** Private tunnel permissions, installed Work behavior, callbacks, soak and login recovery must be observed on the target account.

## User setup

For local agent work, start with [AGENTS.md](AGENTS.md) and the
[Chrome E2E and remote-access runbook](docs/agent-e2e.md). They describe the
owner-approved Chrome control, private configuration, live tests,
recovery and remaining acceptance checks.

The portable private plugin is built with `make plugin` as `dist/code-factory-plugin-0.2.8.zip`. Browser Work uses the separate linked `code-factory-browser-plugin-0.2.8.zip`, described in [the plugin guide](docs/plugin.md). Install the linked browser package in your own account and retain its actual registered app mapping. Local marketplaces and MCP launch configuration are separate from that browser installation.

For the selected Tailscale route, follow [the Tailscale setup guide](docs/tailscale.md): install the release, run `code-factory configure-tailscale`, and add its HTTPS MCP URL to Work with OAuth. No Platform tunnel identifiers or runtime key are needed. Existing Tailscale services are preserved. The instructions below describing OpenAI tunnel credentials apply only to that alternative transport.

Install the release and its persistent private package at `~/.local/lib/code-factory/plugin` (the repository marketplace is `.agents/plugins/marketplace.json`) and invoke its `code-factory-setup` skill with Work local computer access. The skill performs installation, provider sign-in checks, tunnel configuration and connection verification. Repository, priority, profile and skill settings are managed inside the embedded UI.

Onboarding requires Linux with a working user systemd session, an installed and authenticated stable Codex **0.161.0 or later**, Git, existing GitHub CLI authentication, an OpenAI Secure MCP Tunnel with a restricted runtime key, owner-only access to its workspace/organization associations, and a Work host supporting MCP Apps and MCP Events. Later Codex versions require compatibility verification before managed activation. Pilot dependency updates use the owner fork at https://github.com/dkropachev/codex. The setup skill opens a short-lived local password form for the runtime key. Jira is disabled for this pilot; the embedded settings have no Jira credential form.

The tunnel is deliberately configured with a stdio command pointing at `code-factory mcp`; this proxy reads the local owner token and calls the authenticated loopback daemon. Cloud callers never receive the token. Everyone allowed to use that tunnel has the owner's application access: a shared workspace needs a separate authenticated identity boundary before use. Do not connect this single-user service to an unrestricted shared tunnel.

The repository includes a portable plugin manifest and onboarding skill. A cloud connection ID is created by the host during setup; it is not invented or committed in the package. The bundled setup helper creates the registered app mapping after the private connection exists. Tunnel and Work permissions cannot be granted by the installer.

## Development

[User-flow contracts](docs/user-flows/README.md) map implementation-agnostic
behavior to code, tests and known gaps. Use the repository
[reconciliation skill](.agents/skills/reconcile-user-flows/SKILL.md) when changing
any of these. `make flows-check` checks current edits; `make ci FLOW_BASE=main`
checks branch changes from their common ancestor and runs full local verification.

The [UI entity specification](docs/ui/README.md) describes widgets and their
elements, panes, pages/dialogs and connections, with hierarchy/navigation diagrams
and links to implementation and existing test assertions.

The [maintained specification system](docs/specs/README.md) aligns the
[glossary](docs/glossary/README.md), UI registry and behavior catalog with one
reconciliation gate. `make specs-report` shows bounded impact and exact next
commands; `make specs-gaps` inventories typed open gaps and verification owners.
UI/behavior registries use version 2 with stable gap IDs and explicit next checks;
legacy version-1 snapshots remain supported. Use `make specs-validate`,
`make specs-check` and the fast `make specs-ci`; historical flow commands remain compatible. Project skills and
five scoped agent definitions document maintenance ownership. CI is deterministic
and runs without agents or model credentials.

Requirements: Python 3.11+ with `python3 -m pip install -r scripts/requirements-dev.txt`, Go 1.26+ (Go's automatic toolchain download works from 1.25), Node.js 22.12+ and npm. Building the frontend is required before compiling changes to the embedded UI.

```sh
make verify
make test-real-provider
python3 scripts/test_systemd.py
```

`make test` runs Go race tests and browser tests. Default Make and verify commands force fixture mode, including when `CODE_FACTORY_REAL_CODEX=1` is inherited; real model execution requires `make test-real-provider`. Install the matching Playwright Chromium browser with `cd web && npx playwright install chromium` if it is absent. The opt-in Codex tests include real model task execution, question/answer, skill loading, follow-up, interrupt, resume, stale-answer rejection and priority inference in an isolated local repository. Provider, run, subscription and browser tests use isolated fixtures and do not publish to GitHub or Jira.

For local daemon development:

```sh
bin/code-factory serve --data-dir /tmp/code-factory-dev
bin/code-factory doctor
```

`serve` accepts only loopback IP listeners (default `127.0.0.1:8787`). `/mcp` requires a generated owner bearer token even locally. There is no unauthenticated application endpoint or browser-to-localhost bridge. `web/` can be served with `npm run dev`; it needs an MCP Apps host to function. Browser tests provide a parent-frame host fixture.

The CLI also supplies `install`, `configure-tunnel --id tunnel_…`, `onboarding`, `tunnel`, and `mcp`. These are intended for the onboarding skill/operator. Installation writes Linux **user** units and starts only the daemon. It does not change system services or enable login lingering. Install from an extracted, checksummed release archive. Run `onboarding` after configuring the tunnel; the local form encrypts the key and starts the tunnel user service. No services are installed by building or running tests.

## Behavior

- GitHub synchronization runs immediately, then on a 30-second ticker, with page traversal, overlapping incremental checkpoints, hourly reconciliation of open items, conditional HTTP caching, open-PR re-enrichment, and rate-limit retry times. The first import loads open items; later updates retain synchronized closed/merged history locally. Checks, statuses, reviews, comments, commits, draft/head data and timeline references contribute to content revisions. Failed syncs retain their checkpoint and display an error.
- The default inbox shows open issues and PRs. Initial imports form an unread baseline; subsequent discoveries get a New badge. New, unread and acted-on state are independent. Listing does not mark read. Detail views mark the exact revision displayed; an answer or action never suppresses later unread changes. GitHub website reading is not inferred.
- Priority resolves from override, then the most urgent mapped label, then a cached read-only Codex assessment. Assessment context includes local links, synchronized linked items and Jira context; policy or context changes invalidate inference. Failed assessments remain visible and retry later. Manual work takes precedence. Assessments disable discovered MCP servers/apps and never publish labels.
- Relationships preserve reference evidence. Manual links are separate records; adding or removing them changes neither GitHub nor Jira. Jira Cloud access uses only GET and configured `*.atlassian.net` tenants. Unavailable tickets display errors.
- Working tracks started issue/PR work through idle, interrupted and failed sessions until GitHub completion. Issues includes linked PR summaries; My PRs uses author identity and Review PRs uses individual/team requests. PR lifecycle, review approval and current-head CI stay independent. Explicit implementation links, including GitHub development links, can complete an issue on PR merge; incidental references cannot.
- Opening an item never starts work. Select a pipeline (one launch profile and one retained session). Launching first creates a durable prepared snapshot of prompt, profile and selected skill bundles. The user reviews workspace, base, sandbox, approvals and publishing authority, then explicitly starts it. Start returns immediately while a dedicated Git worktree/branch and app-server process are created. Existing checkouts are preserved; managed clones reuse GitHub CLI authentication.
- Local Codex authentication and repository instructions are reused. Profile publishing instructions are supplied to Codex; execution remains subject to Codex sandbox, approval and managed policies. An `ask` publishing setting is an agent instruction, not a separate GitHub authorization firewall. Do not grant broad network/MCP authority to a task whose external writes must be technically prohibited.
- Runs survive view/tunnel closure. Idle messages use `turn/start`; active messages use `turn/steer` with an expected turn ID. Questions and approvals become durable attention records, including original options, blocking state and request ID. Answers are durably claimed before delivery and never replayed automatically. Duplicate/stale answers fail. Interrupt, explicit resume and worktree cleanup are separate actions. Cleanup refuses uncommitted changes.
- Native RPC and structured agent-message questions appear in Messages, including nonblocking questions. For a stopped textual ask, an isolated helper can request a native form in at most two question-only Plan turns, then expose free-text continuation and the same-session console. Recovery state and attempt counts persist; mode restoration is explicit and uncertain sends are never replayed.
- App-server or daemon loss invalidates live request IDs and marks runs interrupted. Resume reconnects stored Codex history in the retained worktree; it does not replay the initial task. Prepared runs remain explicit launch candidates. History and snapshots stay local after worktree cleanup.
- UI polling uses app-only MCP tools once per second while visible, with hidden/disconnected backoff. Transcript and diff data are journaled in bounded chunks and paged. MCP Events sends state changes and attention alerts, never output tokens. The optional session console uses that same bounded local journal and session controls. Callback verification, Standard Webhooks HMAC, expiring durable subscriptions, bounded retries, key rotation and stable delivery IDs are implemented. Callback connections validate public addresses and prohibit redirects. Events use `cursor: null` (no protocol replay); local journal history remains available.

State lives under `$XDG_DATA_HOME/code-factory` or `~/.local/share/code-factory`: SQLite WAL state/events, encrypted credentials, a mode-0600 encryption key and owner token, managed clones, retained worktrees, and immutable launch skill snapshots. Keep this directory private. Losing the encryption key loses stored credentials. Service credentials and secret question answers are redacted from incoming Codex output; answer bodies are not journaled. As with any local coding agent, same-user filesystem access is a trust boundary, not protection against a compromised local account.

## GitHub read boundary

The application depends on `providers.GitHubSource`. The daemon supplies
`CachedGitHub` around the HTTP adapter, using the same read methods for identity,
teams, repository indexes, item details, lifecycle checks, review requests,
notifications and posted reviews. Local usage accounting stays live local state.

Normal queries return an unexpired cached result. Cache misses, expiry and
explicit invalidation fetch through the existing budgeted adapter and retain
successful results. Cache entries are isolated by API endpoint and authenticated
principal, persist in private state, and are bounded to 64 MiB and 4,096 entries.
Cached results preserve their provider observation times and decode into
independent values. Errors and partial enrichment failures do not become
successful cache entries.

Use `providers.WithGitHubRead(ctx, providers.RequireLive)` when a current remote
check is required. This bypasses both query and REST/GraphQL TTLs, allows
conditional HTTP validation, and writes the response back to the shared cache.
Initial/resumed execution, new review-notification targets, post-turn receipt
verification and explicit repository polling use that mode. A failed live check
returns an error; it does not silently return an older success. Ordinary saved
tracking/UI reads still return immediately and refresh external observations in
the background. `CachedGitHub.Invalidate(ctx, resource)` expires the current
principal's item/repository queries, including affected repository indexes;
an empty resource expires that principal's query cache. Other repositories and
principals are preserved. Cache hits need no GitHub HTTP request and remain
available while another query waits on the network.

## Layout

| Directory | Responsibility |
| --- | --- |
| `internal/domain` | Transport-neutral item, profile, run, attention and event types |
| `internal/store` | SQLite migration, atomic state/event transactions and encrypted vault |
| `internal/providers` | Read-only GitHub/Jira adapters |
| `internal/codex` | Version-isolated JSON-lines app-server client |
| `internal/app` | Inbox, priorities, links, assessments, worktrees and run lifecycle |
| `internal/events` | Work callback verification and durable webhook delivery |
| `internal/transport` | Official Go MCP SDK tools/resources and isolated Events extension |
| `internal/setup` | Linux user services and local secure onboarding form |
| `internal/web`, `web` | Embedded MCP Apps resource and React UI |
| `plugin` | Private Work package and onboarding skill |

The Go SDK handles MCP `2026-07-28` and legacy `2025-11-25`/`2025-06-18` tools/resources. Events extends discovery and adds custom methods without coupling the domain to Work. Legacy compatibility does not constitute a delivered Claude plugin. Claude Channels and desktop packaging are later adapters.

See [operations.md](docs/operations.md) for setup, daily use, diagnostics, upgrade, recovery and uninstall. See [acceptance.md](docs/acceptance.md) for the real-host release gate and [DESIGN.md](DESIGN.md) for invariants and recovery behavior.

Suspected Code Factory defects can be submitted through the bundled
`code-factory-report-issue` skill and followed in **Reported issues**. The private
ledger, automatic investigation/filing prerequisites, recovery and verified
release tracking are described in [issue reporting](docs/reporting.md).
The [private dependency administration API](docs/dependency-admin.md) checks and
updates Codex, installed plugins and GitHub-installed skills through identified,
recoverable owner operations.
