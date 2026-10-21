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
| `P-IDENTITY` | `W-IDENTITY` | Product identity and connection health; top of the embedded shell. |
| `P-NAVIGATION` | `W-NAVIGATION` | Current page and pending requests; below identity, independent of page body. |
| `P-FEEDBACK` | `W-GLOBAL-ERROR`, `W-CONNECTION-BLOCKER` | Shell-wide operation errors and initial connection state. Blocker replaces the page body until initial data is available. |
| `P-CONTINUITY` | `W-CONTINUITY-NOTICE` | Footer shared by all embedded pages, including initial loading. |
<!-- /specs:panes-0 -->

## Inbox panes

<!-- specs:panes-1 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-INBOX-QUERY` | `W-INBOX-QUERY`, `W-SYNC-HEALTH` | Inbox search/filter and configured repositories; above the list/detail split. |
| `P-INBOX-LIST` | `W-ITEM-ROW` repeated by item ID, or `W-INBOX-EMPTY` | Filtered synchronized inbox. Selection identifies one detail context; filtering need not clear selection. |
| `P-ITEM-DETAIL` | `W-ITEM-OVERVIEW`, `W-ITEM-ACTIONS`, `W-PRIORITY`, `W-RELATIONSHIPS`, `W-TICKET-CONTEXT`, `W-PR-EVIDENCE`, `W-ITEM-HISTORY` | One selected item ID and its displayed revision. Prompt/loading replaces the other widgets until matching detail is available. Detail loads on item-ID or revision changes and remount; refreshing the shared snapshot alone does not reload it. |
<!-- /specs:panes-1 -->

## Agent panes

<!-- specs:panes-2 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-RUN-LIST` | `W-RUN-ROW` repeated by run ID, or `W-RUN-EMPTY` | All retained runs except prepared runs; selection is independent of inbox selection. |
| `P-RUN-DETAIL` | `W-RUN-CONTROLS`, `W-ATTENTION-NOTICE` repeated by request ID, `W-TRANSCRIPT`, `W-MESSAGE`; or `W-RUN-EMPTY` | One selected run, its pending attention and transcript cursor. Selection of a different run resets this pane's local transcript/message state. |
<!-- /specs:panes-2 -->

## Settings panes

<!-- specs:panes-3 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-REPOSITORIES` | `W-REPOSITORY-SETTINGS` | Repository drafts and configured-repository summary. Discovery updates the skills available to other panes. |
| `P-POLICIES` | `W-POLICY-SETTINGS`, `W-JIRA-CREDENTIAL` | Ordinary settings and private credential submission are separate forms in the same pane. Saving policy does not submit the credential. |
| `P-PROFILES` | `W-PROFILE-SETTINGS` | One editable profile and its default skills; connects to future launch preparation. |
| `P-SKILLS` | `W-SKILL-SETTINGS` | One managed skill draft and the currently known managed skills. |
| `P-DIAGNOSTICS` | `W-DIAGNOSTICS` | Explicitly requested health results and provider authorization guidance. |
<!-- /specs:panes-3 -->

These five panes share the Settings page's local draft lifetime. They are a grid
of sections, not separate routed pages or sequential wizard steps.

## Launch dialog panes

<!-- specs:panes-4 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-LAUNCH-FRAME` | `W-DIALOG-FRAME` | Launch title, originating item and close control; common to both phases. |
| `P-LAUNCH-EDITOR` | `W-LAUNCH-EDITOR` | Selected item, profile and editable task draft; shown before a prepared snapshot is selected. |
| `P-LAUNCH-REVIEW` | `W-LAUNCH-REVIEW` | One durable prepared run; replaces the editor after successful preparation. |
<!-- /specs:panes-4 -->

The editor and review panes are alternatives, not simultaneously editable panes.
Closing the dialog removes their local state. A prepared record can outlive that
dialog even though it is omitted from the current Agents list.

## Attention dialog panes

<!-- specs:panes-5 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-ATTENTION-FRAME` | `W-DIALOG-FRAME` | Request identity, blocking/continuing explanation, close, inline submission error and Open agent chat. |
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

- Inbox and Agents use list/detail side-by-side on larger widths. At widths up to
  700px they stack; lists become vertically bounded and scrollable. These remain
  the same panes, not new mobile pages.
- Settings uses a responsive section grid and becomes one column on narrow
  widths. Each pane keeps its own form semantics.
- Dialogs overlay the page, constrain their height and allow internal scrolling.
  Their current code declares modal roles but does not establish keyboard focus
  trapping/restoration, Escape handling, or mutual exclusion of both dialogs.
- Shared connection and error feedback must remain distinguishable from an
  individual run's failure and an attention submission error. They have different
  owners and recovery actions.
