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
