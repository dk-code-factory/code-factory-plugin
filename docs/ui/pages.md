# Pages and dialogs

[Widgets](widgets.md) · [Panes](panes.md) · [Connections](connections.md) ·
[Implementation/tests](references.md)

Page names below identify task surfaces, not framework routes. All embedded pages
use the owning surface below with compact, conditional feedback.

## Independent application surfaces

The inbox/agent surface and Settings open independently in chat, using the same
protected connection. Settings has its own document and entry tool; neither view
contains navigation to the other. Both may remain open concurrently.

<!-- specs:surfaces -->
| Surface | Context |
| --- | --- |
| `S-FACTORY` | Five work tabs, Reported issues and one conversation page. Settings opens independently. |
| `S-SETTINGS` | A separate MCP Apps settings document using the same authenticated connection and bridged tools. No Inbox/Agents navigation, attention, launch overlay or task pane. |
<!-- /specs:surfaces -->

## Embedded pages

<!-- specs:pages-0 -->
| Page | Purpose | Page-owned panes | Entry and exit |
| --- | --- | --- | --- |
| `PAGE-INBOX` | Open issues including issues with linked PRs, ordered by priority. | Query and full-width issue list only. | Issues defaults to open; pending remains the optional open-without-PR filter. Per-tab search, filter, scroll and row focus remain independent. |
| `PAGE-AGENTS` | Answer agents blocked on human input. | Unanswered questions and approvals only; ordinary progress and results stay in the owning conversation. | List unanswered native questions and approvals, structured message questions and textual fallback asks with their owning session. Live RPC requests require a matching connected session; async/text requests remain answerable through retained session recovery. Selection opens the exact answer dialog. |
| `PAGE-SETTINGS` | Configure repositories, priorities, profiles, skills and diagnostics in an independent view. | One compact column of relevant repository/policy/profile/skill/diagnostic disclosures; no integration placeholder. Plugin/skill administration and system defaults are independent disclosures. | Opened through the dedicated open_settings tool/resource. No Inbox/Agents navigation; saving remains here with local progress/success/error feedback. It does not replace an already-open task view. |
| `PAGE-REPORTS` | Discover suspected defects and inspect investigation and delivery evidence. | Report list and selected report details. | Entered from Reported issues navigation; journal changes refresh both panes. |
| `PAGE-AGENT` | Chat about one item immediately. | Item summary then existing conversation or prefilled first message, followed by collapsed Item details. No item-only intermediate page. | Item selection continues the newest retained run or opens a prefilled draft. First Start is explicit. Sending a message to inactive retained work restores its session and starts that submitted message. Back restores the originating queue. Work history and prepared drafts stay under Item details. |
| `PAGE-MY-PRS` | Open pull requests authored by the authenticated GitHub account. | Query and full-width PR list only. | Independent search/filter and restored Back context; item selection opens its conversation directly. |
| `PAGE-REVIEW-PRS` | Open pull requests requesting the authenticated account or one of its teams. | Query and full-width PR list only. | Independent search/filter and restored Back context; item selection opens its conversation directly. |
| `PAGE-WORKING` | Follow launched work until GitHub completion. | Query and full-width issue/PR list. | Uses server queue membership; idle, failed and interrupted sessions remain tracked. Observed source closure or merge removes its own work, permanently terminates its sessions and retires pending questions, retaining question history and workspaces. A linked implementation PR merge completes the issue's Working membership without terminating its sessions. |
<!-- /specs:pages-0 -->

Issues, My PRs and Review PRs are focused work lists. Messages contains pending requests; retained work appears under the owning conversation’s Item details. Item selection opens the Agent conversation directly. Settings is an independent configuration surface.
PR checks, relationships, profiles and skills remain controls within their owning
page. Only one page is displayed; no sidebar or split layout is used.
No URL deep-link or browser-back routing contract is established by the current
embedded UI.

## Dialog surfaces

<!-- specs:pages-1 -->
| Dialog | Composition | Owning context | Dismissal and completion |
| --- | --- | --- | --- |
| `D-LAUNCH` | `P-LAUNCH-FRAME` and either `P-LAUNCH-EDITOR` or `P-LAUNCH-REVIEW`. | Selected item or retained prepared run; default entry prepares the repository or system role profile, while reopening a draft retains its immutable profile. | Explicit Start only. Edit exposes customization; Close retains prepared snapshots. Uncertain Start reads the same run and gates retry until status is known. |
| `D-ATTENTION` | `P-ATTENTION-FRAME` and one of `P-QUESTIONS`, `P-APPROVAL`, `P-ELICITATION`. | Request ID and owning run, with item title/profile displayed and run selected before opening. | Successful answer returns to the owning conversation. Deferred/failed answers remain pending. Keying by request resets local answers, including secret fields; no answers enter ordinary draft stores. |
<!-- /specs:pages-1 -->

Dialog closure does not answer a request, interrupt an agent or cancel a prepared
launch. A request resolved elsewhere disappears after refreshed state no longer
identifies it as pending. Launch and attention are independent shell conditions;
the current code does not enforce that only one overlay can exist at a time.

## Auxiliary owned pages

<!-- specs:pages-2 -->
| Page | Purpose and panes | Access boundary and transitions |
| --- | --- | --- |
| `PAGE-PACKAGE` | `P-PACKAGE-OVERVIEW`, `P-CONNECTION-GUIDE`. | Public, scoped application landing page. Downloads artifacts or guides the user into the external host. A package link is not an authenticated product session. |
| `PAGE-OWNER-CONSENT` | `P-OWNER-CONSENT`. | Private owner-only consent surface for one active request. Allow/Deny returns to the registered host callback. Expired or invalid requests remain error responses, not access to the embedded inbox. |
| `PAGE-LOCAL-TUNNEL-SETUP` | `P-LOCAL-TUNNEL-KEY`. | Temporary local alternate-transport setup session. Successful private key submission starts the configured alternate tunnel and removes the form. This transport is not currently enabled in the pilot. |
| `PAGE-BROWSER-LAB` | `P-BROWSER-LAB`. | Optional developer/operator utility with its own private session and isolated browser profile. Its screenshot/navigation controls are outside the product shell. |
<!-- /specs:pages-2 -->

External host sign-in, plugin catalog/installation, native Chrome permission and
provider sign-in screens are **external surfaces**. They are navigation targets or
operator prerequisites, not owned Code Factory pages. Do not invent their widgets
or treat this catalog as authorization to automate sign-in.

## Page state ownership

| State | Lifetime and sharing |
| --- | --- |
| Current page, search/filter, selected item and selected run | Shell-local while mounted. Back restores per-page scroll and row focus; query/drafts survive navigation. No browser URL/history or reload-persistence promise. |
| Snapshot records and run status | Shared application state refreshed into all relevant pages. User actions and background updates can change several panes together. |
| Item detail, priority and link drafts | Detail loads by selected item. Priority initializes from the saved override and saves immediately. Unsubmitted relationship targets are kept by item ID in the open app across tabs. |
| Settings drafts and diagnostics result | Settings component local. Leaving/re-entering remounts it from the latest snapshot; no unsaved-changes warning is implemented. |
| Launch draft and selected prepared result | Launch-dialog local. Prepared records themselves persist after preparation. |
| Attention selection and answer draft | The owning run is selected first. Answers/errors/busy state are keyed by request ID and end with that dialog; secret answers stay outside ordinary draft storage. |
| Run transcript and message drafts | Transcript cursor/displayed events reset on run change or remount; durable history remains stored. Message drafts are retained by run ID for the lifetime of the open app, including tab changes. |

These distinctions matter when drawing connections: a saved profile feeds a
future launch, while an edited but unsaved profile does not; selecting another
page is not a service-side run transition.
