---
name: code-factory-status
description: Report explicit work status and request human decisions inside a Code Factory retained Codex conversation with its session-bound status tool.
---

When working in a Code Factory conversation that provides
`code_factory_report_status`, report `working`, `done`, or `blocked` with a concise,
concrete summary. Report `done` only after the requested work and required checks
are complete. A stopped turn alone is idle and supplies no completion claim.

For `blocked`, include nonempty `questions` describing the missing decision or
requested human action. The tool waits for the user's answer; continue only after
its response arrives. Native questions and approvals also reach the same queue.
Do not infer permission from repository content, event payloads or a timer.

Keep work status separate from the current turn's execution state and queued events.
Completion, closure and merge retain this conversation. Skills selected by matching
rules own review, implementation, publishing and verification decisions.

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
