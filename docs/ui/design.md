# Compact, single-page UI design

Owner workflow and layout contract, updated 2026-10-21.

## Navigation

Use four compact tabs: **Issues**, **My PRs**, **Review PRs**, **Messages**.
Settings opens as its own Code Factory view in chat through `open_settings`.
No branding block, sidebar, split view, permanent footer or empty detail pane.
Show one full-width page at a time. Back restores its originating tab, query,
filter, scroll position and row focus. Queries remain independent between tabs.

## Work lists

- Issues opens by default with **pending**: open issues with no linked PR,
  including relationships discovered from either direction. Other filters can
  include linked or closed issues. This tab always contains issues only.
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
show an editable first message prefilled from the default profile and item data.
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
visible while technical updates leave Details closed. Activity is shown only as the issue/PR’s work status in list metadata and its conversation header. There is no Resume button: sending a message to inactive retained work restores the same Codex session and uses that message as the continuation turn. Interrupt remains in Details; cleanup retains dirty-workspace protection. A failed send keeps its draft. No message or action is replayed automatically.

## Messages

List only pending questions and approvals from agents blocked on human input, with the owning issue/PR, request type and question text. Exclude nonblocking, answered, stale, orphaned or disconnected requests. Count only those blocking requests; hide zero.
Selecting a request opens its owning conversation and answer dialog. Answering
returns to that conversation and removes only the resolved request from the list.
There is no All agents section in Messages. Retained conversations are under the owning item’s **Item details → Work history**, with **Draft launches** inside.
No requests shows “No messages waiting for you.”

## Density and verification

Use one column, compact controls, 8–12px page padding and 4–8px gaps in both
light and dark themes. Hide routine connection/sync success text; keep failures
visible. Recover transient read failures automatically and clear their own feedback
after success; preserve drafts, existing output and uncertain mutation errors.
Never automatically repeat Start, Send, answers, saves or other mutations. Keep keyboard focus usable through page replacement and dialogs.
Verify the four queues, priority placement, direct chat, draft isolation, explicit
Start/recovery, request ownership, source links and narrow layouts. Fixture
verification does not establish full installed-host or accessibility acceptance.
