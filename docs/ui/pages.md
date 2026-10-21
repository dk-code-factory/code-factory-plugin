# Pages and dialogs

[Widgets](widgets.md) · [Panes](panes.md) · [Connections](connections.md) ·
[Implementation/tests](references.md)

Page names below identify task surfaces, not framework routes. All embedded pages
use `S-FACTORY` and its identity, navigation, feedback and continuity panes.

## Embedded pages

<!-- specs:pages-0 -->
| Page | Purpose | Page-owned panes | Entry and exit |
| --- | --- | --- | --- |
| `PAGE-INBOX` | Search and triage synchronized issues/PRs without starting work. | `P-INBOX-QUERY`, `P-INBOX-LIST`, `P-ITEM-DETAIL`. | Initial page after data loads, or Inbox navigation. Selection changes detail in place. Launch action opens `D-LAUNCH`; Add repository goes to Settings. |
| `PAGE-AGENTS` | Inspect retained work, respond to requests and continue or recover a task. | `P-RUN-LIST`, `P-RUN-DETAIL`. | Agents navigation, successful launch, Needs attention, or Open agent chat. Selecting a run changes detail in place. Attention notices open `D-ATTENTION`. |
| `PAGE-SETTINGS` | Configure repository access, priorities, work profiles, skills and health inspection. | `P-REPOSITORIES`, `P-POLICIES`, `P-PROFILES`, `P-SKILLS`, `P-DIAGNOSTICS`. | Settings navigation or Add repository. Save actions update data in place. Leave through navigation; local unsaved drafts are not persisted across remount. |
<!-- /specs:pages-0 -->

The app has no separate item-detail, run-detail, PR-checks, ticket, profile or
managed-skill navigation page. Those are panes/widgets within the three pages.
No URL deep-link or browser-back routing contract is established by the current
embedded UI.

## Dialog surfaces

<!-- specs:pages-1 -->
| Dialog | Composition | Owning context | Dismissal and completion |
| --- | --- | --- | --- |
| `D-LAUNCH` | `P-LAUNCH-FRAME` and either `P-LAUNCH-EDITOR` or `P-LAUNCH-REVIEW`. | Item supplied when Launch agent is clicked. Rendered at shell level above the current page. | Close/Escape dismisses local review selection without starting/deleting prepared work and restores a connected opener. Preparation/Start failures appear in the dialog and retain edits/review. Received success closes, selects the run and enters Agents. |
| `D-ATTENTION` | `P-ATTENTION-FRAME` and one of `P-QUESTIONS`, `P-APPROVAL`, `P-ELICITATION`. | Selected pending request and its owning run. May be opened from global navigation or a selected run. | Close/Escape defers responding and restores a connected opener. Modal keyboard focus remains contained across polling. Successful answer closes/refreshes; failed response retains dialog/error. Open chat selects its owning run. |
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
| Current page, search/filter, selected item and selected run | Shell-local while mounted. Survive switching embedded tabs; not a promised URL/history or reload-persistence mechanism. |
| Snapshot records and run status | Shared application state refreshed into all relevant pages. User actions and background updates can change several panes together. |
| Item detail and priority/link drafts | Detail-pane local. New selected content loads asynchronously; current code does not promise every field resets on item-ID changes. |
| Settings drafts and diagnostics result | Settings component local. Leaving/re-entering remounts it from the latest snapshot; no unsaved-changes warning is implemented. |
| Launch draft and selected prepared result | Launch-dialog local. Prepared records themselves persist after preparation. |
| Attention selection and answer draft | Selected request is shell-local; input/error/busy state is dialog-local. The current dialog is not keyed by request ID, so a clean draft reset between simultaneously pending requests is not an established guarantee. |
| Run transcript cursor, retained displayed events and message draft | Run-pane local; reset on a keyed run change or remount. Durable conversation history remains application state. |

These distinctions matter when drawing connections: a saved profile feeds a
future launch, while an edited but unsaved profile does not; selecting another
page is not a service-side run transition.
