# Dependency administration API

The loopback daemon exposes a separate owner API at `/admin/dependencies`.
Authenticate with the existing owner bearer token in an Authorization header.
This route is absent from MCP discovery, the public OAuth gateway and browser
iframe access. Responses use `Cache-Control: no-store`. Browser Origin requests
are rejected. Keep the token in memory; never put it in a URL, command argument,
source file or evidence record.

| Method | Route | Result |
|---|---|---|
| GET | `/admin/dependencies` | Cached operations, registered GitHub skills, active Codex path and management boundaries; no upstream request. |
| POST | `/admin/dependencies/operations` | Start a durable check/update job, or return an existing job with the same request ID. |
| GET | `/admin/dependencies/operations/ID` | Poll that operation and each dependency's observed result. |
| POST | `/admin/dependencies/github-skills` | Register explicit GitHub provenance for an existing standalone skill. |

An update request is:

```json
{
  "id": "owner-upgrade-20261023",
  "action": "update",
  "targets": ["all"],
  "migrateLegacyDK": true
}
```

`action` is `check` or `update`. Targets are `all`, `codex`, `plugins`, `skills`;
omission means all managed kinds. The caller supplies a stable ID of 1–96 letters,
digits, underscores or hyphens, beginning with a letter or digit. A repeated ID
returns its recorded operation without repeating side effects; changing that ID's
request is rejected. Mutation responses are HTTP 202. HTTP 409 means active agent
or maintenance work prevented acceptance. Stop or finish that work explicitly
before submitting maintenance. A request rejected before acceptance has no job.

Jobs run independently of the HTTP request with a 20-minute deadline. Status is
`queued`, `running`, `succeeded`, `partial` or `uncertain`. Individual results
distinguish available/current/updated/migrated dependencies, preserved disabled or
unmanaged items, local-edit conflicts and failures. A partial job retains successful
members. After a crash or timeout, inspect installed state and the retained job;
uncertain operations are never replayed automatically. A new ID is a new explicit
operation, including after an uncertain outcome.

Codex updates use the latest stable release from the owner-selected
`dkropachev/codex` GitHub fork and its release
asset SHA256 digest. The full Linux amd64 package retains the CLI, code-mode host
and other packaged resources together. Each Codex result retains that source, and release asset URLs must belong to that
fork. The candidate must report the requested
version and pass initialize, existing-account, skill-discovery, config and plugin
inventory checks before activation. These checks make no model call and do not
establish every native API or real-task behavior. Failed probes preserve the prior
provider. Successful activation backs up `runtime.json`, creates a new private
provider launcher set and updates the recorded provider path. Idle app-server
processes retire; explicitly continued or new work starts on the new provider.
Global Codex executables and existing authentication/configuration are preserved.
The minimum supported stable CLI is 0.161.0; later versions require the upgrade
probe. Prior distributions and provider selections remain available for recovery.

Plugin checks refresh registered marketplaces and observed inventory. Updates use
Codex's supported marketplace-upgrade and plugin-add commands, then observe the
installed version and enabled state. Each Git marketplace has its own progress and
failure result; a failed source preserves its own plugins while independent
marketplaces continue. Disabled installations are preserved because
native add would enable them. A local marketplace keeps its registered source,
including compatibility patches; it is not replaced with upstream automatically.
An unconfirmed native mutation is uncertain, with no automatic retry. Skill
discovery then rebinds matching configured plugin skills by name to the selected
current installation. Prepared/completed run snapshots remain unchanged.

GitHub-installed standalone skills need provenance. Explicit registration is:

```json
{
  "name": "example-skill",
  "root": "codex",
  "repository": "owner/repository",
  "path": "skills/example-skill",
  "ref": "main"
}
```

`root` selects `~/.codex/skills` or `~/.agents/skills` (`codex` or `agents`). The
skill must already exist as a regular directory with `SKILL.md`; registration
records its current fingerprint. Known GitHub source entries in the skills CLI's
owner `.skill-lock.json` files are also imported during explicit skill maintenance.
Unknown/local skills are retained. GitHub updates resolve the requested ref to a
full commit SHA, fetch that archive through existing gh authentication and check
the installed fingerprint again. Edits after a recorded baseline cause a conflict.
Automatic lock import records the current local tree; it cannot distinguish
earlier local edits from an approved baseline. First-time or previously imported
skills can therefore have those edits replaced. This PR preserves that behavior
and excludes the explicit-adoption repair; it schedules no follow-up fix or issue.
Links, traversal,
oversized content and native-plugin leaf-only installations are rejected. Regular
content is backed up and replaced with an atomic Linux directory exchange; changed
provenance is persisted. A failed provenance write attempts to restore the original.
Fresh skill discovery refreshes selection metadata.

`migrateLegacyDK: true` additionally archives obsolete standalone `dk-*` skills
whose names match the current installed canonical DK Workflows plugin, plus the
canonical standalone AD and Matt Pocock reviewer duplicates. Existing
explicitly registered GitHub skills are excluded. Configured profile attachments
switch to their native equivalents, and the original directories remain in the
operation's private backup. Recovery restores an archived directory if a profile
still references its old path after interruption. Use a fresh run to load the
updated skill bundles; continuing an old run keeps its frozen bundle contents.

Git, gh, Python, Node, systemd and Tailscale are externally managed host
prerequisites. This owner-user API reports that boundary and preserves their
installations, services and routes. It does not run sudo, an OS-wide package
upgrade, arbitrary installer commands or source-provided setup scripts.
