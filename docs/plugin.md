# Code Factory plugin

The complete private plugin is `dist/code-factory-plugin-0.2.8.zip`. Its root
contains a portable `plugin.json`, portable `mcp.json`, Codex compatibility
manifests, SVG branding, onboarding, daily-work and administration skills.
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
`/code-factory/plugin.zip` downloads the plugin with all three skills,
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
`.app.json` reference, all three skills and branding. It omits `mcp.json`, `.mcp.json`
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

The bundled `code-factory-admin` skill uses the same repository, system-default,
plugin inventory and maintenance tools as the settings widget. Codex plugin
management uses the pinned provider CLI; runtime app-server plugin APIs remain
under development and are not used for installation.

## Repository monitoring and plugin administration

For a manual review, ask Code Factory to run the configured review pipeline for
the PR. The model-visible `prepare_pr_review` tool accepts the synchronized PR's
`itemId` from `list_inbox`. It resolves repository, system and built-in review
defaults in that order and returns the prepared run, including its selected
skills/model, pinned head, repository instructions and authority. Preparation
does not contact GitHub, create a workspace or start an agent. After inspecting
the snapshot, the assistant uses `start_run` with its returned `id` under the
owner's review request; results/status come from `read_run`. Queue-only monitoring
still permits explicitly requested manual review. Unsafe or unavailable pipelines
return an error rather than silently substituting one. Posting findings to GitHub
requires separate publishing authority.

Plugin 0.2.4 adds this recipe and a PR-review starter prompt. Existing cloud
packages retain their uploaded skill text; refreshing the existing connection's
tools exposes the new server instructions and tool even before replacing the
uploaded package. The older generic `prepare_launch` path remains supported.

Open Code Factory settings with `open_settings`. Add `owner/repository` under
Repositories; GitHub access is taken from the existing account. Configured
review notifications are checked every two minutes by default and repository
summaries reconcile every 15 minutes. Notification reads preserve their GitHub
read/unread state. Review PRs shows requests addressed to
that user or their teams. Last-poll and access errors are visible; a repository
can be edited or paused, and Poll repositories now requests a fresh sync.

System defaults choose queue-only or automatic review plus review and implementation
pipelines. Queue-only is the default. A repository can inherit either pipeline,
override it, and inherit/override automatic review independently. Its additional
review prompt is appended to the generic profile prompt during preparation.
Automatic reviews wait until current agent work is inactive and require fresh provider state, a current non-draft user/team
request and a supported review profile: read-only with publishing set to `ask`,
or explicitly selected `danger-full-access` with approvals set to `never` and
publishing set to `authorized`. Claims are durable per
request episode and PR head; repeated polls and restarts do not replay a claim.
New heads or a newly observed request episode can receive another review.
Automatic review does not enable implementation. Publishing follows the selected
profile: `ask` requires owner approval; `authorized` permits autonomous publication.
Selecting either profile does not itself enable automatic review.

Plugins & skills shows a cached, paginated inventory from the pinned Codex CLI.
Add a local/Git marketplace (with optional ref), find a plugin or enter its
`name@marketplace`, then explicitly install, update or remove it. Existing
plugins are included. The panel tracks installation, enabled state, observed
versions, available updates, policy and operation errors. Authentication policy
is metadata; actual plugin connection readiness is checked by Codex on use.

Check for plugin updates refreshes Git marketplace metadata and compares
local/Git source manifests with installed versions. For installed remote catalog
plugins whose latest version is not exposed by the stable CLI, the panel says
Unknown and provides an explicit Update action to install its current release.
The app does not call under-development app-server plugin management endpoints.
Optional periodic checks are metadata-only; 0 means manual, and positive intervals
are 15–10080 minutes. No update is installed automatically.

Management creates a durable operation before invoking one CLI command. View
closure does not replay it. A failed/uncertain response or daemon restart requires
inspection/refresh rather than automatic retry. Maintenance and active agent work
exclude each other; no global Codex executable or authentication is replaced.
After discovery/installation, select plugin or standalone skills under Launch
profiles → Default skills, or choose per-launch skills. Prepared launches retain
their skill/support files and executable modes. Disabled or unavailable skills
are visible and cannot be newly prepared.

The bundled `code-factory-admin` skill and the owner MCP tools expose these same
settings and operations. Plugin source compatibility, authentication, hooks and
MCP tools remain subject to Codex policy; the inventory does not certify arbitrary
third-party code. New remote/installed-host acceptance stays separate from fixtures.

Admin tools preserve new monitoring fields when older cached widgets omit them.
For repository policy, an explicit `autoReview: null` means inherit; an omitted
field leaves the existing override unchanged. An empty reviewPrompt explicitly
clears the extra instructions.

## GitHub API allowance

In **Settings → System defaults → GitHub API usage**, set the shared application
budget (60–5000 request attempts/hour), reported-quota reserve (0–5000), and
notification interval (60–3600 seconds). Defaults are 600, 100 and 120 seconds.
REST remaining quota uses requests; GraphQL remaining quota uses points. The
reserve applies to each reported quota, while the local allowance counts HTTP
request attempts.
Legacy zero/unset budget or interval values use those defaults; an unset reserve
uses 100. The counter is a cached Settings snapshot and reads no quota endpoint.

All daemon REST/GraphQL reads, manual polls and selected details use one persistent
serialized gate. Request attempts, including conditional requests, count against
the local allowance; cached reads do not. GitHub reset/Retry-After/polling headers
and conditional validators are honored. Normal polling imports summaries rather
than every PR's reviews, comments, commits and checks. Full details refresh for
selected/tracked work; unavailable details retain saved content and explicit Retry.

Plugins, skills, agent commands and other clients use GitHub independently of this
cap. Inspect their usage separately before increasing the allowance. Verify each
installation separately using the [acceptance guide](acceptance.md).
