# Configure through a retained conversation and confirmed diffs

## Goal

Configure through a retained conversation and confirmed diffs.

## Preconditions

The private owner connection is established. Application data starts clean in the session-state database; native Codex authentication and configuration remain outside application state. GitHub source text is task data, not new authority.

## Main flow

1. Use configuration chat to inspect effective settings and native layers.

2. Choose actual native profiles and agent definitions with arbitrary installed skills.

3. Inspect a concrete application or native configuration proposal.

4. Explicitly confirm or reject against the inspected versions.

5. Apply confirmed changes after active turns finish while preserving unrelated settings.

## Alternatives and recovery

Read-only inspection does not launch a model turn. Surface validation or provider errors and retain saved work. Human questions, explicit interruptions and uncertain deliveries hold automatic dispatch. Resume against current retained history; never replay an uncertain send or an answered RPC blindly. Explicit cleanup preserves dirty worktrees.

## Scenarios

- **S1** — Use configuration chat to inspect effective settings and native layers.
- **S2** — Choose actual native profiles and agent definitions with arbitrary installed skills.
- **S3** — Inspect a concrete application or native configuration proposal.
- **S4** — Explicitly confirm or reject against the inspected versions.
- **S5** — Apply confirmed changes after active turns finish while preserving unrelated settings.

## Verification scope

The assertion references below establish their fixture scope. Native installed-host, real-provider and assistive-technology acceptance require separate dated observations. The retired pipeline editor, review artifacts, AI question repair and built-in issue-report ledger have no compatibility path.

<!-- flow-references -->
## Terminology and UI references

Glossary: [configuration-proposal](../glossary/README.md#configuration-proposal), [profile](../glossary/README.md#profile), [skill](../glossary/README.md#skill).
UI entities: [PAGE-SETTINGS](../ui/README.md#PAGE-SETTINGS), [W-CONFIGURATION-CHAT](../ui/README.md#W-CONFIGURATION-CHAT), [W-CONFIGURATION-PROPOSAL](../ui/README.md#W-CONFIGURATION-PROPOSAL), [W-EFFECTIVE-SETTINGS](../ui/README.md#W-EFFECTIVE-SETTINGS).

## Flow diagram

```mermaid
flowchart TD
    N0["Configure through a retained conversation and confirmed diffs"]
    N1["Use configuration chat to inspect effective settings and native layers #40;S1#41;"]
    N2["Choose actual native profiles and agent definitions with arbitrary installed skills #40;S2#41;"]
    N3["Inspect a concrete application or native configuration proposal #40;S3#41;"]
    N4["Explicitly confirm or reject against the inspected versions #40;S4#41;"]
    N5["Apply confirmed changes after active turns finish while preserving unrelated settings #40;S5#41;"]
    N6(["Hold saved work and show the failure or human request"])
    N7(["Retain conversation and saved evidence"])
    N0 -->|"authorized action"| N1
    N1 -->|"validated or explicitly resumed"| N2
    N1 -->|"missing authority, stale identity or unavailable dependency"| N6
    N2 -->|"validated or explicitly resumed"| N3
    N2 -->|"missing authority, stale identity or unavailable dependency"| N6
    N3 -->|"validated or explicitly resumed"| N4
    N3 -->|"missing authority, stale identity or unavailable dependency"| N6
    N4 -->|"validated or explicitly resumed"| N5
    N4 -->|"missing authority, stale identity or unavailable dependency"| N6
    N5 -->|"validated or explicitly resumed"| N7
    N5 -->|"missing authority, stale identity or unavailable dependency"| N6
```

## Implementation references

- [internal/factory/configuration.go](../../internal/factory/configuration.go) — Concrete application/native proposals, explicit confirmation and safe boundaries. (`func (s *Service) Confirm(`)
- [internal/factory/native.go](../../internal/factory/native.go) — Direct native app-server retained thread creation and resumption. (`func (s *Service) attach(`)
- [web/src/wrapper.tsx](../../web/src/wrapper.tsx) — Saved inbox, session conversation, human input and confirmed configuration. (`function Factory(`)
- [internal/transport/factory.go](../../internal/transport/factory.go) — Session, configuration, event and retained triage APIs. (`func NewFactory(`)
- [plugin/skills/code-factory-status/SKILL.md](../../plugin/skills/code-factory-status/SKILL.md) — Current configure-work implementation or fixture/build dependency.
- [plugin/skills/code-factory-report-issue/SKILL.md](../../plugin/skills/code-factory-report-issue/SKILL.md) — Current configure-work implementation or fixture/build dependency.

## Test references

- [web/tests/wrapper.spec.ts](../../web/tests/wrapper.spec.ts) — `configuration chat and readable effective settings replace forms`; S1, S3. Configuration message addresses persistent chat; effective settings can be read.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestNativeAgentDefinitionUsesNativeTOMLAndProfile(`; S2. Native profile and agent config_file resolve without a separate agent format; effective native developer instructions remain in the actual thread request before wrapper guidance.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestNativeConfigurationLayerVersionsPreserveUnrelatedSettings(`; S1, S4, S5. Confirmed native edit sends the exact filePath, expectedVersion and one model-key replace edit with reloadUserConfig false; changed native layers reject stale proposals. Actual native editor/file execution remains unverified.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestConfigurationConfirmationStalenessAndSafeBoundary(`; S3, S4, S5. Explicit confirmation rejects stale app proposals and waits until active turns finish.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestMissingSkillsAndProfilesAreVisible(`; S2. Missing arbitrary installed skill or native profile fails visibly before a claimed send.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestDynamicToolSchemasUseArrayRequired(`; S3. Actual dynamic-tool schemas compile and every required member is an array, including empty proposal schemas.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestNativeConfigurationStartupFailureRemainsConfirmedAcrossRestart(`; S5. A confirmed native proposal survives known client startup failure before any write and applies once after restart; ambiguous write failure remains uncertain and is not replayed.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestConfigurationAppliedMarkerAndAllBindingsCommitAtomically(`; S5. Actual SQLite binding-write abort rolls back settings and applied marker without retiring the current client; restart applies item/configuration/priority bindings together.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestMultipleNativeProposalsNeverRestoreRetiredEpoch(`; S5. Two independent deferred native proposals both apply without restoring a retired epoch; delayed old disconnect leaves execution idle, unheld and detached.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestSchedulerSkipsHeldQueueDecodeAndBusyConfiguration(`; S5. Thirty intentionally undecodable histories cover 15 held, 14 due but paused, and one running session with runtime rows within 512 bytes; three ticks preserve deferred active configuration and start no native work.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.

<!-- /flow-references -->
