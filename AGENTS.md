# Code Factory agent instructions

This is a personal Linux pilot. Read [docs/agent-e2e.md](docs/agent-e2e.md)
before browser testing, installation, service changes, or live acceptance work.
Read [docs/acceptance.md](docs/acceptance.md) for dated results and outstanding
checks. Start new sessions in this project directory so these instructions load.

## Working agreement

- Perform authorized local commands and Chrome testing yourself. Do not hand the
  user a reproduction checklist when the working browser controller is available.
- The user handles initial sign-in and Chrome's native debugging permission.
  Pause inspection during sign-in; resume after the user confirms it is complete.
  Do not ask them to sign in again when an approved signed-in session works.
- Continue after status questions unless the user explicitly asks you to stop.
  Report the current concrete action briefly; avoid lengthy speculative updates.
- Test only the dedicated private pilot repository. Preserve unrelated browser
  tabs, production repositories, user work, authentication, and Tailscale routes.
- Check for unrelated live runs before restarting services. Coordinate an actual
  desktop logout/login; it closes the user's browser and remote desktop session.

## Architecture and current transport

- Go daemon and embedded React UI; the UI uses the MCP Apps bridge exclusively.
  Do not add direct iframe requests to localhost or embed owner credentials.
- User services: `code-factory.service` and `code-factory-gateway.service`.
  Stable launcher: `~/.local/bin/code-factory`.
- Public MCP: `https://dkropachev-ai-rig.tail62c9f9.ts.net/code-factory/mcp`.
  Private owner approval: the same hostname on HTTPS port `8444`.
- Preserve the unrelated public `/` handler on port 443 and private port 8443.
  Use scoped Code Factory paths. Never reset Tailscale or disable all of port 443.
- Codex provider is pinned to **0.161.0**, with its executable recorded in private
  `runtime.json`. Reuse GitHub/Codex authentication; do not replace global Codex
  to match this pin. Jira and the alternate OpenAI tunnel remain disabled.

## Browser and credentials

- Prefer `node scripts/browser_control.mjs` attaching to the user's existing
  Google Chrome through `chrome://inspect/#remote-debugging` and native Allow.
  Read the runbook for the JSON-line protocol, safe output handling, frame IDs,
  and the native typing workaround for React inputs inside the app iframe.
- Controller readiness is not browser attachment. Discover current page IDs and
  fresh element UIDs. Do not reuse historical terminal session IDs or UI UIDs.
- RDP: `192.168.0.100:3389`, first username `remote`, then Ubuntu user
  `dkropachev`. Private credential location and rotation caveats are in the runbook.
- Never put passwords, owner tokens, OAuth codes, signing keys, cookie/profile
  exports, raw authorization headers, or secret answers in source or evidence.
  Mask URL queries/fragments before exposing browser tool output. Do not dump
  `gateway-auth.json`, `owner.token`, subscription rows, or credential files.

## Verification and evidence

- `make verify`: build, checks, tests, packaging, installer recovery, and smoke.
  Run checks appropriate to changes; documentation-only edits need link/content
  validation, not another full installation or live model run.
- `make connection-test` and `make connection-sdk-test`: opt-in deployed public
  OAuth/MCP tests. They are not real ChatGPT acceptance. Tailnet-only success
  cannot establish public reachability.
- Browser E2E must observe the actual installed plugin, embedded inbox, explicit
  read-only Plan launch, real question, UI answer, follow-up, and recovery.
- Keep tests separate: fixtures, independent SDK, and real Work results. Record
  dated nonsecret evidence tied to the installed binary version. Do not claim an
  embedded UI is missing just because the conversational model cannot see it.
- Full readiness remains blocked until required checks really pass. As of
  2026-10-20, the browser core works; real host event subscriptions/callbacks,
  one-hour soak, login cycle, and other checks listed in acceptance remain open.
- Update the runbook and acceptance ledger after material changes. Registered
  `asdk_app_...` IDs and cloud package `Plugin_...` IDs are distinct; never invent
  one or use a package ID as the remote app mapping.
