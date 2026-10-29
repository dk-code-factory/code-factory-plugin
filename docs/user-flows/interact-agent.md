# Follow up, steer and interrupt agent work

## Goal

The owner continues an existing task or stops its active turn while retaining the work.

## Preconditions

A previously launched run has a live or retained agent session.

## Main flow

1. Select an item to open its conversation directly, or choose a retained conversation under that item’s Work history; read its task and human-facing messages.
2. Send a follow-up after a turn completes to begin the next turn.
3. Send a clarification during active work to steer the displayed turn.
4. Send to inactive retained work to continue the same conversation and workspace. Interrupt active work from secondary details when requested, preserving workspace and history.

A single-session launch retains its native session for questions, answers and follow-up even when the system execution setting changes. Skill-required native child work remains coordinated by that session.

## Alternatives and recovery

Daemon startup automatically continues unfinished work in the same retained session using saved progress. Completed idle sessions remain idle and accept a follow-up without a new task. Explicit interrupts remain stopped until the owner continues them; startup does not undo an interrupt.

A message for an outdated active turn is rejected. Sending a message to interrupted or failed retained work restores its existing session and starts exactly that submitted message, with no original-task or generic-resume turn replay. Concurrent wake attempts cannot start multiple turns. Failed acceptance retains the draft; accepted messages remain saved even if later delivery fails. Back returns to the originating work queue or Messages. The list is absent while the conversation is open; Messages shows valid unanswered questions and approvals, including nonblocking and retained message/text questions; retained conversations remain scoped to their item under Work history. Sending a message does not create a second run. Message drafts stay with each run across tabs and run selection for the lifetime of the open app. Queue acceptance clears only unchanged submitted text; rejected acceptance keeps the draft. Workspace paths and cleanup are secondary details; technical commands, plans, diffs and internal summaries are under Details; progress messages, final replies, questions and readable errors remain visible.

Delivered messages and completed turns reconcile retained questions against explicit same-run owner decisions and later agent acknowledgements. Progress, idle status or publication alone cannot clear an ask. Partial answers remove only their acknowledged questions; other requests and other runs remain independent.

## Scenarios

- **S7** Terminated source work displays Stopped, its reason and retained history. Send and resume are unavailable even after source reopening. Clean-workspace cleanup remains available, and a dirty refusal preserves terminal status and files.

- **S1** Follow-up after idle completes through the same run.
- **S2** Active steering requires the current turn identity and rejects stale steering.
- **S3** Interrupt preserves the run and invalidates outstanding requests. Cancel retry also interrupts a deferred pre-agent PR review and clears its pending deadline even when no agent session is attached; scheduler ticks and restart leave it stopped. A slow provider acknowledgment for one run's interruption does not block unrelated starts, answers, interruptions or accepted retry admission.

Conversation rendering keeps human-facing agent messages and your replies visible, including long messages retained across multiple transcript pages. Technical activity remains under Details, with structured error data there and the readable error message visible. Streaming updates preserve that distinction; a technical-only active stream shows a concise waiting status. Pending questions and approvals remain actionable outside Details.

- **S4** Render native and structured-message questions; classify stopped text and share at most two setup, clarification and form attempts. Offer textual continuation only for a confirmed unanswered question, retaining its delivery status and the same session console. Unconfirmed exhaustion remains diagnostic without attention or automatic task continuation. Preserve answer claims and redaction across recovery.

Automatic recovery exchanges appear as technical activity under Details or in the session console. Your responses keep their ordinary conversation role.

An execution diagnostic received after completion preserves the completed turn
and does not repeat its work or change a later follow-up.

During explicit plugin maintenance, a new Start, message or answer is temporarily refused rather than delivered against changing plugin state. The caller retains its draft and receives the maintenance error. Maintenance is itself refused while agent work is active; question repair and continuation share the same boundary.

- **S5** Restoring an inactive bounded task reapplies its invocation-local filesystem and network profile before the submitted turn. It preserves the same workspace, scratch, private indexes and original approval/publishing authority. A legacy sandbox override cannot discard the scoped policy; invalid initialization fails without starting an unrestricted replacement.

Conversational follow-up normally uses one saved-state tracking read to identify the retained run and expected active turn, then one message operation. Its response includes durable queue acceptance, a message ID and pending attention for that same run; acceptance does not establish agent delivery or task completion.

- **S6** Save a follow-up durably before contacting its agent and return queue acceptance immediately. A blocked agent or unavailable GitHub cannot delay acceptance. Deliver in the background to the same retained session using explicitly unverified saved source context; the agent performs GitHub reads when required by the task. Show queued, delivering, delivered or uncertain status in the conversation and tracking. Recover saved queued messages after restart, but never automatically replay a delivery whose acknowledgement is uncertain or replace it with a generic continuation.



Take over also cancels retained startup recovery before its session reconnects. Cancellation remains effective when reconnect and takeover overlap; later classification cannot recreate attention or restart clarification.

Completion checks queued before takeover cannot start new recovery after an older check finishes. A retired startup session cannot replace the state or active turn of later owner-directed work.

Take over after a repair answer preserves the claimed answer while cancelling its pending automatic task continuation, before or after acknowledgement.

Takeover waiting for a busy recovery turn leaves other runs available to start and present their questions.
Take over also cancels recovery before its first question-only turn. Late questions from that cancelled recovery do not return; a later explicit continuation can ask new genuine questions.

- **S8** Retained PR review continuation accepts an explicit Resume or operational continue message after the previous execution stops. Interrupt waits for active child work to stop before acknowledging completion. If shutdown cannot be verified, it reports the retained error and stops later reviewer work. Validated completed reviewer work is reused across restart, while incompatible implementation or governing inputs require a fresh review with an explanation. Material questions use their owning answer control; new engineering instructions require fresh preparation. Plugin maintenance refuses continuation until it finishes; rejection accepts no message or new review work. Possible child work without durable shutdown proof retains stopped status across failed state saves and abrupt restart; repeated Resume or operational continuation cannot start replacement worker work. A failed final-state save is reported before interruption can acknowledge completion. Questions that have no active child and saved finished reviews retain their recovery paths. Verified shutdown continues to permit explicit Resume and reuse of completed checkpoints.

<!-- flow-references -->
## Terminology and UI references

Glossary: [agent-run](../glossary/README.md#agent-run), [transcript](../glossary/README.md#transcript), [state-idle](../glossary/README.md#state-idle), [state-running](../glossary/README.md#state-running), [coverage-gap](../glossary/README.md#coverage-gap), [unverified-behavior](../glossary/README.md#unverified-behavior), [question-recovery](../glossary/README.md#question-recovery), [session-console](../glossary/README.md#session-console), [state-terminated](../glossary/README.md#state-terminated), [state-deferred](../glossary/README.md#state-deferred), [review-workflow](../glossary/README.md#review-workflow).
UI entities: [PAGE-AGENTS](../ui/README.md#PAGE-AGENTS), [P-RUN-LIST](../ui/README.md#P-RUN-LIST), [P-RUN-DETAIL](../ui/README.md#P-RUN-DETAIL), [W-RUN-ROW](../ui/README.md#W-RUN-ROW), [W-RUN-EMPTY](../ui/README.md#W-RUN-EMPTY), [W-RUN-CONTROLS](../ui/README.md#W-RUN-CONTROLS), [C-SELECT-RUN](../ui/README.md#C-SELECT-RUN), [C-RUN-ACTION](../ui/README.md#C-RUN-ACTION), [W-TRANSCRIPT](../ui/README.md#W-TRANSCRIPT), [W-MESSAGE](../ui/README.md#W-MESSAGE), [C-TRANSCRIPT](../ui/README.md#C-TRANSCRIPT), [C-FOLLOW-UP](../ui/README.md#C-FOLLOW-UP), [PAGE-AGENT](../ui/README.md#PAGE-AGENT), [C-BACK](../ui/README.md#C-BACK), [W-WORK-STATUS](../ui/README.md#W-WORK-STATUS), [W-QUESTION-RECOVERY](../ui/README.md#W-QUESTION-RECOVERY), [W-SESSION-CONSOLE](../ui/README.md#W-SESSION-CONSOLE), [W-RETAINED-REVIEW](../ui/README.md#W-RETAINED-REVIEW).

## Flow diagram

```mermaid
flowchart TD
    N0["Read current status and transcript"]
    N1["Send follow-up when idle #40;S1#41;"]
    N2["Steer current active turn #40;S2#41;"]
    N3["Interrupt active work or cancel deferred launch and stale requests #40;S3#41;"]
    N4["Reject outdated turn message"]
    N5["Send a message to restore inactive retained work"]
    N6(["Retained same-run conversation"])
    N7["Classify stopped text#59; share two setup, clarification and form attempts. Offer only confirmed unanswered textual continuation#59; unresolved stays diagnostic without attention or task replay. Preserve claims, redaction and console. #40;S4#41;"]
    N8["Keep source access bounded, private scratch writable and network destinations scoped #40;S5#41;"]
    N9(["Sandbox initialization failed#59; retained work and authority preserved"])
    N10["Verify current source and report changed scope or head before initial or automatic continuation #40;S7#41;"]
    N11(["Closed or merged source#59; terminate session and retain history/workspace"])
    N12["Save message and acknowledge queue acceptance #40;S6#41;"]
    N13["Deliver saved message to the same agent using retained context"]
    N14(["Keep uncertain delivery visible without automatic replay"])
    N15["Reconcile acknowledged same-run decisions#59; preserve partial, uncertain and other-run asks #40;S6#41;"]
    N16["Continue an interrupted retained review through its existing control #40;S8#41;"]
    N17(["Changed engineering instructions require a fresh prepared review"])
    N18(["Child shutdown verified#59; valid completed work reused"])
    N19(["Missing durable shutdown proof, failed state save or abrupt restart#59; retain stopped work without replacement reviewers"])
    N0 -->|"idle"| N1
    N0 -->|"active"| N2
    N0 -->|"interrupt"| N3
    N2 -->|"stale turn"| N4
    N4 -->|"refresh context"| N0
    N2 -->|"accepted"| N12
    N1 --> N12
    N3 -->|"continue later"| N5
    N5 --> N12
    N0 -->|"inactive message"| N5
    N0 -->|"additional workbench path"| N7
    N7 -->|"resolved or explicit recovery"| N6
    N0 -->|"authorized bounded task start or continuation"| N8
    N8 -->|"valid policy#59; retain scoped work"| N6
    N8 -->|"invalid policy#59; fail before provider launch"| N9
    N0 -->|"initial launch or automatic unfinished-work continuation"| N10
    N10 -->|"source closed or merged"| N11
    N10 -->|"open source#59; agent assesses fresh changes"| N0
    N12 -->|"background delivery"| N13
    N13 -->|"agent accepted message"| N6
    N13 -->|"delivery acknowledgement unavailable"| N14
    N13 -->|"message delivered and agent acknowledges decisions"| N15
    N15 -->|"only settled questions leave actionable attention"| N6
    N0 -->|"retained review workflow"| N16
    N16 -->|"explicit continuation after verified child shutdown"| N18
    N16 -->|"new engineering instruction"| N17
    N16 -->|"possible child work without durable shutdown proof"| N19
```

## Implementation references

- [internal/app/runs.go](../../internal/app/runs.go) — Message routing and interrupt (`func (m *Manager) Message(`)
- [web/src/main.tsx](../../web/src/main.tsx) — Agent chat and interrupt controls (`function Agent(`)
- [internal/codex/client.go](../../internal/codex/client.go) — Turn RPC requests and notifications
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
- [web/fixture-ports.ts](../../web/fixture-ports.ts) — Shared validated loopback fixture origins preserve default ports and allow isolated browser verification alongside another checkout. (`export const fixtureUIOrigin =`)
- [internal/testhost/main.go](../../internal/testhost/main.go) — Embedded UI build and host-fixture configuration
- [internal/testutil/fakecodex/main.go](../../internal/testutil/fakecodex/main.go) — Embedded UI build and host-fixture configuration
- [internal/testutil/testutil.go](../../internal/testutil/testutil.go) — Embedded UI build and host-fixture configuration
- [internal/web/embed.go](../../internal/web/embed.go) — Embedded UI build and host-fixture configuration
- [internal/app/questions.go](../../internal/app/questions.go) — Durable native/message/text question routing, bounded form repair, redaction and retained continuation. (`func (m *Manager) answerSynthetic(`)
- [internal/app/plugins.go](../../internal/app/plugins.go) — Durable pinned-CLI plugin lifecycle and exclusive runtime/maintenance boundary. (`func (m *Manager) PluginAction(`)
- [internal/domain/admin.go](../../internal/domain/admin.go) — Typed plugin operation and inventory persistence. (`type PluginOperation struct`)
- [internal/app/sandbox.go](../../internal/app/sandbox.go) — Invocation-local native filesystem and destination policy; isolated writable scratch and private skill indexes without global configuration changes.
- [internal/codex/sandbox.go](../../internal/codex/sandbox.go) — Invocation-local native filesystem and destination policy; isolated writable scratch and private skill indexes without global configuration changes.
- [internal/app/continuation.go](../../internal/app/continuation.go) — Verify fresh source for initial/automatic continuation; message-triggered restoration supplies saved context and lets the agent perform task-required GitHub reads. (`func (m *Manager) continuationContext(`)
- [internal/providers/continuation.go](../../internal/providers/continuation.go) — Targeted conditional GitHub source read bypasses local TTL while retaining shared budget and cooldown enforcement. (`func (g *GitHub) CurrentWorkItem(`)
- [internal/app/tracking.go](../../internal/app/tracking.go) — Saved bounded tracking returns only active running/waiting concurrency tokens for follow-up; inactive retained turns remain historical identity in currentRun, with no remote read required. (`func (a *App) TrackWork(`)
- [internal/transport/tracking.go](../../internal/transport/tracking.go) — Targeted saved-state reads, compact tracking, durable progress or asynchronous observation without delaying status. (`func (s *Server) dashboard(`)
- [internal/app/messages.go](../../internal/app/messages.go) — Durably save encrypted messages before asynchronous same-session delivery; recover queued messages and retain uncertainty without replay. (`func (m *Manager) QueueMessage(`)
- [internal/app/attention_reconciliation.go](../../internal/app/attention_reconciliation.go) — Background same-run per-question reconciliation after delivery, completion and startup. Requires exact owner decision and subsequent agent acknowledgement; uncertainty preserves attention, native approvals are excluded. (`func (m *Manager) reconcileAttention(`)
- [internal/store/conversation.go](../../internal/store/conversation.go) — Bounded same-run redacted conversation evidence excludes tools, internal recovery input, old source messages and other runs. (`func (s *Store) ConversationEvidence(`)
- [internal/store/conversation.go](../../internal/store/conversation.go) — Transaction guard counts ordinary owner/agent completions and opaque same-run completion chunks as possible conversation changes without importing omitted content as evidence. (`func (t *Tx) ConversationVersion(`)
- [internal/app/launch_retry.go](../../internal/app/launch_retry.go) — Persist deadline-based pre-agent launch deferral, conservative legacy PR-review recovery and automatic-policy revalidation without duplicate claims. (`func (m *Manager) RetryDeferredLaunches(`)
- [internal/aiworkflows/client.go](../../internal/aiworkflows/client.go) — YAML workflow execution retains native session policy, questions, interrupts and application-owned recovery. (`func (c *Client) CallWorkflow(`)
- [internal/aiworkflows/workflows/attention-check.yml](../../internal/aiworkflows/workflows/attention-check.yml) — Embedded model-turn workflow definition and native structured-output contract where applicable. (`name:`)
- [internal/aiworkflows/workflows/question-check.yml](../../internal/aiworkflows/workflows/question-check.yml) — Embedded model-turn workflow definition and native structured-output contract where applicable. (`name:`)
- [internal/aiworkflows/workflows/question-repair.yml](../../internal/aiworkflows/workflows/question-repair.yml) — Embedded model-turn workflow definition and native structured-output contract where applicable. (`name:`)
- [internal/aiworkflows/workflows/review.yml](../../internal/aiworkflows/workflows/review.yml) — Embedded model-turn workflow definition and native structured-output contract where applicable. (`name:`)
- [internal/aiworkflows/workflows/task.yml](../../internal/aiworkflows/workflows/task.yml) — Embedded model-turn workflow definition and native structured-output contract where applicable. (`name:`)
- [internal/app/review_workflows.go](../../internal/app/review_workflows.go) — Retained review continuation uses existing message controls without altering governing scope or overlapping an old worker. (`func (m *Manager) queueReviewContinuation(`)
- [internal/prreview/native.go](../../internal/prreview/native.go) — Production SDK actions, durable origin/gate journaling, ordered child reviews and app-answer reconciliation. (`func (a NativeAdapter) Execute(`)
- [internal/app/review_workers.go](../../internal/app/review_workers.go) — Default independent Codex model workers and sandboxed command reviewers retain selected profile, model, effort and package resources. (`func (m *Manager) productionReviewWorkers(`)
- [internal/app/review_worker_admission.go](../../internal/app/review_worker_admission.go) — Commit execution-bound protection before actual worker dispatch; release only matching verified return proof and retain uncertainty on failed persistence or unjoined child work. (`func admittedReviewCall[`)

## Test references

- [web/tests/assembled.spec.ts](../../web/tests/assembled.spec.ts) — `test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up"`; S1. Assembled UI sends follow-up on the original run.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestAgentQuestionExactlyOnceAndRecovery(`; S2. Stale steering is rejected; matching steering succeeds.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `test("Open agent targets the newest retained run and keeps message drafts across tabs and runs"`; S1. Item shortcut chooses newest retained work; per-run drafts survive navigation without implicit send/start.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `agent conversation shows human messages and keeps technical activity under Details`; S1. Human progress/final/user/error messages stay visible; technical activity is one secondary disclosure, live updates preserve the distinction and questions remain actionable.
- [web/tests/inbox.spec.ts](../../web/tests/inbox.spec.ts) — `single-page navigation preserves list context and drafts at`; S1. Both themes and 1280px/390px widths assert exclusive list/item/agent pages, Back focus/scroll/query restoration and per-record draft preservation.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `transcript recovery keeps output and draft while removing only its own read error`; S1. After transcript transport failures, previous output and follow-up draft remain and only the local read error disappears on recovery.
- [web/tests/connection.spec.ts](../../web/tests/connection.spec.ts) — `mutation connection failure is not replayed and later polling does not dismiss it`; S1. An uncertain Send is attempted once; subsequent healthy polls do not clear its error or submitted draft.
- [web/tests/connection.spec.ts](../../web/tests/connection.spec.ts) — `exact host-wrapped MCP connection error retries a read without exposing its framework text`; S1. Known host-wrapped transport failures recover on reads without framework banners, and mutations remain one attempt.
- [web/tests/connection.spec.ts](../../web/tests/connection.spec.ts) — `exact host-wrapped connection failure never replays a mutation`; S1. Known host-wrapped transport failures recover on reads without framework banners, and mutations remain one attempt.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `technical-only activity leaves a concise waiting state in the conversation`; S1. Technical-only work does not flood the normal conversation; status and pending attention remain available.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestMessageRestoresRetainedSessionWithOneSubmittedTurn(`; S1. An explicitly interrupted session remains stopped across startup; a follow-up retains run/thread/worktree/authority and eight concurrent messages accept exactly one submitted turn.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestWakeMessageFailureKeepsRetainedRecordAndRejectsCleanedWork(`; S1. Missing retained worktree returns wake failure without losing the retained record; prepared and cleaned work cannot accept a message.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `inactive work status and sending a message restore the retained session`; S1. Both themes verify work-item activity and message-triggered continuation, no Resume control, one submit and retained failure draft.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestInterruptThenMessageStalesOldQuestionAndRestoresSameSession(`; S3. Interrupting a waiting fake provider stales its question and clears the turn; message wake restores the same thread/worktree, rejects the old request and answers only the new one.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStoppedQuestionRecoveryNativeAndFallback(`; S4. Same-session bounded question repair, at-most-once answers and original-mode restoration.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestStructuredMessageQuestionSurvivesRestartAndClaimsAnswerOnce(`; S4. Async questions survive restart and concurrent answers have one claimant.
- [web/tests/inbox.spec.ts](../../web/tests/inbox.spec.ts) — `text fallback opens the same session console`; S4. Fallback routes the exact answer to its request and console mode retains the reply draft without a new launch.
- [internal/app/plugins_test.go](../../internal/app/plugins_test.go) — `func TestPluginMaintenanceCannotRaceStartsOrAcceptInjectedArguments(`; S1, S2. Start, message and answer are refused during maintenance, preserving unsubmitted input semantics and worktree absence.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestTaskSandboxRetainsIsolationAcrossSessionWake(`; S5. Start and automatic startup continuation retain the same bounded process profile and thread without a legacy policy override; explicit cleanup removes private scratch.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestSandboxSkillDataIsPrivateBoundedAndRetained(`; S5. Refreshing private skill indexes cannot alter the owner source; resume keeps run caches and rejects symlink sources.
- [internal/app/sandbox_test.go](../../internal/app/sandbox_test.go) — `func TestInvalidTaskSandboxFailsWithoutProviderFallback(`; S5. Invalid sandbox fails before app-server launch without an unrestricted fallback.
- [internal/codex/sandbox_test.go](../../internal/codex/sandbox_test.go) — `func TestTaskSandboxPolicyAndInvalidRoots(`; S5. Independent workspace/scratch filesystem and network permissions reject invalid or global roots; proxy allowlist excludes arbitrary hosts.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestContinuationProvidesFreshChangesWithoutReplacingSession(`; S7. Fresh title/body/update/assignment changes reach the retained agent in structured source context while thread, workspace and original task remain unchanged.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestClosedOrMergedSourcePermanentlyTerminatesContinuation(`; S7. Closed issue, closed PR and merged PR prevent another provider invocation; messages/resume are refused and dirty cleanup preserves terminal state and unsaved files.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestUnverifiedSourceNeverStartsProviderOrReplaysTask(`; S7. Failed fresh source reads fail visibly before provider or workspace creation with no stale fallback.
- [internal/app/continuation_test.go](../../internal/app/continuation_test.go) — `func TestSyncedClosureStopsActiveWorkAndRetiresQuestions(`; S7. Synced source closure retires active provider work, stales pending questions, survives restart without resurrecting asks and remains terminal after source reopening.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `terminated work retains history and cleanup without allowing continuation`; S7. Both themes show Stopped and terminal reason, retain cleanup and history, disable Send and expose no Interrupt or launch action.
- [internal/transport/tracking_test.go](../../internal/transport/tracking_test.go) — `func TestMCPDailyWorkflowsUseOneOrTwoCallsAndReturnDeliveryState(`; S5. Asserts saved-state target resolution, exact question/receipt pagination, bounded projection or daily one/two-call delivery behavior.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestMessageQueueCommitsBeforeBlockedAgentAndSurvivesCallerCancellation(`; S6. Blocked session cannot delay queue acceptance; committed encrypted payload survives caller cancellation, rejects duplicate pending sends and becomes delivered.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestQueuedFollowUpRecoversSameThreadWithoutAnyGitHubPreflight(`; S6. Offline queue recovery restores the retained thread exactly once with saved context and zero GitHub requests.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestInterruptedMessageDeliveryIsUncertainAndNeverReplayed(`; S6. Interrupted delivery is marked uncertain and prevents automatic message or generic-continuation replay.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestQueueMessageRejectsStaleAndUnstartedTargetsBeforeAcceptance(`; S6. Prepared and stale-turn messages reject before any accepted message record exists.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `saved message clears its draft before agent delivery and shows uncertainty without replay`; S6. Accepted queue receipt clears its submitted draft before agent activation; transcript replaces queue feedback with uncertainty and never replays send or starts a substitute run.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestAcceptedMessageRemainsSavedWhenAgentRestorationFails(`; S6. A nonexistent retained workspace does not prevent queue acceptance; failed restoration preserves encrypted accepted text and reports uncertainty on the same thread.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestDeliveredMessageReconcilesOnlyAcknowledgedQuestions(`; S6. Partial acknowledgement retires only Q1, retains actionable Q2 and permits its answer; full same-run acknowledged completion clears the group; unrelated continuation preserves both.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationPreservesUnprovenOrOtherRunRequests(`; S6. Other-run review completion, forged quotes/IDs, reversed evidence order and an intervening run turn cannot retire the original requests.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationHelperFailureRetainsQuestions(`; S6. An invalid helper decision leaves both original requests pending after delivery and continuation.
- [internal/app/questions_test.go](../../internal/app/questions_test.go) — `func TestTextQuestionFollowupRejectsStaleActiveTurnBeforeClaim(`; S2, S6. Wrong or empty active-turn tokens reject a textual follow-up before attention/Vault claim or provider request; pending question remains.
- [internal/app/messages_test.go](../../internal/app/messages_test.go) — `func TestDisconnectedQuestionFollowupRestoresOneSubmittedTurn(`; S1, S6. Actual provider-loss follow-up uses the returned TrackWork continuation fields, preserves historical turn/thread/workspace/authority and encrypted claim, and resumes exactly the submitted response without original-task or generic replay.
- [web/tests/sessions.spec.ts](../../web/tests/sessions.spec.ts) — `chunked owner and agent completions remain normal conversation across journal pages`; S1. Long owner/unstreamed agent Unicode and escaped completions across a journal page retain You/Agent roles and ID coalescing in the normal conversation; raw partial/malformed and command output stay under initially collapsed Details.
- [internal/app/attention_reconciliation_test.go](../../internal/app/attention_reconciliation_test.go) — `func TestAttentionReconciliationRejectsChunkedConversationChanges(`; S6. Actual oversized completion producer changes journal version while run status/turn stay fixed; stale inference preserves the exact pending request. Unchanged/other-run/ordinary-tool controls still settle; opaque tool chunks conservatively preserve uncertainty.
- [internal/app/tracking_test.go](../../internal/app/tracking_test.go) — `func TestTrackingContinuationUsesOnlyActiveTurnTokens(`; S1, S6. Idle/interrupted/failed continuations return empty expectedTurn while preserving historical turnId; running/waiting retain the real token and reject stale active submissions.
- [web/tests/launch.spec.ts](../../web/tests/launch.spec.ts) — `a late first-message Start cannot replace another item's draft`; S1. With a delayed immutable accepted Start response and lagging saved reads, the Messages badge proves React processed the response before asserting another item and exact draft remain selected; private guard removal fails after this barrier.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestClarificationTakeoverAndAnswerRace(`; S3. Locally bounded concurrent owner takeover and native answer preserve the durable claim; cancellation stays reachable on stall and late recovery callbacks cannot recreate questions.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestTakeoverInvalidatesLegacyClassifierCallback(`; S3. An explicit provider gate holds legacy classification pending until takeover completes; the later callback cannot establish a question, with a bounded completion wait and reachable cleanup.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestStartupTakeoverCancelsRecoveryBeforeSessionAttachment(`; S3. Provider-gated startup checking/clarifying classification remains pending until zero-attempt takeover commits before attachment; released callbacks cannot create attention or reconnect.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestStartupTakeoverCancelsAfterSessionAttachment(`; S3. An initialization gate proves one durable claimed attempt with no delivered turn; takeover suppresses late native/async setup callbacks and later explicit continuation can ask a fresh genuine question.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestStalledRecoveryTakeoverDoesNotBlockAnotherRunsQuestion(`; S3. A blocked-goroutine stack proves takeover reached the session mutex held by gated recovery turn/start before another run starts and presents its genuine question; release lets takeover finish.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestLiveAsyncRecoveryAnswerRestoresOriginalPolicy(`; S1. A live async repair-turn answer retires matching duplicate store/session routes, completes the repair and continues with original mode/model/effort/sandbox/approval policy while retaining the durable claim.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestTakeoverAfterClaimPreventsQueuedRepairContinuation(`; S3. Before and after acknowledgement, takeover finalizes the claimed async answer and prevents a causally released queued continuation before provider shutdown. Private missing-cancellation mutation fails claim and actual continuation assertions.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestRetiredResumeCannotOverwriteOwnerReplacement(`; S3. An actual successful retained thread/resume response is held at its session lock; a staged lock-owned retirement/replacement transition proves the retired launch cannot overwrite the replacement's running state, turn or identity.
- [internal/app/question_classification_test.go](../../internal/app/question_classification_test.go) — `func TestTakeoverInvalidatesEntireQueuedCompletion(`; S3. Takeover during provider-gated historical reconciliation invalidates the entire queued completion callback, preventing the subsequent stopped-turn classifier from creating fresh recovery or attention.
- [web/tests/launch.spec.ts](../../web/tests/launch.spec.ts) — `GitHub-deferred review shows its retry reason and deadline and can cancel before a thread starts`; S3. Working list and focused conversation show Waiting for GitHub, retained reason and localized deadline with exact datetime; no alert or owner question appears. A threadless run cannot send messages; Cancel retry sends one interrupt_run and leaves it inactive without Start/message mutations.
- [internal/app/launch_retry_test.go](../../internal/app/launch_retry_test.go) — `func TestDeferredLaunchInterruptAndCleanupCancelAttachedAndDetachedRuns(`; S3. Interrupt and cleanup clear retry deadline/pending authority for attached and detached launches; restart and later ticks start no agent.
- [internal/app/launch_retry_test.go](../../internal/app/launch_retry_test.go) — `func TestDeferredLaunchProjectionRetainsWorkingQueueAndRetryDeadline(`; S3. Deferred work stays in Working with Waiting for GitHub; tracking retains launchRetryAt and cancellation clears the waiting projection.
- [internal/app/launch_retry_test.go](../../internal/app/launch_retry_test.go) — `func TestDeferredLaunchCancelAndCloseDuringSourceVerification(`; S3. Interrupt, manager close, policy disable and repository pause during blocked live verification prevent provider start; interrupt/close create no workspace.
- [internal/app/launch_retry_test.go](../../internal/app/launch_retry_test.go) — `func TestSlowInterruptLeavesOtherRunsAndDeferredAdmissionResponsive(`; S3. A real fake-provider interrupt stays blocked behind a controlled process barrier while deferred polling, unrelated Start, Answer and Interrupt complete; cancellation intent and exact original RPC IDs persist, then the same accepted deferred claim starts after capacity clears.
- [internal/app/launch_retry_test.go](../../internal/app/launch_retry_test.go) — `func TestSlowInterruptCannotChangeReplacementOrCleanedSession(`; S3. A completed old turn can be replaced or deliberately cleaned while its interrupt response is blocked; the old RPC reports disconnect and stale old-session events cannot overwrite replacement authority or resurrect cleaned work.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestWorkflowPreservesPolicySkillsAndInteractiveReplies(`; S1. YAML execution regression; assertions preserve provider completion, admission, authority or private checkpoint boundaries.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestInterruptAndFollowUpUseRetainedSessionWithoutReplay(`; S3. YAML execution regression; assertions preserve provider completion, admission, authority or private checkpoint boundaries.
- [internal/app/workflows_test.go](../../internal/app/workflows_test.go) — `func TestWorkflowAdmissionPreservesStartingStateAndRecoveryClaim(`; S4. Blocked native admission stays starting; answered recovery is claimed before its sole continuation is sent.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestInteractiveCompletionDoesNotWaitForCheckpointFlush(`; S1. Blocked workflow checkpoint cannot delay native completion/answer routing; closing drains the successful checkpoint.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestInteractiveCheckpointFailureReportsDiagnosticWithoutChangingCompletion(`; S1. Failed terminal persistence emits one sanitized originating-turn diagnostic after immediate native completion; an active follow-up is preserved and native work is not replayed.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestInteractiveCheckpointDiagnosticDoesNotDeadlockClose(`; S1. Shutdown releases execution ownership before a diagnostic callback waits on the application session lock; the callback exits after that lock is released.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestInteractiveSuccessAndNativeFailuresDoNotReportWorkflowError(`; S1, S3. Ordinary completed, interrupted and failed native turns retain one unchanged terminal notification without an extra workflow diagnostic.
- [internal/app/workflows_test.go](../../internal/app/workflows_test.go) — `func TestAnsweredContinuationPreservesStateOnStorageFailure(`; S4. Storage faults before dispatch preserve idle and an answered unclaimed recovery; a fault after acknowledgement preserves the active turn and diagnostic, with no erroneous idle event or repeated continuation after storage recovers.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestAdmissionCancellationWinsBeforeAcknowledgementTransfer(`; S1. A queued acknowledgement cannot report successful admission after cancellation takes ownership; output remains undecoded and workflow cancellation is synchronous.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestSuccessfulAdmissionOutlivesCallerUntilClientShutdown(`; S1. After successful admission, caller cancellation preserves observation until native interruption, client cancellation or Close; provider work starts only once.
- [internal/aiworkflows/client_test.go](../../internal/aiworkflows/client_test.go) — `func TestAcknowledgementTransferRejectsCanceledClient(`; S1. A canceled client lifetime cannot accept or decode a queued native acknowledgement as successful admission.
- [internal/app/retained_review_test.go](../../internal/app/retained_review_test.go) — `func TestReviewInterruptResumeJoinsOldExecution(`; S8. Interrupt persists its intent and joins an uncooperative child before returning, while overlapping Resume is rejected; later continuation reuses valid completed work.
- [internal/app/review_workflow_guards_test.go](../../internal/app/review_workflow_guards_test.go) — `func TestReviewTerminationCancelsConcurrentStartAndResume(`; S8. Concurrent retained Start/Resume and termination cancels the replacement execution while preserving pre-lock cancellation of old work.
- [internal/app/review_maintenance_test.go](../../internal/app/review_maintenance_test.go) — `func TestRetainedReviewContinuationHonorsPluginMaintenance(`; S8. Maintenance refuses retained continuation without accepting a message; an injected unavailable runtime remains blocked after maintenance, while a valid runtime resumes only the remaining reviewer.
- [internal/app/single_codex_test.go](../../internal/app/single_codex_test.go) — `func TestSingleCodexReviewRetainsSkillModeAndNativeSession(`; S1, S6. Single-session review follows up in the original native session after global settings switch back to workflow.
- [internal/app/single_codex_test.go](../../internal/app/single_codex_test.go) — `func TestSingleCodexStartupRetainsNativeSessionAfterSettingsSwitch(`; S6. After settings switch to workflow and manager restart, the native review preserves mode, thread, workspace, authority, skill snapshot and saved progress; repeated recovery resumes once without original-task replay or YAML execution.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestAcceptedReviewAnswerRecoversAppIntentBeforeEngineJournal(`; S8. A durable application answer/continuation committed before SDK acceptance recovers once after manager restart; waiting occupies no execution capacity and repeated recovery cannot replay child work.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestReviewAnswerContinuationCannotUndoInterrupt(`; S8. A later explicit interrupt wins the exact app-answer-commit/automatic-admission race; accepted answer remains retained for an explicit Resume.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestProductionReviewInterruptJoinsProviderProcess(`; S8. A fake production provider blocked before turn/start acknowledgement is reaped; missing native admission proof is reported as cleanup uncertainty rather than a successful joined interrupt.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestProductionCommandInterruptTerminatesSeparateReviewerSession(`; S8. Production native command termination kills and joins a separate TERM-ignoring reviewer session before Interrupt returns, preserving native process identity and environment controls.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestProductionModelInterruptTerminatesSeparateToolSession(`; S8. A production model turn owns a TERM-ignoring tool in a separate session; native turn interruption joins that tool before app-server reaping and parent interruption acknowledgement.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestReviewInterruptReportsUnverifiedChildCleanup(`; S8. Unverified child cleanup durably blocks repeated Resume, continue/resume messages and automatic continuation before and after manager restart, preserving the same execution, error, stop intent and checkpoints without additional child work.
- [internal/app/review_workers_test.go](../../internal/app/review_workers_test.go) — `func TestReviewVerifiedChildCleanupAllowsResume(`; S8. Verified child cleanup permits explicit Resume with and without manager restart; completed reviewer checkpoints are reused, preparation runs once and only one completion assessment occurs.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewWorkerAdmissionAndVerifiedReturn(`; S8. All four worker operations enter only after an execution/attempt/stage-bound admission is committed. Success, joined provider failure and verified cancellation clear it before return; ErrWorkerCleanup retains it.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewWorkerAdmissionWriteFailureStartsNoWorkers(`; S8. A failed pre-dispatch admission transaction invokes no worker. Later operations in that execution remain stopped; rollback leaves no invented admission.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewWorkerProofWriteFailureStopsLaterDispatch(`; S8. A failed shutdown-proof write for each worker operation retains the matching admission and prevents every later worker operation even after storage becomes writable.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewCleanupFinalizationFailureBlocksRestart(`; S8. Cleanup retention failure before the finalizer callback, at retained-state/event/UI-change writes or at deferred-constraint commit returns a typed cleanup/persistence error from Interrupt. Earlier admission survives rollback, disk reopen and restart; repeated replacement routes preserve state and dispatch no workers.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewAbruptWorkerLossRetainsAdmission(`; S8. A real helper process enters a wrapped fake worker only after its admission commits, then is killed without a worker return. Disk reopen/recovery preserves that exact unmatched admission and refuses Resume, operational messages and automatic answer continuation with zero replacement worker calls. This fixture does not exercise real-provider crash survival.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewSessionSavePreservesWorkerAdmission(`; S8. A Session save during admitted work updates its checkpoint while preserving the current stored worker admission and cleanup diagnostic; later finalization cannot erase that protection.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewVerifiedReturnWriteFailureStopsPreparation(`; S8. Preparation whose verified-return proof cannot be persisted retains its admission and incomplete Session. No material check, reviewer or completion worker starts, and Interrupt/continuation report the missing proof.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewWorkerProofCannotClearAnotherAdmission(`; S8. A worker return whose attempt identity no longer matches cannot clear a different retained admission.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewCommandDelegationRetainsWorkerAdmission(`; S8. CommandWorkers delegates a command reviewer while its single outer execution-bound worker admission is present.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewHistoricalWorkerIdentityRequiresFreshPreparation(`; S8. An old unwrapped worker identity cannot accept an answer or dispatch resumed workers. Historical gate/checkpoint/artifact metadata remains unchanged and fresh preparation is required.
- [internal/app/review_worker_admission_test.go](../../internal/app/review_worker_admission_test.go) — `func TestReviewOrchestratorExitCannotAcknowledgeAdmittedWorker(`; S8. An adapter that exits without waiting for an admitted worker cannot acknowledge verified shutdown: the finalizer checks the durable pending admission, Interrupt returns cleanup uncertainty, and replacement routes preserve the barrier.
- [internal/app/review_worker_edges_test.go](../../internal/app/review_worker_edges_test.go) — `func TestProductionCommandBlockedSendHasBoundedCleanup(`; S8. A fake provider stops reading after initialization while a valid 64 KiB command argument blocks request transmission. Interrupt returns within the bounded cleanup margin with ErrWorkerCleanup, reaps the provider and joins local RPC goroutines; durable admission/stop intent blocks Resume, continuation and all later worker dispatch without claiming native shutdown proof.
- [internal/app/review_interrupt_result_test.go](../../internal/app/review_interrupt_result_test.go) — `func TestReviewInterruptRetainsJoinedExecutionOutcome(`; S8. After verified worker return, a failed parent-finalization write remains bound to its original execution. The shared interrupt wait pauses before consuming that result while explicit Resume completes a replacement; the old waiter still returns the same typed persistence error and cannot alter replacement state or replay work.
- [web/tests/fixtures.ts](../../web/tests/fixtures.ts) — `export async function host(`; S1, S3, S4, S6, S7. Shared per-test mocked host and serial assembled-daemon helpers; scenario assertions remain mapped to their feature spec files.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.
- **S3** — `gap-flow-interact-agent-s3-01` · Unverified behavior · P2 · owner: user-flow-maintainer. The default fake-provider fixture now directly interrupts waiting work, asserts stale requests and verifies same-session message wake. Current installed-host real-provider interruption and assistive-technology behavior remain unverified. Next verification: Verify the same interruption, stale-question exclusion and message wake against the installed provider/host without replaying old answers; retain version-bound evidence.

<!-- /flow-references -->
