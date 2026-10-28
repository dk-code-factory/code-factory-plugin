---
name: code-factory-report-issue
description: Report suspected Code Factory defects from observed behavior and track investigation, GitHub issues, fixes and releases through the private report ledger.
---

Use `report_issue` when observed Code Factory behavior appears defective. Reporting
queues automatic local investigation and GitHub issue filing; it does not authorize
fixing code. Default filing is `dk-code-factory/code-factory-plugin`; investigation
can establish dependency ownership within `dk-code-factory`.

Supply a stable `idempotencyKey` for this observation, `observedAt` in RFC3339,
`surface`, `expected`, `actual`, and `steps` or a `reproductionUnknown` explanation.
Include impact, frequency, exact errors, exact tool calls/results, relevant `runIds`
and `itemIds`, event range and observed `pluginVersion` when available. Keep
observations separate from `suspectedCause`. Do not invent causes or reproduction.
Keep the same key and payload when retrying a lost submission response; conflicting
reuse fails. Keep returned report IDs for follow-up.

The backend captures local versions, referenced runs and immutable launch settings,
attention, journal/transcript ranges, revisions, synchronization and diagnostic
summaries. Do not ask the user to copy locally available data. Supply host-only
observations such as what Work displayed or the exact tool response you received.
Never include credentials, authentication material or unrelated private work.
Evidence stays private outside Git; only sanitized relevant excerpts are published.
Missing evidence is explicit and does not itself disqualify a suspected bug.

Use `add_issue_evidence` with the report `id`, a stable `idempotencyKey` and `text`
for additional observations, including requested information. This resumes reports
waiting for information. Use `get_issue_report` for progress and links, or
`list_issue_reports` for the shared searchable ledger and status/component/version
filters. The embedded **Reported issues** view reads this same ledger. Its auditor traces
observations to code and can retain an invalid outcome with a source-backed
explanation, or an already-resolved outcome with a verified completed issue and
fix present in inspected source. Neither outcome creates a new issue. Missing
evidence or failed reproduction alone does not invalidate a report. Resolved
source does not imply a released or installed fix; retain those distinctions.

Use `refresh_issue_reports` to refresh GitHub/release evidence without an agent.
Use `resume_issue_report` after prerequisites are restored. Interrupted workflows
require inspection and reconciliation of retained attempts before `reconciled: true`
and a substantive `note` authorize retry. Uncertain GitHub creation is reconciled
by its report marker; never create or retry an issue directly from chat.

Report processing separately from GitHub state and delivery. Closed does not imply
fixed; merged does not imply released. Distinguish verified component versions,
stable/prerelease availability and observed installed versions. Say **Unknown**
when evidence is unavailable. Do not install or upgrade dependencies automatically.
