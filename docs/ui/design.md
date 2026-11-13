# Compact conversation UI

## Navigation

Use compact tabs: **Working**, **Issues**, **My PRs**, **Review PRs**, **Messages**,
**Events** and **Configuration**. Issues is the initial view. `open_settings`
opens Configuration. Show one full-width page at a time, with one content column.
Back restores the originating queue, query, filter, page, scroll and row focus.
Item selection starts no agent. Failed selection clears the previous item's
context and disables message submission until matching detail is available.
Source and relationship versions refresh selected detail, including the same item.
Back and reopening reload it. Acknowledge a read only after matching detail renders,
using its exact displayed revision; delayed, failed and abandoned responses stay
unread.

## Work lists

- Working contains tracked retained sessions until source or linked implementation
  completion; conversation history remains available afterward.
- Issues contains issues, My PRs contains the authenticated account's PRs, and
  Review PRs contains its direct or team review requests. Unknown identity remains
  visible rather than being guessed.
- Sort by P0–P3, unknown last, then latest update. Place priority beside the title
  and show the repository/number link below. Source links do not select an item.
- Keep read/unread, New and recorded actions independent. Provide search,
  open/all/new/unread filters and bounded list expansion.
- Search the complete saved source before paging. Begin with 50 bodyless rows,
  expand to 200, then provide next/previous pages. Discard responses to superseded
  queries. Load full source only for the selected item.
- Poll a compact change version and reuse the accepted view when unchanged.
  A new query requires a fresh projection; delayed older versions cannot replace
  newer state. Transcript updates are handled independently.

## Item conversation

Selecting an item opens saved source context and its canonical conversation.
Sending the first message creates a session using its configured native agent;
subsequent messages reuse its native thread and retained worktree. Preserve drafts
per item and on failed sends. Show execution state, reported work status and
pending event count independently. An idle turn does not imply reported done.

Show native messages and compact technical details. Combine streamed agent text
with its matching completed message once. Keep priorities, recorded actions,
relationships and source details available alongside chat. Manual unlink removes
only the manual relationship. Source completion does not erase conversation
history or worktrees.
Drain available transcript pages immediately before waiting for live updates.
Keep journal rows and the retained row window bounded. Reassemble only complete,
ordered output fragments across pages; incomplete sets produce no invented text.

Run/resume bypasses the event timer. Interrupt holds automatic delivery. Chat may
steer an active unblocked turn with queued event context; a native blocking request
must be answered first. Uncertain delivery remains held until explicit retained
history reconciliation. Cleanup is explicit and refuses dirty worktrees.
Recovery controls use persisted delivery state. Pending thread creation offers
retry without discarding a batch. A pending operation in one session leaves
other sessions' controls and answers available.

## Messages

Show pending native questions, approvals and explicit blocked-status requests with
session context and original options. Preserve request identity and blocking
semantics. Native approval amendments send the exact offered payload. Secret
answers use secret inputs and remain absent from transcripts. Stale RPC identities
cannot be answered; lost or answered requests are never replayed. There is no
text-question classifier or repair-turn UI.

Form elicitation shows the native message and schema with a JSON response draft.
Preserve rejected answers for correction and expose explicit decline/cancel.
URL elicitation opens through the host and requires explicit continuation.
Device verification requires native host support; the UI never fabricates proof.

## Configuration and events

Configuration has persistent chat, readable effective application/native settings
and concrete proposed diffs. Display intentional agent communication as titled
plain-text cards and questions as answer forms with optional choices and free
text. Hide ordinary assistant commentary and status summaries in this view;
retain them for diagnostics. New threads use the communication tool, while
older retained threads use explicit status-tool results and blocked questions.
Application proposals show only changed settings with complete before/after
values. Every edit needs explicit confirmation separate from
the proposing agent. Stale previews are rejected; confirmed changes wait for safe
turn boundaries. Native profiles, agent definitions and installed skills remain
the configuration source. Events exposes separate event, webhook-delivery, batch and ordered-rule archive
pages without starting work. Begin with 50 rows, offer 100 or 200, and provide
previous/next navigation and manual refresh. Kind and size changes reset offset
and selection. Show selected compact metadata and bounded previews with original
byte and truncation counts; bounded rule lists retain their total counts. Read
full rule instructions in Configuration. Retain the accepted same-query page and
selection when refresh fails. Hide stale pages after a query change, discard late
responses, and reread the archive when reopening Events.

## Verification

Use compact controls, narrow-layout wrapping, and light/dark themes. Read retries
may recover automatically; mutations are never automatically repeated. Preserve
message drafts and uncertain errors. Fixtures cover chat, native questions,
configuration confirmation, batch/status indicators, priorities and relationships.
The isolated production launch gate measures all seven navigation labels. Fixture
checks do not establish installed-host, real-provider, soak or full assistive
technology acceptance.
