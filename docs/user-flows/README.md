# User-flow contracts

These contracts describe the behavior found in the source at initial commit
`53ff8be`, plus the contributor flow added with this tooling. Each flow separates
implementation-agnostic behavior from implementation references, test references
and known verification gaps. This is a specification baseline, not a new claim that
every scenario has been tested. [Acceptance](../acceptance.md) remains authoritative
for dated observations and outstanding real-host checks.

| Flow | Contract |
| --- | --- |
| Owner setup | [Install and connect](install-connect.md) |
| Work settings | [Repositories, policies, profiles and skills](configure-work.md) |
| Provider updates | [Synchronize inbox](sync-inbox.md) |
| Daily triage | [Browse, read, acknowledge and record actions](triage-inbox.md) |
| Prioritization | [Resolve and override priority](resolve-priority.md) |
| Related context | [Connect work and inspect tickets](relate-items.md) |
| Start work | [Review and launch](launch-agent.md) |
| Continue work | [Follow up, steer and interrupt](interact-agent.md) |
| Human input | [Questions, approvals and forms](answer-attention.md) |
| Retained work | [Recover and clean up](recover-cleanup.md) |
| Embedded view | [Live status, transcript and changes](view-live-work.md) |
| Host alerts | [Subscriptions and delivery](receive-notifications.md) |
| Private input | [Credentials and secret answers](protect-secrets.md) |
| Operator lifecycle | [Health, upgrades, rollback and uninstall](operate-installation.md) |
| Contributor workflow | [Maintain flow contracts](maintain-flow-contracts.md) |

Use the repository skill at
[.agents/skills/reconcile-user-flows/SKILL.md](../../.agents/skills/reconcile-user-flows/SKILL.md).
It is a development skill; it is not part of the installed end-user plugin.

## Maintenance and local CI

The catalog now references canonical glossary terms, stable UI entity IDs and
structured nodes/edges for each flow diagram. Diagrams supplement the written
behavior and include alternatives, failure and recovery. The UI-to-behavior map
lives here in the catalog; UI documents contain no reverse links.

The [unified specification guide](../specs/README.md) owns command semantics,
review format, reconciliation policy and provider-neutral CI instructions.
Use `python3 scripts/specs.py impact --base HEAD --json` to identify affected
terms, entities and scenarios, then maintain the appropriate layer. Historical
`python3 scripts/user_flows.py` commands and `make flows-*` targets are compatible
aliases for the unified gate. `make specs-ci` runs the fast deterministic checks;
`make ci FLOW_BASE=main` runs the full local branch gate.

Behavior-bearing changes need related implementation and meaningful tests.
Refactors, terminology corrections and structural-reference updates may retain
accurate prose with explicit review and fresh verification. Generated diagrams
and reference blocks do not count as independent behavioral changes. New and
retired behavior still need code and tests. The original product-flow adoption
list and source base stay pinned to `53ff8be`; registry migration does not broaden
that exception.

Read actual assertions before claiming scenario coverage. References and passing
fixtures do not close partial coverage or real-host gaps. Read the
[reconciliation skill](../../.agents/skills/reconcile-user-flows/SKILL.md) for
layer ownership, structured graphs, verification and handoffs.
