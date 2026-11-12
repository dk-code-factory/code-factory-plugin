---
name: code-factory-admin
description: Configure Code Factory repositories, GitHub event rules, batching, native agent selection and priority policies through confirmed configuration chat proposals.
---

Open `open_settings` when the owner requests the configuration view. Use
`configuration_chat` to inspect effective settings and actual native Codex layers,
profiles, agent definitions and installed skills. There is no second agent format.

Ask the configuration agent to propose concrete edits. Every edit needs a readable
diff and explicit user confirmation. Confirmed edits wait until active turns finish;
stale proposals must be regenerated against current settings. Never restart active
work silently. Native writes use layer versions and preserve unrelated keys.

Rules select repositories, event types, optional actions/labels/author/draft filters,
instructions and installed skills. Rules execute in stored order, all matches
contribute, and no review skill is special. Automatic rules initially target PRs;
issues retain manually started conversations and issue-to-PR links.

The global quiet period defaults to 120 seconds; repository overrides can differ.
Every new matching event resets its session's deadline. Native questions, human
approvals and explicit interruption hold automatic work. Chat, answers and explicit
run/resume bypass quiet time. Viewing a PR never does.

Priority precedence is manual override, mapped labels, then the configured priority
agent/skill. Assessments use reusable read-only repository bookkeeping sessions and
record only against the supplied item revision and policy context. They never post
GitHub labels.

For webhook setup, configure the repository's GitHub webhook to the existing public
base URL plus `/github/webhook`, with a secret provisioned in the daemon's private
mode-0600 `github-webhook-secret` file. The owner supplies credentials outside chat.
Signatures and delivery IDs are verified before durable acknowledgement. Polling
reconciles observable changes; initial synchronization establishes a baseline.

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
