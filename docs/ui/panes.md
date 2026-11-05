# Panes

Panes compose reusable widgets for the selected item, configuration conversation or event inspection.

<!-- specs:panes-0 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-IDENTITY` | Contains W-IDENTITY. | Contains W-IDENTITY. |
| `P-NAVIGATION` | Contains W-NAVIGATION. | Contains W-NAVIGATION. |
| `P-FEEDBACK` | Contains W-GLOBAL-ERROR, W-CONNECTION-BLOCKER. | Contains W-GLOBAL-ERROR, W-CONNECTION-BLOCKER. |
| `P-WAITING-QUEUE` | Contains W-WAITING-ROW. | Contains W-WAITING-ROW. |
| `P-CONFIGURATION` | Contains W-CONFIGURATION-CHAT, W-EFFECTIVE-SETTINGS, W-CONFIGURATION-PROPOSAL. | Contains W-CONFIGURATION-CHAT, W-EFFECTIVE-SETTINGS, W-CONFIGURATION-PROPOSAL. |
| `P-EVENTS` | Contains W-EVENT-BATCHES. | Contains W-EVENT-BATCHES. |
<!-- /specs:panes-0 -->

<!-- specs:panes-1 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-INBOX-QUERY` | Contains W-INBOX-QUERY. | Contains W-INBOX-QUERY. |
| `P-INBOX-LIST` | Contains W-ITEM-ROW, W-INBOX-EMPTY. | Contains W-ITEM-ROW, W-INBOX-EMPTY. |
| `P-ITEM-DETAIL` | Contains W-ITEM-OVERVIEW, W-ITEM-ACTIONS, W-PRIORITY, W-RELATIONSHIPS. | Contains W-ITEM-OVERVIEW, W-ITEM-ACTIONS, W-PRIORITY, W-RELATIONSHIPS. |
<!-- /specs:panes-1 -->

<!-- specs:panes-2 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-RUN-DETAIL` | Contains W-RUN-CONTROLS, W-WORK-STATUS, W-TRANSCRIPT, W-MESSAGE. | Contains W-RUN-CONTROLS, W-WORK-STATUS, W-TRANSCRIPT, W-MESSAGE. |
<!-- /specs:panes-2 -->

<!-- specs:panes-3 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
<!-- /specs:panes-3 -->

<!-- specs:panes-4 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
<!-- /specs:panes-4 -->

<!-- specs:panes-5 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-QUESTIONS` | Contains W-QUESTIONS. | Contains W-QUESTIONS. |
| `P-APPROVAL` | Contains W-APPROVAL. | Contains W-APPROVAL. |
| `P-ELICITATION` | Native elicitation shows message and schema with a JSON response draft, explicit decline/cancel, or host-opened URL and explicit continue. Device verification requires native host support; no proof is fabricated. | Native elicitation shows message and schema with a JSON response draft, explicit decline/cancel, or host-opened URL and explicit continue. Device verification requires native host support; no proof is fabricated. |
<!-- /specs:panes-5 -->

<!-- specs:panes-6 -->
| Pane | Widgets | Shared context and placement |
| --- | --- | --- |
| `P-PACKAGE-OVERVIEW` | `W-PACKAGE-OVERVIEW` | Branding, version, portable download/checksum and privacy text on the public package page. |
| `P-CONNECTION-GUIDE` | `W-CONNECTION-GUIDE` | Host setup guidance, linked browser-package download and artifact metadata links on that same page. |
| `P-OWNER-CONSENT` | `W-OWNER-CONSENT` | One private owner connection request. It is not an embedded Settings pane. |
| `P-LOCAL-TUNNEL-KEY` | `W-LOCAL-TUNNEL-KEY` | One local alternate-tunnel setup session. Disabled/unused in the current selected pilot transport. |
| `P-BROWSER-LAB` | `W-BROWSER-LAB` | Optional private developer browser controller, independent of product navigation. |
<!-- /specs:panes-6 -->
