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
