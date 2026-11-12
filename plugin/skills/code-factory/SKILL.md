---
name: code-factory
description: Inspect Code Factory GitHub work and retained Codex conversations, send authorized chat, and answer exact pending agent questions.
---

Use the connected Code Factory tools with their host namespace. Open the dashboard
only when requested. Opening and reading never starts work.

- Read `factory_state` for the inbox, canonical sessions, pending-event counts,
  execution state and separately reported work status. `idle` does not mean done.
- Read `factory_item` for the canonical item ID and local relationships.
- Use `session_chat(itemId, text)` for authorized work or steering. Each item keeps
  one chosen native agent/profile, retained thread and worktree. Chat flushes queued
  events immediately; automatic work waits for the configured quiet period.
- Read `session_questions`, show the exact questions/options, and call
  `answer_session_question` only with the user's answer and exact request ID.
  Native blocking requests must be answered before chat. Never invent approval.
- Use `session_control` for explicit run, resume, interrupt or cleanup. Interrupt
  holds automatic work. Cleanup refuses dirty work and retains conversation history.
- Inspect `factory_events` for durable observations, ordered rule actions, batches
  and delivery state. Uncertain sends require retained-history reconciliation;
  never replay them blindly or treat queue acceptance as task completion.

All matching rules contribute instructions and arbitrary installed skills. Those
skills own reviewing, implementation, verification and publishing within native
Codex permissions and the user's authority. Code Factory supplies no review pipeline,
reviewer whitelist, automatic publication engine or question classifier.

Use `configuration_chat` for settings changes. The configuration agent reads and
proposes; the owner explicitly confirms a concrete diff in the configuration view.
Do not confirm a proposal on the user's behalf without an explicit confirmation.
Native configuration/authentication and unrelated owner settings remain native.

## API version 2

Start with `open_factory` or `open_settings` for the daemon `instanceId` and UI
resource. Opening returns bootstrap only. Read tools return `pending` until their
saved-state dependencies are current, then `ready` with `value`. Poll pending
reads every 250 ms while visible. Read every versioned `document_chunk` for a
large JSON document; decode base64 and reassemble the exact version without
truncating source, settings or question payloads.

Every submitted operation requires a fresh client-generated `requestId` and the
bootstrap `instanceId`. Chat, controls, answers, local edits, configuration,
synchronization, diagnostics and native inspection return an operation receipt.
Poll `operation_status` with the original identities. Queued/running means accepted,
never delivered or completed. Only `succeeded` establishes completion. Reusing an
identity returns the original operation. After a lost response query status; never
resubmit under a fresh ID. `uncertain` or `lost` requires saved-state inspection
and an explicit human decision. A daemon restart never replays mutations.
