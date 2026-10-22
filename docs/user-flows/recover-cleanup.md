# Recover retained work and clean up safely

## Goal

The owner can recover interrupted work after a process or connection loss and deliberately remove a clean stopped workspace.

## Preconditions

At least one run has been prepared or launched and its local state is retained.

## Main flow

1. Reopen the application and inspect retained run status and history.
2. After process loss, explicitly resume an interrupted or failed run.
3. Continue in the retained workspace and conversation history.
4. Explicitly clean up a stopped run when its workspace has no uncommitted changes.

## Alternatives and recovery

Closing the view or transport does not stop work. Process loss makes old requests stale. Resume must not replay the original task or old answers. Cleanup first retires any retained session, makes its pending requests stale and marks the run interrupted. If workspace removal is then refused because it is dirty, the workspace remains but continuation requires explicit Resume. Cleanup retains snapshots and history.

## Scenarios

- **S1** Restart recovery preserves prepared candidates and interrupts dead sessions while staling pending requests.
- **S2** Explicit resume reconnects retained work instead of replaying its original task.
- **S3** Dirty cleanup retains the workspace after interrupting the retained session; continuation requires explicit Resume. Clean stopped workspace cleanup retains history.

Native request IDs become stale after process loss. Structured message questions retain durable identity; unanswered native questions and interrupted repairs retain a textual fallback on the original run. Lost approvals never become textual authorization. Answering a fallback restores the retained session without replaying repair attempts or prior answers; explicit Resume remains available for ordinary interrupted work.
<!-- flow-references -->
## Terminology and UI references

Glossary: [agent-run](../glossary/README.md#agent-run), [workspace](../glossary/README.md#workspace), [state-interrupted](../glossary/README.md#state-interrupted), [state-cleaned](../glossary/README.md#state-cleaned), [coverage-gap](../glossary/README.md#coverage-gap), [unverified-behavior](../glossary/README.md#unverified-behavior), [question-recovery](../glossary/README.md#question-recovery).
UI entities: [PAGE-AGENTS](../ui/README.md#PAGE-AGENTS), [P-RUN-LIST](../ui/README.md#P-RUN-LIST), [P-RUN-DETAIL](../ui/README.md#P-RUN-DETAIL), [W-RUN-ROW](../ui/README.md#W-RUN-ROW), [W-RUN-EMPTY](../ui/README.md#W-RUN-EMPTY), [W-RUN-CONTROLS](../ui/README.md#W-RUN-CONTROLS), [C-SELECT-RUN](../ui/README.md#C-SELECT-RUN), [C-RUN-ACTION](../ui/README.md#C-RUN-ACTION).

## Flow diagram

```mermaid
flowchart TD
    N0["Reopen and inspect retained status and history #40;S1#41;"]
    N1["Explicitly resume retained work, or answer a recovered question #40;S2#41;"]
    N2["Continue retained workspace without replaying task or answers"]
    N3["Request cleanup of a stopped run #40;S3#41;"]
    N4["Refuse dirty workspace cleanup"]
    N5(["Dirty workspace retained#59; continuation requires explicit Resume"])
    N6(["Cleaned workspace with retained history"])
    N7["Retire retained session, stale pending requests and mark interrupted"]
    N0 -->|"resume"| N1
    N1 --> N2
    N0 -->|"cleanup stopped work"| N3
    N2 -->|"later stop and cleanup"| N3
    N7 -->|"dirty"| N4
    N4 --> N5
    N7 -->|"clean"| N6
    N3 -->|"before attempting workspace removal"| N7
```

## Implementation references

- [internal/app/runs.go](../../internal/app/runs.go) — Recovery and explicit resume; Cleanup retires any retained session, closes its provider process, stales pending requests and persists interrupted before attempting worktree removal. Dirty removal failure retains the workspace but requires explicit Resume to continue. (`func (m *Manager) Recover(`)
- [internal/store/store.go](../../internal/store/store.go) — Durable local run and journal storage
- [web/src/main.tsx](../../web/src/main.tsx) — Retained runs, resume and cleanup controls
- [internal/domain/types.go](../../internal/domain/types.go) — Shared domain, persistence, transport or dependency contract
- [internal/transport/mcp.go](../../internal/transport/mcp.go) — Shared domain, persistence, transport or dependency contract
- [go.mod](../../go.mod) — Shared domain, persistence, transport or dependency contract
- [go.sum](../../go.sum) — Shared domain, persistence, transport or dependency contract
- [internal/testutil/testutil.go](../../internal/testutil/testutil.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [internal/testutil/fakecodex/main.go](../../internal/testutil/fakecodex/main.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [internal/app/questions.go](../../internal/app/questions.go) — Bounded recovery and durable text/structured question answer claims; preserve authority and redaction. (`func (m *Manager) answerSynthetic(`)

## Test references

- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestRecoveryStalesRequestsAndIdleRuns(`; S1. Stored dead session and attention become interrupted/stale.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestNonblockingApprovalsCrashAndResume(`; S2. Crashed fake process can explicitly resume.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestConcurrentPollingSteeringAndAnswersThenDirtyCleanup(`; S3. Dirty cleanup returns an error and retains the unsaved file; the assertion does not verify session retirement, interrupted status, stale requests or subsequent explicit resume.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `test("dirty cleanup retains the daemon workspace and history until explicit Resume"`; S2, S3. Assembled dirty cleanup shows interrupted state/refusal, retains file/history/draft, disables Send, then resumes the same run/thread/workspace explicitly.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStructuredMessageQuestionSurvivesRestartAndClaimsAnswerOnce(`; S1, S2. Structured question retains identity across restart; concurrent responses accept only once.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestLostNativeQuestionsRemainAnswerableWithoutConvertingApprovals(`; S1. Lost native question becomes text; approvals remain stale.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestRestartDuringRepairRetainsOneFallbackWithoutReplayingAttempts(`; S1. In-flight repair restart produces one durable fallback without replaying attempts.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestRestartMaterializesAlreadyPersistedFallback(`; S1. Crash between persisted fallback stage and attention creation is repaired idempotently.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.
- **S1** — `gap-flow-recover-cleanup-s1-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Prepared-run recovery persistence is implemented but lacks a targeted assertion. Next verification: Persist a prepared run, reconstruct recovery state, and assert that its prepared snapshot survives without launching or creating a workspace.
- **S2** — `gap-flow-recover-cleanup-s2-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Fake resume success does not prove full real-provider history preservation; dated real acceptance is in ../acceptance.md. Next verification: Check existing dated provider acceptance against the current binary and retain explicit evidence of resumed conversation/workspace history without task or answer replay; fixture success alone is insufficient.
- **S3** — `gap-flow-recover-cleanup-s3-01` · Unverified behavior · P2 · owner: user-flow-maintainer. Assembled dirty refusal asserts interrupted status, retained file/history/draft and same-run/thread Resume. Pending-request staleness, session-retirement races and clean cleanup/history retention still need dedicated assertions. Next verification: Exercise pending requests/concurrent clients during cleanup, then separately assert clean removal and retained snapshots/history.

<!-- /flow-references -->
