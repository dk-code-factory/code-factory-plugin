# Recover retained work and clean up safely

## Goal

The owner can recover interrupted work after a process or connection loss and deliberately remove a clean stopped workspace.

## Preconditions

At least one run has been prepared or launched and its local state is retained.

## Main flow

1. Reopen the application and inspect retained run status and history.
2. After process loss, send a message to continue an interrupted or failed run.
3. Continue in the retained workspace and conversation history.
4. Open the secondary agent details, review that cleanup retires the session and removes a clean workspace, then explicitly clean up a stopped run with no uncommitted changes.

## Alternatives and recovery

Closing the view or transport does not stop work. Process loss makes live RPC IDs stale. Structured-message questions remain retained; lost native questions become textual continuations, while approvals must be requested again through their native authorization path. Sending to inactive work restores its retained session and uses the submitted message as the only new turn. It must not replay the original task, a generic resume prompt or old answers. Failed delivery retains the draft. Cleanup first retires any retained session, makes its pending requests stale and marks the run interrupted. If workspace removal is then refused because it is dirty, the workspace remains but continuation happens by sending a message. Cleanup retains snapshots and history.

## Scenarios

- **S1** Restart recovery preserves prepared candidates and interrupts dead sessions while staling pending requests.
- **S2** Sending a message restores retained work and starts exactly that message instead of replaying its original task.
- **S3** Dirty cleanup retains the workspace after interrupting the retained session; continuation happens by sending a message. Clean stopped workspace cleanup retains history.

- **S4** Render native and structured-message questions; repair a stopped textual ask at most twice, then offer free-text continuation and the same session console. Preserve answer claims and redaction across recovery.

Plugin maintenance also survives view closure. Startup marks queued/running operations uncertain without replay; a failed/null inventory response retains the previous observations. Refreshing actual provider state precedes any user-chosen retry. Existing run recovery and cleanup do not remove plugin installation history.

- **S5** Private scratch and refreshed review indexes remain scoped to the retained run across recovery. Successful explicit cleanup removes that scratch along with its clean workspace; dirty cleanup preserves both. The owner’s original private settings and retained conversation history remain intact.

<!-- flow-references -->
## Terminology and UI references

Glossary: [agent-run](../glossary/README.md#agent-run), [workspace](../glossary/README.md#workspace), [state-interrupted](../glossary/README.md#state-interrupted), [state-cleaned](../glossary/README.md#state-cleaned), [coverage-gap](../glossary/README.md#coverage-gap), [unverified-behavior](../glossary/README.md#unverified-behavior), [question-recovery](../glossary/README.md#question-recovery), [session-console](../glossary/README.md#session-console).
UI entities: [PAGE-AGENTS](../ui/README.md#PAGE-AGENTS), [P-RUN-LIST](../ui/README.md#P-RUN-LIST), [P-RUN-DETAIL](../ui/README.md#P-RUN-DETAIL), [W-RUN-ROW](../ui/README.md#W-RUN-ROW), [W-RUN-EMPTY](../ui/README.md#W-RUN-EMPTY), [W-RUN-CONTROLS](../ui/README.md#W-RUN-CONTROLS), [C-SELECT-RUN](../ui/README.md#C-SELECT-RUN), [C-RUN-ACTION](../ui/README.md#C-RUN-ACTION), [PAGE-AGENT](../ui/README.md#PAGE-AGENT), [W-WORK-STATUS](../ui/README.md#W-WORK-STATUS), [W-QUESTION-RECOVERY](../ui/README.md#W-QUESTION-RECOVERY), [W-SESSION-CONSOLE](../ui/README.md#W-SESSION-CONSOLE).

## Flow diagram

```mermaid
flowchart TD
    N0["Reopen and inspect retained status and history #40;S1#41;"]
    N1["Send a message to restore interrupted or failed work #40;S2#41;"]
    N2["Continue retained workspace without replaying task or answers"]
    N3["Request cleanup of a stopped run #40;S3#41;"]
    N4["Refuse dirty workspace cleanup"]
    N5(["Dirty workspace retained#59; send a message to continue"])
    N6(["Cleaned workspace with retained history"])
    N7["Retire retained session, stale pending requests and mark interrupted"]
    N8["Render native and structured-message questions#59; repair a stopped textual ask at most twice, then offer free-text continuation and the same session console. Preserve answer claims and redaction across recovery. #40;S4#41;"]
    N9["Keep source access bounded, private scratch writable and network destinations scoped #40;S5#41;"]
    N10(["Sandbox initialization failed#59; retained work and authority preserved"])
    N0 -->|"send message"| N1
    N1 --> N2
    N0 -->|"cleanup stopped work"| N3
    N2 -->|"later stop and cleanup"| N3
    N7 -->|"dirty"| N4
    N4 --> N5
    N7 -->|"clean"| N6
    N3 -->|"before attempting workspace removal"| N7
    N0 -->|"additional workbench path"| N8
    N8 -->|"resolved or explicit recovery"| N5
    N0 -->|"authorized bounded task start or continuation"| N9
    N9 -->|"valid policy#59; retain scoped work"| N5
    N9 -->|"invalid policy#59; fail before provider launch"| N10
```

## Implementation references

- [internal/app/runs.go](../../internal/app/runs.go) — Recovery and retained-session continuation by message. Cleanup retires the provider, stales pending requests and persists interrupted before worktree removal. Dirty refusal preserves files/history; sending a message restores that retained thread without a separate Resume action. (`func (m *Manager) Recover(`)
- [internal/store/store.go](../../internal/store/store.go) — Durable local run and journal storage
- [web/src/main.tsx](../../web/src/main.tsx) — Retained runs, resume and cleanup controls
- [internal/domain/types.go](../../internal/domain/types.go) — Shared domain, persistence, transport or dependency contract
- [internal/transport/mcp.go](../../internal/transport/mcp.go) — Shared domain, persistence, transport or dependency contract
- [go.mod](../../go.mod) — Shared domain, persistence, transport or dependency contract
- [go.sum](../../go.sum) — Shared domain, persistence, transport or dependency contract
- [internal/testutil/testutil.go](../../internal/testutil/testutil.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [internal/testutil/fakecodex/main.go](../../internal/testutil/fakecodex/main.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [internal/app/questions.go](../../internal/app/questions.go) — Durable native/message/text question routing, bounded form repair, redaction and retained continuation. (`func (m *Manager) answerSynthetic(`)
- [internal/app/plugins.go](../../internal/app/plugins.go) — Durable pinned-CLI plugin lifecycle and exclusive runtime/maintenance boundary. (`func (m *Manager) PluginAction(`)
- [internal/domain/admin.go](../../internal/domain/admin.go) — Typed plugin operation and inventory persistence. (`type PluginOperation struct`)
- [internal/app/sandbox.go](../../internal/app/sandbox.go) — Invocation-local native filesystem and destination policy; isolated writable scratch and private skill indexes without global configuration changes.
- [internal/codex/sandbox.go](../../internal/codex/sandbox.go) — Invocation-local native filesystem and destination policy; isolated writable scratch and private skill indexes without global configuration changes.

## Test references

- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestRecoveryStalesRequestsAndIdleRuns(`; S1. Stored dead session and attention become interrupted/stale.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestNonblockingApprovalsCrashAndResume(`; S2. Crashed fake process can explicitly resume.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestConcurrentPollingSteeringAndAnswersThenDirtyCleanup(`; S3. Dirty cleanup returns an error and retains the unsaved file; the assertion does not verify session retirement, interrupted status, stale requests or subsequent explicit resume.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `test("sending a message restores the dirty retained daemon session without a Resume action"`; S2, S3. Assembled cleanup refuses dirty removal, preserves history/workspace/thread and restores that session by sending the draft, without separate Resume.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestMessageRestoresRetainedSessionWithOneSubmittedTurn(`; S2. Cold daemon recovery preserves run/thread/worktree/authority and delivers exactly the submitted message; eight simultaneous sends accept one wake only.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestWakeMessageFailureKeepsRetainedRecordAndRejectsCleanedWork(`; S2, S3. Missing retained worktree returns wake failure without losing the retained record; prepared and cleaned work cannot accept a message.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `inactive work status and sending a message restore the retained session`; S2. Both themes verify work-item activity and message-triggered continuation, no Resume control, one submit and retained failure draft.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStoppedQuestionRecoveryNativeAndFallback(`; S4. Same-session bounded question repair, at-most-once answers and original-mode restoration.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStructuredMessageQuestionSurvivesRestartAndClaimsAnswerOnce(`; S4. Async questions survive restart and concurrent answers have one claimant.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `text fallback opens the same session console`; S4. Fallback routes the exact answer to its request and console mode retains the reply draft without a new launch.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestLostNativeQuestionsRemainAnswerableWithoutConvertingApprovals(`; S4. Lost question IDs are stale but retained textual continuation remains; lost approvals never become textual authorization.
- [internal/app/plugins_test.go](../../internal/app/plugins_test.go) — `func TestPluginRestartAndMalformedInventoryRetainObservedState(`; S1. Startup marks in-flight plugin operations uncertain; malformed inventory retains prior observations without replay.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestTaskSandboxRetainsIsolationAcrossSessionWake(`; S5. Start and wake retain the same bounded process profile without a legacy policy override; explicit clean cleanup removes scratch.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestSandboxSkillDataIsPrivateBoundedAndRetained(`; S5. Refreshing private skill indexes cannot alter the owner source; resume keeps run caches and rejects symlink sources.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestInvalidTaskSandboxFailsWithoutProviderFallback(`; S5. Invalid sandbox fails before app-server launch without an unrestricted fallback.
- [internal/codex/sandbox_test.go](../../internal/codex/sandbox_test.go) — `func TestTaskSandboxPolicyAndInvalidRoots(`; S5. Independent workspace/scratch filesystem and network permissions reject invalid or global roots; proxy allowlist excludes arbitrary hosts.

## Verification and gaps

Local suites: `go`.
Fixture success does not establish real-host acceptance.
- **S1** — `gap-flow-recover-cleanup-s1-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Prepared-run recovery persistence is implemented but lacks a targeted assertion. Next verification: Persist a prepared run, reconstruct recovery state, and assert that its prepared snapshot survives without launching or creating a workspace.
- **S2** — `gap-flow-recover-cleanup-s2-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Fake resume success does not prove full real-provider history preservation; dated real acceptance is in ../acceptance.md. Next verification: Check existing dated provider acceptance against the current binary and retain explicit evidence of resumed conversation/workspace history without task or answer replay; fixture success alone is insufficient.
- **S3** — `gap-flow-recover-cleanup-s3-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Assembled dirty refusal asserts interrupted status, retained file/history/draft and same-run/thread Resume. Pending-request staleness, session-retirement races and clean cleanup/history retention still need dedicated assertions. Next verification: Exercise pending requests/concurrent clients during cleanup, then separately assert clean removal and retained snapshots/history.

<!-- /flow-references -->
