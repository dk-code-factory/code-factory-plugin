# Pages

Working, Issues, My PRs, Review PRs, Messages, Configuration and Events are navigation views. Conversation content belongs to the selected item.

<!-- specs:pages-0 -->
| Page | Purpose | Page-owned panes | Entry and exit |
| --- | --- | --- | --- |
| `PAGE-INBOX` | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. |
| `PAGE-AGENTS` | Displays P-WAITING-QUEUE. | Displays P-WAITING-QUEUE. | Displays P-WAITING-QUEUE. |
| `PAGE-SETTINGS` | Displays P-CONFIGURATION, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. | Displays P-CONFIGURATION, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. | Displays P-CONFIGURATION, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. |
| `PAGE-AGENT` | Displays P-ITEM-DETAIL, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. | Displays P-ITEM-DETAIL, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. | Displays P-ITEM-DETAIL, P-RUN-DETAIL, P-QUESTIONS, P-APPROVAL, P-ELICITATION. |
| `PAGE-MY-PRS` | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. |
| `PAGE-REVIEW-PRS` | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. |
| `PAGE-WORKING` | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. | Displays P-INBOX-QUERY, P-INBOX-LIST. |
| `PAGE-EVENTS` | Displays P-EVENTS. | Displays P-EVENTS. | Displays P-EVENTS. |
<!-- /specs:pages-0 -->

<!-- specs:pages-1 -->
| Dialog | Composition | Owning context | Dismissal and completion |
| --- | --- | --- | --- |
<!-- /specs:pages-1 -->

<!-- specs:pages-2 -->
| Page | Purpose and panes | Access boundary and transitions |
| --- | --- | --- |
| `PAGE-PACKAGE` | `P-PACKAGE-OVERVIEW`, `P-CONNECTION-GUIDE`. | Public, scoped application landing page. Downloads artifacts or guides the user into the external host. A package link is not an authenticated product session. |
| `PAGE-OWNER-CONSENT` | `P-OWNER-CONSENT`. | Private owner-only consent surface for one active request. Allow/Deny returns to the registered host callback. Expired or invalid requests remain error responses, not access to the embedded inbox. |
| `PAGE-LOCAL-TUNNEL-SETUP` | `P-LOCAL-TUNNEL-KEY`. | A successful private submission removes the form. An unknown save outcome locks the used submission and requires saved-state inspection or fresh setup authorization; the submitted key is never automatically replayed. Alternate transport remains disabled in the pilot. |
| `PAGE-BROWSER-LAB` | `P-BROWSER-LAB`. | Optional developer/operator utility with its own private session and isolated browser profile. Its screenshot/navigation controls are outside the product shell. |
<!-- /specs:pages-2 -->

<!-- specs:surfaces -->
| Surface | Context |
| --- | --- |
| `S-FACTORY` | Seven navigation tabs and one selected item conversation. Opening does not authorize work. |
| `S-SETTINGS` | Configuration chat can open as a dedicated MCP Apps resource. |
<!-- /specs:surfaces -->
