# Acceptance and verification

This public repository contains source, deterministic fixtures and specification
contracts. Personal pilot logs, hostnames, app registrations, conversation links,
credentials and installation state are intentionally not published.

GitHub Actions runs the checks in [CI and releases](ci.md). A green workflow proves
that those automated checks passed for the displayed commit. It does not establish
real ChatGPT rendering, host event callbacks, accessibility conformance, public
reachability or installed-user-session recovery.

Each installation must maintain its own dated, nonsecret operator evidence tied to
its actual binary version. Required live checks include the installed complete
plugin, inbox discovery/revision/priority behavior, explicit read-only Plan launch,
a real provider question answered in the widget, follow-up, stale-answer and
restart recovery, a connected one-hour soak, CSP behavior and a coordinated desktop
logout/login. Verify real event subscription, callback challenge, delivery,
filtering, refresh and unsubscribe separately. Retain manual accessibility gaps.

Use `code-factory record-evidence` only for checks actually observed on that
installation. Keep private evidence outside this checkout. Historical source
adoption is not proof of runtime readiness. The structured registries retain
explicit fixture coverage gaps and next verification steps.

## Recovery and response feedback source checks · 2026-10-22

The recovery port is compared with published baseline
`9cd2c99bb972009ed2f5b5d35f0f2fa141077111`. It retains the published Agents pane and
excludes the pilot PR-tracking helper and work-tracking changes. There is no SQL
migration; question-origin metadata and recovery records are additive.

The complete Go race suite and 21 Playwright tests passed locally. Recovery
fixtures cover first/second native repair success, two-attempt fallback, ordinary
completion, malformed forms, restart and legacy native requests, duplicate answers,
takeover, encrypted answer redaction, helper isolation/model selection and approval
boundaries. A persisted fallback missing its attention record is materialized
idempotently after restart. MCP fixtures check scoped run recovery responses,
snapshot recovery records and app-only dismissal.

Browser fixtures cover decorative clock/arrow icons and readable labels, response
confirmation expiry despite polling, owning-run isolation, no confirmation/replay
on failed or uncertain answers, separate refresh failure after successful delivery,
literal UI_ANSWER_OK transcript text, fallback answers/inspection/dismissal, retained
drafts and takeover. Both themes at 390px have keyboard checks and scoped automated
WCAG A/AA scans; core-state scans also run in both themes. The takeover button's
dark-theme contrast was corrected before these checks passed.

Fresh reviewed reconciliation for all affected subjects, `make specs-check` and
`make ci FLOW_BASE=9cd2c99bb972009ed2f5b5d35f0f2fa141077111` passed against that
published baseline, including packaging, installer recovery and smoke checks.
These source results establish no deployed binary version:
this task does not install, restart services or perform real-provider/host checks.
All installed-plugin, real callback, soak, coordinated login and manual
accessibility acceptance gaps remain open and version-bound.
