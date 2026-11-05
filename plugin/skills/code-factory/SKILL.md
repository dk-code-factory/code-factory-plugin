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
