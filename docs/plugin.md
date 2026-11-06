# Code Factory plugin

The complete private plugin is `dist/code-factory-plugin-0.2.8.zip`. Its root
contains a portable `plugin.json`, portable `mcp.json`, Codex compatibility
manifests, SVG branding, onboarding, daily-work, administration, issue-drafting and status-reporting skills.
It bundles the Tailscale HTTPS endpoint and discovers OAuth from the server's
protected-resource metadata. No client secret or local owner token ships.

The installed source lives at `~/.local/lib/code-factory/plugin`. Running
`code-factory install-plugin` adds it to `~/.agents/plugins/marketplace.json`.
Existing marketplace identity and unrelated entries are retained. The plugin is
available as `code-factory@code-factory-personal` when setup creates a new catalog.
It is discoverable, rather than automatically enabled in unrelated Codex sessions.

On a ChatGPT desktop host that reads this user's filesystem, restart the app,
open the **Code Factory personal** plugin source, install **Code Factory**, and
finish OAuth. Start a new Work conversation and ask **Open Code Factory**.
The browser authorizing the connection must be on the owner's Tailscale network.

Browser-only Work cannot read this machine's personal marketplace. OpenAI's
current private/local MCP packaging flow may require registering the remote MCP
connection first, then linking the observed `plugin_asdk_app_...` ID with
`code-factory link-plugin --app-id OBSERVED`. The package cannot create a cloud
registration itself. No cloud app ID is fabricated, and private Work installation
and authorization remain unverified until observed. The ZIP is not a public plugin
directory submission; public distribution remains outside this personal pilot.

Build the ZIP with `make plugin`. Install build-time validation dependencies with
`python3 -m pip install -r scripts/requirements-dev.txt` if they are missing.
`python3 scripts/test_plugin_host.py` verifies marketplace discovery and package
installation using the real Codex plugin host in an isolated `CODEX_HOME`; it does
not sign in, invoke a model or change the user's enabled plugins.
The canonical portable schemas are vendored for offline manifest validation.

The full Linux release also contains the plugin; `make package` emits both the
Linux archive and plugin ZIP. `code-factory configure-tailscale` updates both MCP
configuration files to the installed host's current HTTPS endpoint. Registered
Work app mappings remain separate from portable ZIPs, which never contain local
credentials or host application state.

Source contract: [OpenAI plugin packaging](https://developers.openai.com/plugins/build/plugins).

## Hosted package

The running Tailscale gateway hosts the complete package and a browser installation
page at its HTTPS base URL. Under the default `/code-factory` base,
`/code-factory/plugin.zip` downloads the plugin with all five skills,
branding and MCP connection; the adjacent `plugin.zip.sha256` and `plugin-release.json` describe
those exact bytes. Public `plugin.json` and `mcp.json` provide package metadata.
The archive is embedded in the versioned binary, so services do not depend on a
source checkout or serve the private application directory. On startup its endpoint
is bound to the configured Tailscale host and archive checksums are regenerated.

Hosting does not register or install a plugin inside a ChatGPT account. The landing
page gives the private browser MCP connection steps and explains where package
import depends on Work account support. MCP and private consent retain their
existing authorization boundaries. Only explicit public artifacts are served.

## Registered browser package

An uploaded `Plugin_...` identifier names a package, not an MCP server.
A private browser package references its separately registered remote server:

```sh
python3 scripts/package_plugin.py --registered-app-id plugin_asdk_app_OBSERVED
```

The generated `code-factory-browser-plugin-<version>.zip` includes the registered
`.app.json` reference, all five skills and branding. It omits `mcp.json`, `.mcp.json`
and the compatibility `mcpServers` setting. Both manifests point to `.app.json`.
No fake or placeholder ID is accepted as a substitute for the observed ID, and
this does not create or authenticate the remote connection itself. Access to that
registered server must be verified in Work.

`code-factory link-plugin --app-id OBSERVED` records the nonsecret reference for
the gateway. Its `/code-factory/plugin-browser.zip` route then generates the linked package
without publishing arbitrary host files. Until a registered ID is recorded, that
route returns HTTP 409 with a setup explanation and no download. The portable
`/plugin.zip` remains a separate artifact and is not labelled as a verified browser
installation. Public submission requires the separate With MCP review flow.

## Conversations and configuration

Open the inbox with `open_factory`, then select an item to inspect its saved
context. Selection starts no agent. Sending `session_chat` creates or resumes that
item's canonical conversation; event rules can also authorize its first action.
The agent uses the repository's selected native Codex profile/agent definition and
a retained worktree. Rules may attach any installed skills. Skills decide how to
review, implement, publish and verify work.

`open_settings` opens a persistent configuration conversation with readable
effective settings and proposed diffs. The bundled administration skill uses
`configuration_chat`, `inspect_configuration` and `propose_configuration`.
Proposing a change grants no authority to apply it: the owner must explicitly
confirm the concrete diff in the app. Stale proposals are rejected and confirmed
changes wait for safe turn boundaries. Native Codex configuration and skills stay
native; there is no separate pipeline/profile-authoring format or built-in plugin
updater. Install or update skills through the native Codex tooling.

The bundled status skill teaches agents to call `code_factory_report_status` with
working, done or blocked. Blocked requires a concrete human question or requested
action. Native questions and approvals appear in the same attention queue with
their original options. A completed turn without a status report appears idle.

## GitHub API allowance

Inspect and propose the shared application GitHub budget through configuration
chat. Defaults are 600 request attempts/hour, 100 reported quota units reserved,
and a 120-second notification interval. REST quota uses requests and GraphQL quota
uses points; the local allowance counts actual HTTP attempts. Reads honor the
provider's server cooldown, conditional cache and persistent shared gate.

Repository summary synchronization and detailed reconciliation recover observable
changes even when the app is closed. Initial import establishes a baseline without
launching historical work. Signed webhooks enter the same durable event stream;
redeliveries and overlapping observed facets are deduplicated. Review requests,
comments, threads and current-head CI can match ordered event rules. Batches wait
for a configurable quiet period, default 120 seconds, and an available session.
Human questions and explicit interruption hold automatic delivery. Chat, answers
and explicit run/resume bypass the timer.

Agent and skill GitHub commands, native plugin management and other clients use
GitHub independently of the daemon's request budget. Verify each installation
separately using the [acceptance guide](acceptance.md).
