# Connect Work through Tailscale

Run `code-factory configure-tailscale` after installing the packaged build.
No Platform tunnel ID, organization ID, workspace ID or OpenAI runtime key is
needed. Tailscale must already be signed in as the intended owner.

Setup derives the host and owner from the local Tailscale daemon. It adds a
Funnel HTTPS endpoint on standard port 443 under `/code-factory`, plus the
corresponding RFC OAuth metadata paths, and a private Serve consent endpoint on
8444. Separate loopback listeners use 8788 and 8789. An existing public port-443
service keeps its routes. Setup refuses conflicting paths or making an existing
private service public. Other Tailscale ports and routes are preserved.
The MCP daemon stays authenticated on loopback port 8787.

In ChatGPT Work, open **Plugins → + → Add custom MCP server**. Use the HTTPS
`/mcp` URL printed by setup, select **OAuth**, and create a private connection.
Leave optional OAuth client fields empty: the gateway supports dynamic client
registration with PKCE S256. Sign in/connect when prompted. Your browser follows
a redirect to the private Tailscale consent page: click **Connect Code Factory**.
The browser doing this must be connected to your tailnet as the configured owner.
Other tailnet members cannot approve. No password or API key is entered in chat.

Funnel is reachable from the public internet; MCP requests require OAuth access
tokens. The public listener never trusts Tailscale identity headers and cannot
serve the consent form. Only the private Serve listener accepts Tailscale's
injected owner identity. Auth codes are short lived, bound to the approved client,
redirect, resource and PKCE challenge, and single use. Refresh tokens rotate.
Only token hashes persist in a private mode-0600 authorization state file.
Public endpoints do not accept the daemon's local owner token.

Setup installs `code-factory-gateway.service`, ordered after and starting the
main daemon. The gateway remains available while the daemon restarts. `code-factory doctor` reports this transport and local readiness;
actual Work discovery, UI and callback receipt require separate evidence.
The persistent installed plugin still needs its observed Work app ID:
`code-factory link-plugin --app-id OBSERVED`. Refresh it and test in a new chat.

OAuth registrations allow the documented ChatGPT callback paths. If Work reports
a different callback URI or cannot reach the endpoint, record the observed error;
do not disable authentication to work around it. An interrupted consent request
expires; reconnect from Work to start another. Existing access and refresh tokens
survive gateway restart. Removing `gateway-auth.json` while the gateway is stopped
revokes all connections and requires reauthorization.

Uninstall removes only the matching Code Factory Funnel/Serve routes and gateway
unit; other Tailscale services remain. Configuration and application data are
retained for reinstall. Run `configure-tailscale` again after reinstalling.

Contracts: [Tailscale Funnel](https://tailscale.com/docs/features/tailscale-funnel),
[Serve identity headers](https://tailscale.com/docs/features/tailscale-serve),
[Work OAuth](https://developers.openai.com/plugins/build/auth).

The same public HTTPS base hosts Code Factory's installation page and
complete plugin download. Open `/code-factory/` for instructions or
`/code-factory/plugin.zip` for the
package. Package downloads are public and contain no application data. MCP still
requires OAuth, and approval remains on the private Serve listener.

Older port-10000 installations migrate on `configure-tailscale`. The previous
profile is backed up privately. Its existing route is retained; the canonical
connection becomes `https://HOST/code-factory/mcp`. New connections and linked
plugin packages must use that URL. Setup failures restore the previous profile
and remove only newly added routes; reruns reuse matching handlers. Uninstall
removes matching current routes and the old port-10000 route, while preserving
unrelated services.

This choice follows an observed browser A/B test: ChatGPT's OAuth discovery
failed on port 10000 after 30 seconds, while the isolated equivalent discovery
fixture on the same hostname's port 443 succeeded in approximately 1.5 seconds.
It does not establish a general documented ChatGPT ban on nonstandard ports.
