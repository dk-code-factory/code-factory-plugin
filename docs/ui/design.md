# Compact, single-page UI design

Owner workflow and layout contract, updated 2026-10-21.

## Navigation

Use five compact tabs: **Working**, **Issues**, **My PRs**, **Review PRs**, **Messages**. Issues remains the initial view.
Settings opens as its own Code Factory view in chat through `open_settings`.
No branding block, sidebar, split view, permanent footer or empty detail pane.
Show one full-width page at a time. Back restores its originating tab, query,
filter, scroll position and row focus. Queries remain independent between tabs.

## Work lists

- Working contains items with a started pipeline, including idle/interrupted/failed sessions. A PR leaves when closed or merged; an issue leaves when closed or any linked implementation PR merges. A reference alone cannot complete work. Observed source closure or merge permanently terminates its sessions and retires pending questions, retaining history and workspaces. A linked implementation PR merge completes the issue's Working membership without terminating the issue's sessions.
- Issues defaults to **open**, including linked PR summaries. **pending** remains the optional open-without-PR filter. This tab always contains issues only.
- My PRs defaults to open PRs authored by the authenticated GitHub account.
- Review PRs defaults to open PRs requesting that account or one of its teams.
  Display identity or team-membership failures; do not guess the account.
- Sort by priority P0–P3, with unknown priority last; break ties by latest update.
- Put the priority badge left of the title, beside its first line when it wraps.
  Keep the unread dot beside the title. Put the repository/number link and other
  state indicators below. The identifier links to the original issue/PR and is
  displayed once, without an extra GitHub label or icon.
- Keep read, New and action history independent. Source-link activation does not
  select or mark an item read. No matching items offers Clear filters.

## Item → conversation

Clicking an item opens its **Agent** page immediately, with item title,
identifier, a source-description summary and chat in one content column.
There is no intermediate item-only page or Open agent step.

Open the newest retained unfinished conversation when one exists. Otherwise
show an editable first message with a selectable pipeline. Default to Implementation for issues/My PRs and PR review for Review PRs; require selection if the default is unavailable. Changing a pipeline preserves edited text and invalidates an old proposal.
Show profile, mode, permissions and publishing authority before **Start agent**.
Opening the page never starts or prepares work. Start is explicit: prepare the
visible message and profile choices, then start that retained proposal. If the
resolved mode/authority changes, show the updated values and require Start again.
An uncertain Start checks the same run before permitting retry; preserve that
state when leaving the item. A late response must not navigate away from another
item. Keep first-message drafts per item and follow-up drafts per run while open.

Keep full description, priority editing, actions, relationships and history under
**Item details** below the chat. **New run** there opens the optional prepared
review, with Edit and Advanced customization. It remains a separate proposal.
Show agent progress/status updates and final replies, your messages, readable
errors and pending questions. Keep command output, plans, raw diffs, provider
summaries and other technical activity in the existing collapsed **Details**
section alongside workspace paths and cleanup. New human-facing messages remain
visible while technical updates leave Details closed. Activity is shown as the issue/PR’s aggregate work status in list metadata and its conversation header; an older blocked session outranks newer idle work. PR lifecycle, approval and CI remain separate status values. Linked implementation and reference PRs show their own summaries above the conversation. There is no Resume button: sending a message to inactive retained work restores the same Codex session and uses that message as the continuation turn. Interrupt remains in Details; cleanup retains dirty-workspace protection. A failed send keeps its draft. No message or action is replayed automatically.

## Messages

List unanswered questions and approvals with owning item/session context. Include live RPC questions, structured questions on agent messages, and recovered textual asks. Nonblocking questions say the agent continues. Exclude answered, stale, cleaned and orphaned requests. A lost live RPC ID is never reused; retained message/text questions can explicitly restore the original thread. Lost approvals require a fresh native authorization request.

A small read-only helper examines a stopped reply without a native question. Only a confirmed unanswered question creates attention; completion, failed checks and uncertain classification do not. A failed classification asks the retained agent to clarify whether input is needed. Clarification and native-form repair share at most two durably claimed attempts, including setup failures and restart. Active clarification shows its shared count and Take over session without an attention badge, and disables ordinary Send even with a filled draft. Unresolved classification stops progress without creating attention and permits explicit owner messaging. Native forms take precedence; a confirmed textual fallback preserves its original blocking or continuing status and offers Open session console. No reply needed dismisses that local ask without sending a task turn or authorizing an action. Persist attempt counts and stages, bound helper/check deadlines, and never replay uncertain sends. Explicit takeover cancels further repair and queued continuation. After answering, restore the selected task mode explicitly.

The session console is an alternative rendering of the same journal: messages, commands/output, plans, diffs and errors, with shared answer/message/interrupt controls. It retains drafts and history limits and creates neither another CLI process nor a terminal transport.

## Density and verification

Use one column, compact controls, 8–12px page padding and 4–8px gaps in both
light and dark themes. Hide routine connection/sync success text; keep failures
visible. Recover transient read failures automatically and clear their own feedback
after success; preserve drafts, existing output and uncertain mutation errors.
Never automatically repeat Start, Send, answers, saves or other mutations. Keep keyboard focus usable through page replacement and dialogs.
Verify the five queues, priority placement, direct chat, draft isolation, explicit
Start/recovery, request ownership, source links and narrow layouts. Fixture
verification does not establish full installed-host or accessibility acceptance.
