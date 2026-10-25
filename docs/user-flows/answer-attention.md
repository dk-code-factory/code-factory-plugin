# Answer agent questions and approval requests

## Goal

The owner sees requests for attention and sends a valid response to the originating session once.

## Preconditions

An agent has requested a question, approval, permission grant or supported form response.

## Main flow

1. Open Messages to see unanswered native, structured-message and recovered textual questions and approvals, with owning item, request type and question. Select a request to open its owning conversation and attention dialog.
2. Choose an answer or complete the supported form.
3. Submit the answer and return to the owning run’s conversation.

An accepted response is saved privately together with the one-time answer claim
before delivery. Its exact content remains encrypted even if delivery becomes
uncertain. The request leaves actionable attention and stays absent after reopening,
disconnect or restart; an answered repair cannot become a new fallback question.
Recovery also retires an older duplicate fallback for that answered episode,
while preserving unrelated unanswered requests. Invalid or rejected duplicate
responses cannot replace the saved answer.

In chat, ask which agents need an answer. The connected plugin lists the same
answerable requests with their item, profile, exact questions and options. It
asks for your response, sends it to the matching request once, and checks the
fresh queue and owning run before reporting continuation. An empty queue means
no answerable requests now, not that every task finished. Reads and pagination
never start work or answer for you; secret fields stay in the embedded form.
On-demand queue access does not imply background host notifications.

## Alternatives and recovery

Keyboard focus stays within the dialog through updates. Escape defers the answer and restores its connected opener; it does not submit a response.

Messages counts valid unanswered questions and approvals, including nonblocking questions and retained message/text questions; an empty queue remains available with a concise status. Answering one request removes only that request from the waiting list. Retained conversations and prepared launches are available only in their owning item’s Work history. Answered, stale and orphaned requests are excluded. Live RPC requests require their original connected process; retained message/text questions can restore their session. Approval command, reason and requested access are readable fields; original data is available under Technical details. Allow once, Allow for this session, Deny and Cancel preserve the exact original response values. Answers are local to each request; secret answers never enter ordinary item/run draft storage.

Nonblocking requests leave work active and remain visible in both Messages and the owning conversation. Invalid answers and grants beyond requested permissions are rejected. Duplicate, resolved or stale requests cannot be answered again. Delivery failure after a response is claimed is not automatically replayed. Unsupported host verification stays explicit.

A delivered conversational message, completed turn or startup schedules background reconciliation of retained message/text questions. Only a specific owner decision followed by an explicit agent acknowledgement in the same run can retire that question. Partial decisions preserve the other original question IDs and options; resolved, cancelled or superseded questions leave actionable attention with evidence references retained. A read-only helper checks bounded redacted conversation evidence and cannot answer questions or grant permissions. Missing or ambiguous evidence, helper failure, a changed conversation or another run’s completed review preserves the outstanding asks. Saved-state reads remain immediate and never invoke this helper. Native requests and approvals still require their original protocol response.

Daemon startup preserves unanswered questions without continuing past them. Lost native questions remain answerable through a retained textual continuation; lost approvals expire and must be requested again through the native approval path. No old answer or approval is replayed during automatic session recovery.

Closing or merging the source item terminates its sessions and retires their pending questions and approvals. Those asks cannot be answered or recreated through recovery; history remains available.

When an agent stops with a textual question, the application first checks whether input is actually needed. A helper can request a supported form in the same session at most twice. A successful form is displayed directly; failed or unavailable repair exposes the original question with a free-text answer and an optional session console. Takeover cancels further repair. An uncertain textual fallback also offers No reply needed, clearing that local ask without advancing the session or authorizing anything. An answer continues the same work with its original mode and authority; a failed or uncertain send is never replayed automatically.

## Scenarios

- **S1** A blocking question is visible and answered exactly once, including concurrent submissions.
- **S2** Approval choices and form content are validated without broadening permissions.
- **S3** Nonblocking requests leave the run active; stale requests reject responses after recovery.

- **S5** The chat queue is discoverable, read only, oldest first and paginated without skipping requests when earlier asks resolve. It preserves owning context and exact options, excludes stale/answered/orphaned requests, and verifies that an accepted answer resumes the same agent without removing another agent’s ask.

- **S4** Render native and structured-message questions; repair a stopped textual ask at most twice, then offer free-text continuation and the same session console. Preserve answer claims and redaction across recovery.

Automatic recovery exchanges appear as technical activity under Details or in the session console. Your responses keep their ordinary conversation role.

During plugin maintenance an answer is refused with explicit feedback; the caller retains its draft. Maintenance cannot begin while live waiting/running agent work owns the provider. No answer is replayed automatically.

Successful conversational answers return local delivery state, the originating run/turn/thread identities and remaining pending attention, so queue discovery and answer verification normally take two calls. Accepted delivery does not establish task completion. Uncertain delivery still requires reading current state without replay.

<!-- flow-references -->
## Terminology and UI references

Glossary: [attention-request](../glossary/README.md#attention-request), [agent-run](../glossary/README.md#agent-run), [state-blocking](../glossary/README.md#state-blocking), [state-continuing](../glossary/README.md#state-continuing), [coverage-gap](../glossary/README.md#coverage-gap), [unverified-behavior](../glossary/README.md#unverified-behavior), [keyboard-focus](../glossary/README.md#keyboard-focus), [modal-dialog](../glossary/README.md#modal-dialog), [question-recovery](../glossary/README.md#question-recovery), [session-console](../glossary/README.md#session-console), [state-terminated](../glossary/README.md#state-terminated).
UI entities: [D-ATTENTION](../ui/README.md#D-ATTENTION), [P-ATTENTION-FRAME](../ui/README.md#P-ATTENTION-FRAME), [P-QUESTIONS](../ui/README.md#P-QUESTIONS), [P-APPROVAL](../ui/README.md#P-APPROVAL), [P-ELICITATION](../ui/README.md#P-ELICITATION), [W-DIALOG-FRAME](../ui/README.md#W-DIALOG-FRAME), [W-ATTENTION-NOTICE](../ui/README.md#W-ATTENTION-NOTICE), [W-QUESTIONS](../ui/README.md#W-QUESTIONS), [W-APPROVAL](../ui/README.md#W-APPROVAL), [W-ELICITATION](../ui/README.md#W-ELICITATION), [C-RUN-ATTENTION](../ui/README.md#C-RUN-ATTENTION), [C-ATTENTION-CHAT](../ui/README.md#C-ATTENTION-CHAT), [C-ANSWER](../ui/README.md#C-ANSWER), [C-EXTERNAL-AUTH](../ui/README.md#C-EXTERNAL-AUTH), [PAGE-AGENTS](../ui/README.md#PAGE-AGENTS), [P-WAITING-QUEUE](../ui/README.md#P-WAITING-QUEUE), [W-WAITING-ROW](../ui/README.md#W-WAITING-ROW), [C-GLOBAL-ATTENTION](../ui/README.md#C-GLOBAL-ATTENTION), [W-QUESTION-RECOVERY](../ui/README.md#W-QUESTION-RECOVERY), [W-SESSION-CONSOLE](../ui/README.md#W-SESSION-CONSOLE), [C-TEXT-DISMISS](../ui/README.md#C-TEXT-DISMISS).

## Flow diagram

```mermaid
flowchart TD
    N0["Display original request and blocking status"]
    N1["Choose answer or complete supported form #40;S2#41;"]
    N2["Save the private reply and claim the matching pending request together once #40;S1#41;"]
    N3(["Answered request stays out of attention after reopening or recovery"])
    N4["Reject invalid, duplicate, resolved or stale response"]
    N5["Correct supported answer or refresh pending request"]
    N6(["Claimed reply remains saved after uncertain delivery#59; no reopening or automatic replay"])
    N7["Nonblocking request leaves work active #40;S3#41;"]
    N8["Inspect a stopped reply for an unresolved human question #40;S4#41;"]
    N9["Request a native question form in the same session at most twice"]
    N10["Show the pending question with a free-text answer"]
    N11["Open the same retained session console"]
    N12["Cancel further repair and return control to the user"]
    N13["Read the current question queue in chat with owning item, profile and exact choices #40;S5#41;"]
    N14(["No pending requests#59; do not infer task completion"])
    N15["Read a fresh queue and owning run#59; report continuation or another wait"]
    N16["Check explicit same-run owner decisions and later agent acknowledgements for each retained question #40;S4#41;"]
    N17(["Retire only settled questions and their recovery state#59; retain evidence references"])
    N18(["Keep unanswered or unverified questions#59; never infer resolution from another run or publication alone"])
    N0 -->|"answer"| N1
    N0 -->|"nonblocking"| N7
    N7 -->|"respond"| N1
    N1 --> N2
    N2 -->|"valid and delivered"| N3
    N2 -->|"invalid or stale"| N4
    N4 --> N5
    N5 -->|"refresh"| N0
    N2 -->|"claimed then delivery fails"| N6
    N0 -->|"no supported form"| N8
    N8 -->|"ordinary completion"| N3
    N8 -->|"human input needed"| N9
    N9 -->|"native form received"| N1
    N9 -->|"two failures or unavailable recovery"| N10
    N10 -->|"answer directly"| N1
    N10 -->|"inspect session"| N11
    N11 -->|"reply"| N1
    N9 -->|"explicit user takeover"| N12
    N12 -->|"retain session and draft"| N11
    N10 -->|"No reply needed#59; dismiss local attention only"| N3
    N0 -->|"Ask which agents need input"| N13
    N13 -->|"Show selected current request and ask the user"| N0
    N13 -->|"No answerable requests"| N14
    N2 -->|"Chat answer accepted"| N15
    N15 -->|"Owning agent continued#59; report observed status"| N3
    N15 -->|"Another request is pending"| N13
    N0 -->|"delivered follow-up, completed turn or startup"| N16
    N16 -->|"exact unchanged-conversation evidence settles every question"| N17
    N16 -->|"partial settlement#59; display original remaining IDs/options"| N0
    N16 -->|"unrelated message, helper failure, stale evidence or no explicit acknowledgement"| N18
```

## Implementation references

- [internal/app/runs.go](../../internal/app/runs.go) — Durable claims and request-specific validation (`func (m *Manager) Answer(`)
- [web/src/main.tsx](../../web/src/main.tsx) — Question, approval and form dialogs (`function AttentionDialog(`)
- [internal/codex/client.go](../../internal/codex/client.go) — Process request-response transport
- [internal/domain/types.go](../../internal/domain/types.go) — Shared domain, persistence, transport or dependency contract
- [internal/store/store.go](../../internal/store/store.go) — Shared domain, persistence, transport or dependency contract
- [internal/transport/mcp.go](../../internal/transport/mcp.go) — Shared domain, persistence, transport or dependency contract
- [go.mod](../../go.mod) — Shared domain, persistence, transport or dependency contract
- [go.sum](../../go.sum) — Shared domain, persistence, transport or dependency contract
- [web/src/types.ts](../../web/src/types.ts) — Embedded UI build and host-fixture configuration
- [web/src/style.css](../../web/src/style.css) — Embedded UI build and host-fixture configuration
- [web/src/bridge.ts](../../web/src/bridge.ts) — Embedded UI build and host-fixture configuration
- [web/package.json](../../web/package.json) — Embedded UI build and host-fixture configuration
- [web/package-lock.json](../../web/package-lock.json) — Embedded UI build and host-fixture configuration
- [web/tsconfig.json](../../web/tsconfig.json) — Embedded UI build and host-fixture configuration
- [web/vite.config.ts](../../web/vite.config.ts) — Embedded UI build and host-fixture configuration
- [web/index.html](../../web/index.html) — Embedded UI build and host-fixture configuration
- [web/playwright.config.ts](../../web/playwright.config.ts) — Embedded UI build and host-fixture configuration
- [internal/testhost/main.go](../../internal/testhost/main.go) — Embedded UI build and host-fixture configuration
- [internal/testutil/fakecodex/main.go](../../internal/testutil/fakecodex/main.go) — Embedded UI build and host-fixture configuration
- [internal/testutil/testutil.go](../../internal/testutil/testutil.go) — Embedded UI build and host-fixture configuration
- [internal/web/embed.go](../../internal/web/embed.go) — Embedded UI build and host-fixture configuration
- [web/src/modal-focus.ts](../../web/src/modal-focus.ts) — Initial modal focus, visible-control Tab containment, Escape dismissal, focus preservation across updates and conditional opener restoration. (`export function useModalFocus(`)
- [internal/app/questions.go](../../internal/app/questions.go) — Durable native/message/text question routing, bounded form repair, redaction and retained continuation. (`func (m *Manager) answerSynthetic(`)
- [internal/app/plugins.go](../../internal/app/plugins.go) — Plugin maintenance excludes new answer delivery without replay. (`func (m *Manager) PluginAction(`)
- [internal/app/attention.go](../../internal/app/attention.go) — Read-only oldest-first actionable question/approval queue with compact owning context, run filtering, bounded cursor pagination and no provider RPC IDs or answer data. (`func (a *App) ListAttention(`)
- [plugin/skills/code-factory/SKILL.md](../../plugin/skills/code-factory/SKILL.md) — Conversational queue discovery, exact user/question matching, safe one-time answer, fresh queue/run verification and explicit host subscription limits.
- [internal/app/continuation.go](../../internal/app/continuation.go) — Verify fresh source for initial/automatic continuation; message-triggered restoration supplies saved context and lets the agent perform task-required GitHub reads. (`func (m *Manager) continuationContext(`)
- [internal/providers/continuation.go](../../internal/providers/continuation.go) — Targeted conditional GitHub source read bypasses local TTL while retaining shared budget and cooldown enforcement. (`func (g *GitHub) CurrentWorkItem(`)
- [internal/transport/tracking.go](../../internal/transport/tracking.go) — Targeted saved-state reads, compact tracking, durable progress or asynchronous observation without delaying status. (`func (s *Server) dashboard(`)
- [internal/store/reads.go](../../internal/store/reads.go) — Targeted saved-state reads, compact tracking, durable progress or asynchronous observation without delaying status. (`func Select[T any](`)
- [internal/app/attention_reconciliation.go](../../internal/app/attention_reconciliation.go) — Background same-run per-question reconciliation after delivery, completion and startup. Requires exact owner decision and subsequent agent acknowledgement; uncertainty preserves attention, native approvals are excluded. (`func (m *Manager) reconcileAttention(`)
- [internal/store/conversation.go](../../internal/store/conversation.go) — Bounded same-run redacted conversation evidence excludes tools, internal recovery input, old source messages and other runs. (`func (s *Store) ConversationEvidence(`)
- [internal/store/vault.go](../../internal/store/vault.go) — Encrypt exact attention replies in the same transaction as answer and recovery claims; no plaintext answer payload in public state. (`func (v *Vault) SetTx(`)

## Test references

- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestAgentQuestionExactlyOnceAndRecovery(`; S1. Successful answer and duplicate rejection.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestConcurrentPollingSteeringAndAnswersThenDirtyCleanup(`; S1. Concurrent answers have one successful claimant.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestAttentionValidationPreservesPermissionAndFormBoundaries(`; S2. Permission subset and schema validation.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestNonblockingApprovalsCrashAndResume(`; S2, S3. Nonblocking status and approval choices.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestRecoveryStalesRequestsAndPreservesIdleRuns(`; S3. Completed idle sessions retain their status and workspace; expired native requests become stale and reject answers.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up"`; S1. Actual UI dialog through assembled fixture.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `test("keyboard attention stays contained through updates and Escape restores the pending opener"`; S1. Keyboard question choices survive polling, Tab remains contained, Escape defers without answering and restores opener; later one answer reaches idle with polite run status.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `test("global attention selects its owning run and returns to its conversation after answering"`; S1. Owning title/profile and run selection survive answer completion.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `readable approval choices preserve payloads in a narrow`; S2. Both themes preserve original decisions and requested permission-turn payloads behind readable labels.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `Messages lists waiting agents and answering removes only its owning request`; S1. Two waiting agents show item/profile/questions; selecting second identifies its context, exact request is answered, its conversation opens, and the other request remains.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStoppedQuestionRecoveryNativeAndFallback(`; S4. Same-session bounded question repair, at-most-once answers and original-mode restoration.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStructuredMessageQuestionSurvivesRestartAndClaimsAnswerOnce(`; S4. Async questions survive restart and concurrent answers have one claimant.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `text fallback opens the same session console`; S4. Fallback routes the exact answer to its request and console mode retains the reply draft without a new launch.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestTextQuestionDismissalCannotAnswerOrApproveNativeRequests(`; S4. Text-only dismissal cannot approve a native request or launch work.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `an uncertain textual reply can be dismissed without advancing`; S4. Uncertain helper output is reviewable and dismissible without sending another task turn.
- [internal/app/plugins_test.go](../../internal/app/plugins_test.go) — `func TestPluginMaintenanceCannotRaceStartsOrAcceptInjectedArguments(`; S1. An answer call is refused during exclusive plugin maintenance before any request can be delivered.
- [internal/app/attention_test.go](../../internal/app/attention_test.go) — `func TestAttentionQueueFiltersAndPaginatesWithoutSkippingResolvedRequests(`; S5. Queue includes live RPC and retained text/message asks; excludes answered/stale/orphaned/prepared/cleaned requests, preserves options and blocking status, strips unrelated run content, validates cursors/limits and never skips asks when a preceding page is resolved.
- [internal/transport/attention_test.go](../../internal/transport/attention_test.go) — `func TestMCPAttentionQueueWaitAnswerContinueAndStaleRecovery(`; S1, S3, S5. Independent MCP SDK fixture prepares and starts two agents, observes blocking questions, rejects wrong IDs/malformed/duplicate answers, resumes the owning session, preserves the other ask and rejects interrupted RPC answers.
- [internal/transport/mcp_test.go](../../internal/transport/mcp_test.go) — `func TestAuthenticatedMCPDiscoveryToolsAndUI(`; S5. Authenticated discovery advertises list_attention to model/app as a read-only local operation; unauthenticated discovery rejects.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestContinuationProvidesFreshChangesWithoutReplacingSession(`; S3. Fresh title/body/update/assignment changes reach the retained agent in structured source context while thread, workspace and original task remain unchanged.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestClosedOrMergedSourcePermanentlyTerminatesContinuation(`; S3. Closed issue, closed PR and merged PR prevent another provider invocation; messages/resume are refused and dirty cleanup preserves terminal state and unsaved files.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestUnverifiedSourceNeverStartsProviderOrReplaysTask(`; S3. Failed fresh source reads fail visibly before provider or workspace creation with no stale fallback.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestSyncedClosureStopsActiveWorkAndRetiresQuestions(`; S3. Synced source closure retires active provider work, stales pending questions, survives restart without resurrecting asks and remains terminal after source reopening.
- [web/tests/factory.spec.ts](../../web/tests/factory.spec.ts) — `terminated work retains history and cleanup without allowing continuation`; S3. Both themes show Stopped and terminal reason, retain cleanup and history, disable Send and expose no Interrupt or launch action.
- [internal/transport/tracking_test.go](../../internal/transport/tracking_test.go) — `func TestMCPDailyWorkflowsUseOneOrTwoCallsAndReturnDeliveryState(`; S5. Asserts saved-state target resolution, exact question/receipt pagination, bounded projection or daily one/two-call delivery behavior.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestDeliveredMessageReconcilesOnlyAcknowledgedQuestions(`; S4. Partial acknowledgement retires only Q1, retains actionable Q2 and permits its answer; full same-run acknowledged completion clears the group; unrelated continuation preserves both.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationPreservesUnprovenOrOtherRunRequests(`; S4. Other-run review completion, forged quotes/IDs, reversed evidence order and an intervening run turn cannot retire the original requests.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationHelperFailureRetainsQuestions(`; S4. An invalid helper decision leaves both original requests pending after delivery and continuation.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationRetiresRecoveryAndPreservesNativeApprovals(`; S4. A cancelled synthetic question retires its recovery episode without answering or dismissing an unrelated native command approval.
- [internal/app/attention_answers_test.go](../../internal/app/attention_answers_test.go) — `func TestAnsweredRepairNeverReappearsAfterRecovery(`; S1, S4, S5. Restart settles answered and legacy partially saved recovery episodes, retires pre-existing duplicate fallbacks, preserves unrelated unanswered requests and remains stable on repeated recovery.
- [internal/app/attention_answers_test.go](../../internal/app/attention_answers_test.go) — `func TestAnsweredRepairDisconnectDoesNotCreateFallback(`; S4. A provider disconnect after claiming a repair answer resolves recovery without creating another text question.
- [internal/app/attention_answers_test.go](../../internal/app/attention_answers_test.go) — `func TestAttentionRepliesPersistEncryptedAndStayOutOfQueue(`; S1, S2, S3, S4, S5. Native, nonblocking, approval, message and text replies survive a new database/vault connection exactly; invalid/duplicate submissions cannot overwrite them and repeated restart keeps the queue empty.
- [internal/app/attention_answers_test.go](../../internal/app/attention_answers_test.go) — `func TestUncertainAttentionReplyIsRetainedWithoutReopening(`; S1, S4, S5. Unavailable session leaves an encrypted exact reply and uncertain status, no plaintext journal, no reopened request and no duplicate replay.
- [internal/app/attention_answers_test.go](../../internal/app/attention_answers_test.go) — `func TestNativeAnswerRollsBackWhenRecoveryCannotBeSaved(`; S1, S4. A missing recovery record rolls back both the encrypted answer and native answer claim, leaving the unanswered request visible and answerable.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.
- **S2** — `gap-flow-answer-attention-s2-01` · Unverified behavior · P1 · owner: specification-coordinator. External native user-verification and every supported approval/dialog variant lack real-host coverage. Next verification: Record actual-host native user-verification and supported approval/dialog variants using version-bound nonsecret observations; preserve fixture and real-host distinctions.
- **S5** — `gap-flow-answer-attention-s5-01` · Unverified behavior · P2 · owner: specification-coordinator. Installed ChatGPT Work now directly invokes list_attention, presents the exact native question, delivers the user choice through answer_request and observes same-thread continuation, with independently audited MCP method names. Actual-host multi-agent pagination, stale/duplicate answers, uncertain delivery and recovered-text question variants remain unverified; fixtures cover those boundaries. Next verification: Observe the remaining multi-agent/page, stale/duplicate, uncertain-delivery and recovered-text variants in an explicitly scoped private-pilot campaign, without replaying delivery or substituting runs.
- **S4** — `gap-flow-answer-attention-s4-reconciliation-real-host` · Unverified behavior · P2 · owner: user-flow-maintainer. Same-run per-question resolution and conservative failure boundaries pass fixtures, but installed real-helper partial/full acknowledgement classification has not been observed. Next verification: In the dedicated private pilot, send a partial answer to two async questions, observe the real helper retain the unanswered question, then settle it and verify restart persistence and actionable queue removal.

<!-- /flow-references -->
