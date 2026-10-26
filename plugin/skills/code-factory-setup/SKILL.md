---
name: code-factory-setup
description: Connect and verify the Code Factory plugin and its local Linux service through Tailscale OAuth. Use for installation, connection problems, and onboarding; use the Code Factory skill for daily inbox and agent work.
---

Start with the MCP connection bundled in this package. Its endpoint is in root
`mcp.json`; OAuth is discovered from the server. If tools are available, call
`diagnostics` and `open_settings` and guide configuration in that dedicated view.
Use `open_factory` separately to verify the inbox.
Do not make the user reenter a working server URL or recreate existing resources.

For OAuth, guide the user to **Connect Code Factory** on the private Tailscale
approval page. Their browser must be on the tailnet as the configured owner.
No Platform tunnel IDs, organization/workspace IDs or runtime API key are needed.
Never ask for credentials in chat or disable authentication to bypass a failure.

If the local service needs installation, use authorized local computer access.
Install the checksummed Linux release with `./code-factory install`, then run
`code-factory configure-tailscale` and `code-factory doctor`. Reuse GitHub and Codex
sign-ins and existing data. Setup preserves other Tailscale services. If a host
requires a registered MCP app before linking a local package, create/reuse the
private connection for the bundled endpoint, obtain its real ID from the Work UI,
and run `code-factory link-plugin --app-id <observed-id>`. Never invent an app ID.
Refresh the installed plugin and test it in a new Work conversation.

Verify the embedded inbox, an explicitly launched read-only Plan-mode Codex task,
a real question and answer, and a follow-up. Work must separately demonstrate
subscription, signed callback receipt and filtering, reconnect/recovery, and the
remaining acceptance checks. Keep local/fixture results separate from real Work
evidence. Report the exact observed blocker if browser, computer, private plugin
installation, Tailscale or MCP Apps support is unavailable.
