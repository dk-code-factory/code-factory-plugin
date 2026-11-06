# User-flow contracts

These maintained contracts describe the native session wrapper and retained connection, triage and installation infrastructure. Stable retained flow IDs and the pinned historical adoption list remain unchanged. Retired pipeline, report-ledger and question-repair behavior is removed; it has no migration or compatibility path.

- [Install and connect a private workspace](install-connect.md)
- [Configure through a retained conversation and confirmed diffs](configure-work.md)
- [Synchronize saved GitHub observations without historical execution](sync-inbox.md)
- [Browse issues and PRs without launching work](triage-inbox.md)
- [Resolve manual, label and native-agent priority](resolve-priority.md)
- [Preserve issue and PR relationships and source evidence](relate-items.md)
- [Create one canonical retained conversation for authorized work](launch-agent.md)
- [Chat, steer and explicitly control retained work](interact-agent.md)
- [Answer exact native requests and explicit blocked status](answer-attention.md)
- [Recover retained sessions without blind replay or data loss](recover-cleanup.md)
- [Inspect saved session status and retained transcript](view-live-work.md)
- [Subscribe to host notifications and manage delivery](receive-notifications.md)
- [Preserve private credentials and redact native session answers](protect-secrets.md)
- [Inspect health, upgrade, roll back and uninstall](operate-installation.md)
- [Reconcile flow contracts with implementation and tests](maintain-flow-contracts.md)
- [Route durable GitHub changes into ordered quiet-period batches](route-events.md)

The [layered specification guide](../specs/README.md) owns reconciliation, fixture suites and explicit verification gaps. Real-host observations remain separate from fixture evidence.
