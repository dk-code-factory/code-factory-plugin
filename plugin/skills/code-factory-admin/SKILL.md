---
name: code-factory-admin
description: Configure Code Factory repository monitoring, automatic review policies, Codex plugins, update checks, and review or implementation skills through its admin tools and settings widget.
---

Use the connected Code Factory tools; names may have a host namespace. Open
`open_settings` for the admin widget. These settings belong to the owner's local
Code Factory installation and its pinned Codex provider.

Read current settings, profiles and repository configuration before editing them.
Add repositories as `owner/repository` with `save_repository`; preserve unrelated
fields and checkpoints. The signed-in GitHub account needs repository access.
Notifications are checked at the configured `githubPollSeconds` interval (120
seconds by default); repository indexes reconcile every 15 minutes. Manual
`sync_repositories` also respects the shared GitHub budget and cooldown. Review PRs is for
requests addressed to that user or their teams; it is not all open PRs.

System `autoReview` defaults to false (queue only). A repository's nullable
`autoReview` inherits when null, forces queue-only when false, and enables automatic
review when true. System and repository `reviewProfileId` and
`implementationProfileId` select default pipelines; empty repository values inherit.
Repository `reviewPrompt` appends trusted owner instructions to the generic review
prompt. `paused` stops monitoring. Explain the effective policy before saving an
automatic-review choice. Automatic reviews support read-only profiles with publishing
set to `ask`, or explicitly selected `danger-full-access` profiles with approvals
set to `never` and publishing set to `authorized`. The former requires owner approval
to publish; the latter permits autonomous publication. Automatic review does not
enable implementation, and selecting a profile does not enable automatic review. Request/head
claims prevent duplicate launches across polls and restarts.

For an explicitly requested manual PR review, use the daily Code Factory skill's
`list_inbox` → `prepare_pr_review` → `start_run` sequence. The preparation tool
resolves these configured review defaults automatically and returns a durable
snapshot with the PR head, selected skills and repository prompt. Queue-only
monitoring does not prevent an authorized manual review. Do not enable automatic
review merely to start one requested review.

Use `read_plugin_admin` for cached installed/available plugins, enabled state,
installed/available versions, marketplace names, and durable operation status.
Use its query, available, offset and limit arguments to search the catalog.
Unknown update or authentication readiness is not a successful check.

`manage_codex_plugins` accepts refresh, check-updates, discover-skills,
add-marketplace, remove-marketplace, install, update and remove. Targets for plugin
mutations are exact `name@marketplace` selectors; marketplace sources are absolute
local paths or supported Git sources, with an optional ref. Honor the user's chosen
plugin and source. Install, update and remove only when that action is requested;
checking updates does not authorize installing them. Plugin code, hooks and tools
run under Codex's actual permissions. Do not replace Codex, sign in on the user's
behalf, embed credentials in a source URL, or change unrelated plugin choices.

Management returns an operation ID. Poll `read_plugin_admin` until it succeeds,
fails or becomes uncertain; never automatically replay an uncertain action.
Maintenance is refused during active agent work. On an uncertain response or daemon
restart, refresh inventory and inspect observed state before considering a retry.
Local/Git marketplaces expose manifest version comparisons; when the CLI does not
expose a remote catalog's available version, retain the explicit unknown status.

After installation or discovery, use the returned skills in `save_profile.skills`
for a review or implementation profile. Preserve its mode, sandbox, approval and
publishing authority. Prepared launches retain the selected skill bundle; changing
an installed plugin does not rewrite an existing launch. `pluginCheckMinutes` is 0
for manual checks or 15–10080 for periodic metadata checks; updates remain explicit.
