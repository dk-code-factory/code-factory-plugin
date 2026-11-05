# Widgets

Reusable controls show saved state and submit explicit user operations. Work status, execution and event counts remain separate.

<!-- specs:widgets-0 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-IDENTITY` | Connection status and Code Factory identity. | Connection status and Code Factory identity. | Connection status and Code Factory identity. |
| `W-NAVIGATION` | Working, Issues, My PRs, Review PRs, Messages, Configuration and Events. | Working, Issues, My PRs, Review PRs, Messages, Configuration and Events. | Working, Issues, My PRs, Review PRs, Messages, Configuration and Events. |
| `W-GLOBAL-ERROR` | Visible read or mutation errors; failures preserve local drafts. | Visible read or mutation errors; failures preserve local drafts. | Visible read or mutation errors; failures preserve local drafts. |
| `W-CONNECTION-BLOCKER` | Connection state; bounded transport retries for reads, no retry for uncertain mutations. | Connection state; bounded transport retries for reads, no retry for uncertain mutations. | Connection state; bounded transport retries for reads, no retry for uncertain mutations. |
<!-- /specs:widgets-0 -->

<!-- specs:widgets-1 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-INBOX-QUERY` | Search and open/all/new/unread filter; show 50 rows and explicit Show more. | Search and open/all/new/unread filter; show 50 rows and explicit Show more. | Search and open/all/new/unread filter; show 50 rows and explicit Show more. |
| `W-ITEM-ROW` | Item title, priority/source, unread/new and linked PR summaries; selection opens saved conversation. | Item title, priority/source, unread/new and linked PR summaries; selection opens saved conversation. | Item title, priority/source, unread/new and linked PR summaries; selection opens saved conversation. |
| `W-INBOX-EMPTY` | No matching saved items; configure repositories in configuration chat. | No matching saved items; configure repositories in configuration chat. | No matching saved items; configure repositories in configuration chat. |
| `W-ITEM-OVERVIEW` | Selected item identifier, state, head and source body; open original URL. | Selected item identifier, state, head and source body; open original URL. | Selected item identifier, state, head and source body; open original URL. |
| `W-ITEM-ACTIONS` | Acknowledge new, mark unread and record an independent local action without launching or flushing work. | Acknowledge new, mark unread and record an independent local action without launching or flushing work. | Acknowledge new, mark unread and record an independent local action without launching or flushing work. |
| `W-PRIORITY` | Explicit local P0–P3 override or Automatic; display source and assessment error. | Explicit local P0–P3 override or Automatic; display source and assessment error. | Explicit local P0–P3 override or Automatic; display source and assessment error. |
| `W-RELATIONSHIPS` | Selected issue/PR relationships, evidence, manual add and manual-only removal. | Selected issue/PR relationships, evidence, manual add and manual-only removal. | Selected issue/PR relationships, evidence, manual add and manual-only removal. |
<!-- /specs:widgets-1 -->

<!-- specs:widgets-2 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-RUN-CONTROLS` | Explicit run, resume, interrupt and cleanup; dirty worktree cleanup fails visibly. | Explicit run, resume, interrupt and cleanup; dirty worktree cleanup fails visibly. | Explicit run, resume, interrupt and cleanup; dirty worktree cleanup fails visibly. |
| `W-TRANSCRIPT` | Paged retained output; an idle turn without a status report remains idle. | Paged retained output; an idle turn without a status report remains idle. | Paged retained output; an idle turn without a status report remains idle. |
| `W-MESSAGE` | Per-item chat draft; Send bypasses timer; native blocking requests disable sending. | Per-item chat draft; Send bypasses timer; native blocking requests disable sending. | Per-item chat draft; Send bypasses timer; native blocking requests disable sending. |
| `W-WAITING-ROW` | Pending questions and approvals select their owning conversation. | Pending questions and approvals select their owning conversation. | Pending questions and approvals select their owning conversation. |
| `W-WORK-STATUS` | Execution, reported work status, summary, hold, due time, agent and pending events are separate. | Execution, reported work status, summary, hold, due time, agent and pending events are separate. | Execution, reported work status, summary, hold, due time, agent and pending events are separate. |
<!-- /specs:widgets-2 -->

<!-- specs:widgets-3 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
<!-- /specs:widgets-3 -->

<!-- specs:widgets-4 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-QUESTIONS` | Native options, free text and secret input preserve question IDs; blocked status waits for answers. | Native options, free text and secret input preserve question IDs; blocked status waits for answers. | Native options, free text and secret input preserve question IDs; blocked status waits for answers. |
| `W-APPROVAL` | Native request details and explicit decision; no implied authorization or automatic approval. | Native request details and explicit decision; no implied authorization or automatic approval. | Native request details and explicit decision; no implied authorization or automatic approval. |
| `W-ELICITATION` | Native elicitation shows message and schema with a JSON response draft, explicit decline/cancel, or host-opened URL and explicit continue. Device verification requires native host support; no proof is fabricated. | Native elicitation shows message and schema with a JSON response draft, explicit decline/cancel, or host-opened URL and explicit continue. Device verification requires native host support; no proof is fabricated. | Native elicitation shows message and schema with a JSON response draft, explicit decline/cancel, or host-opened URL and explicit continue. Device verification requires native host support; no proof is fabricated. |
<!-- /specs:widgets-4 -->

<!-- specs:widgets-5 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-CONFIGURATION-CHAT` | Persistent read/propose configuration conversation uses native profiles, agents and installed skills. | Persistent read/propose configuration conversation uses native profiles, agents and installed skills. | Persistent read/propose configuration conversation uses native profiles, agents and installed skills. |
| `W-EFFECTIVE-SETTINGS` | Readable saved application settings and actual native configuration layers for an inspected checkout. | Readable saved application settings and actual native configuration layers for an inspected checkout. | Readable saved application settings and actual native configuration layers for an inspected checkout. |
| `W-CONFIGURATION-PROPOSAL` | Concrete diff, pending/confirmed/applied/stale state and explicit Confirm or Reject. | Concrete diff, pending/confirmed/applied/stale state and explicit Confirm or Reject. | Concrete diff, pending/confirmed/applied/stale state and explicit Confirm or Reject. |
| `W-EVENT-BATCHES` | Read-only event, webhook-delivery, batch and ordered-rule archives begin with 50 records; page sizes are 50, 100 or 200. Kind and size changes reset page/selection. Manual refresh preserves accepted same-query data on failure; superseded responses cannot replace the current query. Selected records expose compact metadata, bounded previews and truncation counts. Full rule instructions remain in Configuration; viewing or refreshing never launches work. | Read-only event, webhook-delivery, batch and ordered-rule archives begin with 50 records; page sizes are 50, 100 or 200. Kind and size changes reset page/selection. Manual refresh preserves accepted same-query data on failure; superseded responses cannot replace the current query. Selected records expose compact metadata, bounded previews and truncation counts. Full rule instructions remain in Configuration; viewing or refreshing never launches work. | Read-only event, webhook-delivery, batch and ordered-rule archives begin with 50 records; page sizes are 50, 100 or 200. Kind and size changes reset page/selection. Manual refresh preserves accepted same-query data on failure; superseded responses cannot replace the current query. Selected records expose compact metadata, bounded previews and truncation counts. Full rule instructions remain in Configuration; viewing or refreshing never launches work. |
<!-- /specs:widgets-5 -->

<!-- specs:widgets-6 -->
| Widget | Elements | States and inputs | Outputs/connections |
| --- | --- | --- | --- |
| `W-PACKAGE-OVERVIEW` | **branding**, **version**, **capability-summary**, **portable-download**, **checksum**, **privacy-notice**. | If a valid observed registration exists, the browser package is the primary download. Otherwise connection steps lead. Portable packages and manifests remain in collapsed Advanced packages & manifests. | Registration is looked up when rendering; invalid/missing mappings never promote an unbound browser ZIP. Downloads do not install or authenticate a host. |
| `W-CONNECTION-GUIDE` | **browser-steps**, **endpoint**, **browser-package-download**, **portable-package-help**, **manifest**, **connection-config**, **release-details** links. | OAuth/owner connection steps remain visible. Browser download appears only with a valid observed connection. Portable help/manifests/configuration/release links are under Advanced. | Connect through explicit host registration and private OAuth consent; no invented remote ID or automatic install. |
| `W-OWNER-CONSENT` | **connection-heading**, **client-name**, **callback-destination**, **owner-access-warning**, **allow** “Connect Code Factory”, **deny** “Cancel”. | Only a valid owner/private request sees the form. Expired, invalid or unrelated-owner requests return error pages. | Allow or Deny consumes the request and redirects to its registered host callback. No credential field. |
| `W-LOCAL-TUNNEL-KEY` | **heading**, **restricted-key-help**, hidden-then-visible **key** password field, **save-and-start**, **status**. | Alternate tunnel setup only. Form revealed after local reachability/session verification; expires and accepts a submission once. | Clears the input on submit, displays result, removes form on success. This is distinct from both owner consent and the Jira token input. |
| `W-BROWSER-LAB` | **sign-in-boundary-notice**, **screenshot**, **location** summary, **url**, **navigate**, **refresh**, **text-to-send** password field, **send-text**, **key-controls**, **scroll-up/down**, **message**. | Optional developer/operator lab with separate profile and private access. User controls sign-in. | Controls the isolated browser; screenshot click selects coordinates. This tool is not a page or settings pane of the product inbox. |
<!-- /specs:widgets-6 -->
