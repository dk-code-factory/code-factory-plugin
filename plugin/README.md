# Code Factory

Your GitHub inbox and interactive local Codex agents, together in ChatGPT Work.

This private plugin includes the HTTPS MCP connection, the embedded inbox and
agent workspace, a guided setup skill, and a daily triage/agent skill. It uses
OAuth; no API keys, owner tokens or other credentials are embedded in the package.

In the ChatGPT desktop app, open Plugins and select the **Code Factory personal**
source, then install **Code Factory**. Authorize its connection when prompted.
Your browser must be connected to the owner's Tailscale network to approve it.
Start a new Work conversation and ask **Open Code Factory**.

The matching Linux daemon and Tailscale gateway must be running on the host named
in `mcp.json`. The embedded app manages repositories, profiles and settings.
Opening an item never launches work; review and Start are explicit operations.

Local plugin sources are documented for the desktop app. For browser-only Work,
the host may require registering the remote MCP connection once and importing a
package that references its observed app ID. The bundled endpoint and setup skill
provide that connection information; package files cannot create a cloud account
registration or bypass its OAuth consent.

This is a private personal plugin. Work UI and callback acceptance remain pending
until tested in your real Work conversation. The ZIP is an installable package,
not a public directory listing or evidence that a cloud installation succeeded.
