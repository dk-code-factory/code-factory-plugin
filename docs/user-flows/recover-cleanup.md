# Recover retained sessions without blind replay or data loss

## Goal

Recover retained sessions without blind replay or data loss.

## Preconditions

The private owner connection is established. Application data starts clean in the session-state database; native Codex authentication and configuration remain outside application state. GitHub source text is task data, not new authority.

## Main flow

1. After restart retain queues and expire lost RPC identities.

2. Reconcile uncertain delivery against retained native history before explicit retry or discard.

3. Resume the retained native thread without creating a replacement conversation.

4. Keep interruption and human holds until explicit continuation.

5. Clean a worktree only through an explicit action and refuse dirty changes.

## Alternatives and recovery

Read-only inspection does not launch a model turn. Surface validation or provider errors and retain saved work. Human questions, explicit interruptions and uncertain deliveries hold automatic dispatch. Resume against current retained history; never replay an uncertain send or an answered RPC blindly. Explicit cleanup preserves dirty worktrees. If workspace creation was interrupted, retain its planned location and recover only the registered workspace belonging to the expected repository and branch. Preserve dirty content during reuse. Refuse foreign directories, wrong branches and an existing branch without its workspace; expose the failure for explicit action. Explicit cleanup can retire a missing-workspace intent while preserving its existing branch and retained conversation.

## Scenarios

- **S1** — After restart retain queues and expire lost RPC identities.
- **S2** — Reconcile uncertain delivery against retained native history before explicit retry or discard.
- **S3** — Resume the retained native thread without creating a replacement conversation.
- **S4** — Keep interruption and human holds until explicit continuation.
- **S5** — Clean a worktree only through an explicit action and refuse dirty changes.

## Verification scope

The assertion references below establish their fixture scope. Native installed-host, real-provider and assistive-technology acceptance require separate dated observations. The retired pipeline editor, review artifacts, AI question repair and built-in issue-report ledger have no compatibility path.

<!-- flow-references -->
## Terminology and UI references

Glossary: [retained-session](../glossary/README.md#retained-session), [event-batch](../glossary/README.md#event-batch), [workspace](../glossary/README.md#workspace).
UI entities: [W-RUN-CONTROLS](../ui/README.md#W-RUN-CONTROLS), [W-WORK-STATUS](../ui/README.md#W-WORK-STATUS), [W-EVENT-BATCHES](../ui/README.md#W-EVENT-BATCHES).

## Flow diagram

```mermaid
flowchart TD
    N0["Recover retained sessions without blind replay or data loss"]
    N1["After restart retain queues and expire lost RPC identities #40;S1#41;"]
    N2["Reconcile uncertain delivery against retained native history before explicit retry or discard #40;S2#41;"]
    N3["Resume the retained native thread without creating a replacement conversation #40;S3#41;"]
    N4["Keep interruption and human holds until explicit continuation #40;S4#41;"]
    N5["Clean a worktree only through an explicit action and refuse dirty changes #40;S5#41;"]
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

- [internal/factory/service.go](../../internal/factory/service.go) — Durable canonical sessions, event queue, quiet clock and delivery claims. (`func (s *Service) Chat(`)
- [internal/factory/native.go](../../internal/factory/native.go) — Direct native app-server retained thread creation and resumption. (`func (s *Service) attach(`)
- [internal/factory/requests.go](../../internal/factory/requests.go) — Exact session-bound native requests and explicit status, without classifiers. (`func (s *Service) Answer(`)
- [web/src/wrapper.tsx](../../web/src/wrapper.tsx) — Saved inbox, session conversation, human input and confirmed configuration. (`function Factory(`)
- [internal/transport/factory.go](../../internal/transport/factory.go) — Session, configuration, event and retained triage APIs. (`func NewFactory(`)
- [internal/factory/native.go](../../internal/factory/native.go) — Persist planned workspace before Git creation, verify registered ownership for recovery, and retain dirty content/explicit cleanup boundaries. (`func (s *Service) workspace(`)

## Test references

- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestRestartExpiresIdentitiesAndNeverReplaysUncertainBatch(`; S1, S2. Restart expires RPC identities and holds uncertain delivery without blind replay.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestChatFlushBusySteerHoldAndRetainedReuse(`; S3, S4. Human chat flushes queued events, steers unblocked active work and respects holds while retaining thread.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestRetainedWorktreeCleanupRefusesDirtyChanges(`; S5. Explicit cleanup rejects dirty worktrees, expires pending requests, preserves history and keeps the session cleaned after delayed completion/disconnect callbacks; removed worktrees cannot resume.
- [web/tests/wrapper.spec.ts](../../web/tests/wrapper.spec.ts) — `uncertain mutation response is not retried`; S2. A transport failure after sending is visible and the mutation is sent once.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestExplicitRetryRequiresSuccessfulHistoryReconciliation(`; S2. Explicit retry refuses unavailable history or confirmed delivery and requeues only after a successful lookup proves absence; resolution itself sends no work.
- [web/tests/wrapper.spec.ts](../../web/tests/wrapper.spec.ts) — `uncertainty recovery uses durable delivery state rather than error text`; S1, S2. Uncertain batch recovery controls follow persisted deliveryState independently of error wording; pending thread recovery offers retry without batch discard.
- [web/tests/wrapper.spec.ts](../../web/tests/wrapper.spec.ts) — `session controls preserve the selected target and require explicit clicks`; S3, S4, S5. Viewing sends no control; explicit Interrupt, Resume and Clean worktree each target the selected session and render cleaned status.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestConfigurationAppliedMarkerAndAllBindingsCommitAtomically(`; S1. Actual SQLite binding-write abort rolls back settings and applied marker without retiring the current client; restart applies item/configuration/priority bindings together.
- [internal/factory/review7_test.go](../../internal/factory/review7_test.go) — `func TestInterruptedRealWorktreeCreationResumesRegisteredDirtyWorkspace(`; S1, S3, S5. Actual Git before/after-add callbacks verify durable canonical workspace path and chosen Git repository identity before filesystem creation, then successful registration before injected command failure. A fresh service reuses the single dirty registered workspace despite unavailable origin and changed configured checkout, retains content and starts one fake-native turn. Explicit cleanup refuses dirty changes, then removes the clean workspace while retaining its conversation.
- [internal/factory/review7_test.go](../../internal/factory/review7_test.go) — `func TestWorkspaceRecoveryRefusesForeignPathsAndAllowsMissingIntentCleanup(`; S3, S5. Persisted workspace intents for a foreign directory, wrong registered branch and foreign repository with the expected branch are refused for both adoption and explicit cleanup, preserving their paths/content. A branch-only missing-workspace intent is refused for adoption but explicit cleanup retires the session while preserving its existing branch.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.
- **S1** — `gap-flow-recover-cleanup-s1-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Installed-daemon restart with real native retained progress and queued event claims remains unverified. Fixture restart assertions establish no installed-host acceptance. Next verification: In the approved private pilot, restart the installed daemon with retained native work and pending events; verify conversation continuity and claim reconciliation without replay.
- **S2** — `gap-flow-recover-cleanup-s2-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Fake-provider message restoration does not prove current installed real-provider conversation or workspace history preservation. No installed-host or real-provider recovery acceptance is established by this development port. Next verification: Verify message restoration against the installed port version and record retained conversation/workspace history without task or answer replay; fixture success alone is insufficient.

<!-- /flow-references -->
