# Reporting Code Factory defects

Invoke `code-factory-report-issue` when observed behavior appears defective. Chat
supplies the host observation, expected/actual behavior, observation time and
reproduction steps or an explanation that reproduction is unknown. A stable
idempotency key makes lost responses safe to retry. The returned report ID is
retained before investigation starts. Investigation and issue creation are
automatic; fixing code is a separate task.

The six authenticated MCP tools are `report_issue`, `add_issue_evidence`,
`get_issue_report`, `list_issue_reports`, `refresh_issue_reports` and
`resume_issue_report`. Chat and the **Reported issues** page read the same SQLite
ledger. Lists support cursor pagination and processing, component, version and
text filters. Journal changes refresh visible lists and details.

Evidence lives under the private application data directory in
`issue-reports/REPORT_ID/`, never in a source checkout. The manifest identifies
captured versions, relevant runs, launch snapshots, attention, item revisions,
journal ranges, synchronization summaries, hashes, timestamps and omissions.
Only referenced work is copied; credentials and authentication are excluded.
Unscoped service logs are omitted to avoid unrelated private work; the investigator
can inspect relevant local logs around the observation. Only sanitized relevant
excerpts belong in an issue. Missing host observations can be appended later.

Automatic investigation requires an installed `agent-workflow` on the service's
PATH, authenticated native Codex, GitHub CLI authentication, Python 3 and an
installed `dk-workflows` Codex plugin. An explicit
`CODE_FACTORY_DK_WORKFLOWS_ROOT` can identify its installed package root when
standard cache discovery is unavailable. Code Factory never installs or upgrades
these dependencies. An existing automatic dk-workflows update policy must be explicitly changed to notify/off before reporting can invoke its lifecycle. Fixture mode disables production investigation.

Each investigation retains its own private controller workspace and lifecycle
receipt. The packaged workflow uses native Codex authentication and the retained
issue-drafting skill in draft mode, honoring its lifecycle token and nested
invocations. One report investigates at a time, with a thirty-minute engine
deadline. Missing prerequisites remain blocked. Controller admission and resume
requests retain their IDs before network/process calls. Interrupted effects
require an inspection and reconciliation note. Uncertain lifecycle entry also
requires inspecting and completing its retained invocation before retry.

The default repository is `dk-code-factory/code-factory-plugin`. Supported
component provenance can route `agent-workflow` or `dk-workflows` bugs to their
own organization repositories. The adapter validates Issues availability, source
commit/path and supported labels. Uncertain ownership blocks filing. Clear
existing duplicates are associated with the same issue.

After the workflow completes, the deterministic `code-factory report-publish
--data-dir PRIVATE_DIR --id REPORT_ID` command reads its retained draft. It
records publication intent and an atomic claim before creating an issue. An
opaque report marker reconciles a lost creation response. An empty search is not
proof that creation failed: uncertain creation is never repeated automatically.
Use Resume to reconcile its identity after connectivity recovers. Reports,
workflow handles and status history survive daemon and controller restarts.

Processing, outcome, GitHub closure and delivery are independent. A closed issue
alone is not a fix; a merged closing PR alone is not a published package. Release
tracking requires published metadata, uploaded assets, complete checksums, a
matching tag commit and verified fix ancestry. Code Factory provenance supplies
explicit fixed-issue/component associations. Native `agent-workflow` releases use
an independently verified closing PR or commit, the complete native package and
checksum inventory, and matching dependency/conformance build declarations.
Matching source fingerprints corroborate published declarations; reporting does
not rebuild packages or verify their signatures. Source-only dependency releases
without checked package evidence remain **Unknown**. Stable releases,
prereleases and observed installed versions stay distinct. Dependency releases
belong to their dependency; a Code Factory release must explicitly establish any
relationship. Unavailable evidence displays **Unknown**. GitHub state refreshes
at startup and every five minutes, releases every fifteen minutes; manual refresh
starts no agent. Failed or rate-limited reads retain successful evidence with
freshness, error and retry information.

`release/fixed-issues.json` lists explicit issue/component/fix-commit associations.
For Code Factory releases, each full fix commit must be an ancestor of the checked
source commit and explicitly reference the issue with a fixing/closing statement.
CI derives daemon and portable/Codex plugin versions from the exact package bytes
and includes those versions and checked fixed issues in `CI-MANIFEST.json`.
Delivery still promotes the exact CI artifacts without rebuilding or replacing
an existing release.

Ordinary verification builds a pinned test-only workflow controller and uses fake
Codex, lifecycle and GitHub boundaries. It never starts a real model or needs
model credentials. Installed-host and authenticated filing acceptance require the
separate approved private repository and browser session described in the browser
runbook; fixture success does not establish that acceptance.
