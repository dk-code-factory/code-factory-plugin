# Pane specification

[Widgets](widgets.md) · [Pages](pages.md) · [Connections](connections.md) ·
[Implementation/tests](references.md)

Every `W-*` below resolves to the [widget inventory](widgets.md). A pane may have
several widgets, repeated widget instances or an alternative loading/empty state.
The list expresses logical composition, not required component boundaries.

## Shared shell panes

<!-- specs:panes-0 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-IDENTITY` | `W-IDENTITY` connection status within navigation. | Connection feedback for each independently opened surface. Normal success is screen-reader-only; failures remain visible. |
| `P-NAVIGATION` | `W-NAVIGATION` | Current page and pending requests; below identity, independent of page body. |
| `P-FEEDBACK` | `W-GLOBAL-ERROR`, `W-CONNECTION-BLOCKER` | Shell-wide operation errors and initial connection state. Blocker replaces the page body until initial data is available. |
| `P-WAITING-QUEUE` | W-WAITING-ROW | Conditional content in the single active page. |
| `P-NEW-CHAT` | W-NEW-CHAT | Conditional content in the single active page. |
<!-- /specs:panes-0 -->

## Inbox panes

<!-- specs:panes-1 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-INBOX-QUERY` | `W-INBOX-QUERY`, `W-SYNC-HEALTH` | Compact search/filter row on the list page; only repository errors accompany it. |
| `P-INBOX-LIST` | `W-ITEM-ROW` repeated by item ID, or `W-INBOX-EMPTY` | Full-width filtered list, exclusively on PAGE-INBOX. Selection navigates away to one item. |
| `P-ITEM-DETAIL` | `W-ITEM-OVERVIEW`, `W-ITEM-ACTIONS`, `W-PRIORITY`, `W-RELATIONSHIPS`, `W-TICKET-CONTEXT`, `W-PR-EVIDENCE`, `W-ITEM-HISTORY`; contextual Back-only `W-NAVIGATION` instance. | Item summary above chat and collapsed triage/details below it on PAGE-AGENT. |
<!-- /specs:panes-1 -->

## Agent panes

<!-- specs:panes-2 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-RUN-LIST` | `W-RUN-ROW` repeated by run ID, or `W-RUN-EMPTY` | Collapsed Work history inside the selected item’s Item details, containing only that item’s retained conversations and nested Draft launches. |
| `P-RUN-DETAIL` | `W-RUN-CONTROLS`, `W-ATTENTION-NOTICE` repeated by request ID, `W-TRANSCRIPT`, `W-MESSAGE`; or `W-RUN-EMPTY`; contextual Back-only `W-NAVIGATION` instance. | Conversation content on PAGE-AGENT below the item summary, with origin-specific Back and per-run follow-up drafts. |
<!-- /specs:panes-2 -->

## Settings panes

<!-- specs:panes-3 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-REPOSITORIES` | `W-REPOSITORY-SETTINGS` | Repository drafts and configured-repository summary. Discovery updates the skills available to other panes. |
| `P-POLICIES` | `W-POLICY-SETTINGS` | Collapsed priority and notification form with ordinary label/priority rows and local feedback. |
| `P-PROFILES` | `W-PROFILE-SETTINGS` | One editable profile and its default skills; connects to future launch preparation. |
| `P-SKILLS` | `W-SKILL-SETTINGS` | One managed skill draft and the currently known managed skills. |
| `P-DIAGNOSTICS` | `W-DIAGNOSTICS` | Explicitly requested health results and provider authorization guidance. |
<!-- /specs:panes-3 -->

These five relevant panes share the Settings page's local draft lifetime. They are collapsible sections with Repositories initially open; saves remain in Settings with independent form feedback.

## Launch dialog panes

<!-- specs:panes-4 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-LAUNCH-FRAME` | `W-DIALOG-FRAME` | Launch title, originating item and close control; common to both phases. |
| `P-LAUNCH-EDITOR` | `W-LAUNCH-EDITOR` | Optional Edit view of a selected item and profile with Advanced customization. |
| `P-LAUNCH-REVIEW` | `W-LAUNCH-REVIEW` | Default entry into launch; one durable snapshot, also reopened from Draft launches. |
<!-- /specs:panes-4 -->

The editor and review panes are alternatives, not simultaneously editable panes.
Closing the dialog removes their local state. A prepared record outlives the dialog and reopens from Draft launches without another preparation.

## Attention dialog panes

<!-- specs:panes-5 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-ATTENTION-FRAME` | `W-DIALOG-FRAME` | Owning item title/profile, blocking explanation, close, local error and Open agent chat. |
| `P-QUESTIONS` | `W-QUESTIONS` | Used when the request supplies questions; all question answers are submitted together. |
| `P-APPROVAL` | `W-APPROVAL` | Nonquestion command/file/permission approval with original request detail and available decisions. |
| `P-ELICITATION` | `W-ELICITATION` plus request-details display from `W-APPROVAL` | Nonquestion elicitation: supported form, external authorization URL or unsupported-native-host notice. |
<!-- /specs:panes-5 -->

Questions take precedence when a questions collection is present. Otherwise the
request type selects permission approval, elicitation or ordinary decisions. These
are variants within the same dialog; there is no approval navigation page.

## Auxiliary surface panes

<!-- specs:panes-6 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-PACKAGE-OVERVIEW` | `W-PACKAGE-OVERVIEW` | Branding, version, portable download/checksum and privacy text on the public package page. |
| `P-CONNECTION-GUIDE` | `W-CONNECTION-GUIDE` | Host setup guidance, linked browser-package download and artifact metadata links on that same page. |
| `P-OWNER-CONSENT` | `W-OWNER-CONSENT` | One private owner connection request. It is not an embedded Settings pane. |
| `P-LOCAL-TUNNEL-KEY` | `W-LOCAL-TUNNEL-KEY` | One local alternate-tunnel setup session. Disabled/unused in the current selected pilot transport. |
| `P-BROWSER-LAB` | `W-BROWSER-LAB` | Optional private developer browser controller, independent of product navigation. |
<!-- /specs:panes-6 -->

## Layout and access constraints

- Work tabs display full-width lists; selection replaces the list with one
  conversation page at every width. No side-by-side or stacked list/detail layout.
- Settings is one column of collapsed sections with Repositories open initially.
- Dialogs overlay the current page, constrain height and scroll internally.
  Local keyboard fixtures cover focus containment, Escape and opener restoration;
  complete installed-host accessibility remains separately unverified.
- Shared connection and error feedback must remain distinguishable from an
  individual run's failure and an attention submission error. They have different
  owners and recovery actions.
