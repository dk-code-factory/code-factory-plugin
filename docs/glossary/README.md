# Glossary

Canonical names and meanings. Names may be local to a named scope.

<!-- specs:terms -->
<a id="owner"></a>
## Owner

`owner` · domain

The person authorized to manage the private workspace.

<a id="repository"></a>
## Repository

`repository` · domain

A versioned collection of source material and its related work items.

<a id="inbox"></a>
## Inbox

`inbox` · domain

The collection of synchronized issues and pull requests available for personal triage.

<a id="work-item"></a>
## Work item

`work-item` · domain

An issue or pull request tracked in the private workspace.

<a id="agent-run"></a>
## Agent run

`agent-run` · domain

Retained agent work with a task, workspace, conversation and lifecycle status.

<a id="profile"></a>
## Work profile

`profile` · domain

Reusable task defaults, execution permissions and publishing authority.

<a id="skill"></a>
## Skill

`skill` · domain

Named instructions selected as context for agent work.

<a id="prepared-launch"></a>
## Prepared launch

`prepared-launch` · domain

A durable snapshot of proposed work settings awaiting an explicit start.

<a id="attention-request"></a>
## Attention request

`attention-request` · domain

A question, approval or supported form response requested by an agent.

<a id="credential"></a>
## Credential

`credential` · domain

A private value that authorizes access to a service.

<a id="priority"></a>
## Priority

`priority` · domain

The urgency assigned to an item by local override, label mapping or background assessment.

<a id="relationship"></a>
## Relationship

`relationship` · domain

A local or discovered reference connecting work items or ticket context.

<a id="subscription"></a>
## Subscription

`subscription` · domain

A host registration for delivery of matching application events.

<a id="reconciliation"></a>
## Reconciliation

`reconciliation` · domain

A reviewed decision about consistency among related contracts, implementation and assertions.

<a id="verification"></a>
## Verification

`verification` · domain

Recorded execution of checks against identified inputs.

<a id="workspace"></a>
## Workspace

`workspace` · domain

An isolated working checkout retained for an agent task.

<a id="transcript"></a>
## Transcript

`transcript` · domain

The retained conversation, output and changes belonging to agent work.

<a id="host"></a>
## Host

`host` · domain

The application presenting the connected private workspace.

<a id="glossary"></a>
## Glossary

`glossary` · domain

The canonical vocabulary and meanings used to describe the application.

<a id="entity"></a>
## UI entity

`entity` · domain

A named logical part of the visible interface.

<a id="widget-type"></a>
## Widget type

`widget-type` · domain

A reusable control or display with a cohesive purpose.

<a id="widget-instance"></a>
## Widget instance

`widget-instance` · domain

A displayed occurrence of a widget with its own local context.

<a id="pane"></a>
## Pane

`pane` · domain

A group of controls or displays sharing a task or selected record.

<a id="page"></a>
## Page

`page` · domain

A complete logical task surface.

<a id="dialog"></a>
## Dialog

`dialog` · domain

A temporary task surface above a page.

<a id="shell"></a>
## Shell

`shell` · domain

Shared identity, navigation, feedback and continuity around task surfaces.

<a id="element"></a>
## Named element

`element` · domain

An individually named control, label, indicator or display field within a widget.

<a id="coverage-gap"></a>
## Coverage gap

`coverage-gap` · domain

An identified shortfall in supported capability or evidence about behavior.

<a id="known-limitation"></a>
## Known limitation

`known-limitation` · domain

An observed constraint or shortcoming in current behavior or operating conditions.

<a id="unverified-behavior"></a>
## Unverified behavior

`unverified-behavior` · domain

Behavior whose claimed result has not yet been established by the required evidence.

<a id="keyboard-focus"></a>
## Keyboard focus

`keyboard-focus` · domain

The interface element currently designated to receive keyboard input.

<a id="modal-dialog"></a>
## Modal dialog

`modal-dialog` · domain

A dialog that restricts user interaction to its active task surface while open.

<a id="state-new"></a>
## New

`state-new` · state

An item discovered after the initial synchronization baseline and not yet acknowledged.

<a id="state-unread"></a>
## Unread

`state-unread` · state

An item with content changes beyond its last read revision.

<a id="state-acted-on"></a>
## Acted on

`state-acted-on` · state

An item with recorded personal action history independent of read and discovery status.

<a id="state-loading"></a>
## Loading

`state-loading` · state

Requested content is not yet available for display.

<a id="state-empty"></a>
## Empty

`state-empty` · state

The current collection has no records.

<a id="state-ready"></a>
## Ready

`state-ready` · state

Content is available for interaction.

<a id="state-error"></a>
## Error

`state-error` · state

An operation or content request has failed.

<a id="state-connected"></a>
## Connected

`state-connected` · state

The host connection is available.

<a id="state-disconnected"></a>
## Disconnected

`state-disconnected` · state

The host connection is unavailable or reconnecting.

<a id="state-pending"></a>
## Pending

`state-pending` · state

A decision, assessment or response has not yet resolved.

<a id="state-prepared"></a>
## Prepared

`state-prepared` · state

Work settings are retained for review before an explicit start.

<a id="state-queued"></a>
## Queued

`state-queued` · state

Explicitly started work is awaiting execution.

<a id="state-starting"></a>
## Starting

`state-starting` · state

A requested agent session is being initialized.

<a id="state-running"></a>
## Running

`state-running` · state

An agent turn is active.

<a id="state-waiting"></a>
## Waiting

`state-waiting` · state

An agent session is awaiting input or another required result.

<a id="state-idle"></a>
## Idle

`state-idle` · state

The current turn has finished and the retained session can receive follow-up.

<a id="state-interrupted"></a>
## Interrupted

`state-interrupted` · state

Active work has stopped and requires explicit continuation.

<a id="state-failed"></a>
## Failed

`state-failed` · state

Work ended with an error and requires explicit recovery.

<a id="state-cleaned"></a>
## Cleaned

`state-cleaned` · state

The stopped working checkout has been removed while history remains retained.

<a id="state-blocking"></a>
## Blocking

`state-blocking` · state

A request requires a response before the agent can proceed.

<a id="state-continuing"></a>
## Continuing

`state-continuing` · state

A pending request permits the agent to keep working.

<a id="state-verified"></a>
## Verified

`state-verified` · state

A check has observed its required successful result.

<a id="state-unverified"></a>
## Unverified

`state-unverified` · state

A required result has not been established by observation.

<a id="connection-contains"></a>
## contains

`connection-contains` · connection-type

Places a logical child within a task surface.

<a id="connection-selects"></a>
## selects

`connection-selects` · connection-type

Changes which record or subset is displayed.

<a id="connection-navigates"></a>
## navigates

`connection-navigates` · connection-type

Changes the active logical page.

<a id="connection-opens"></a>
## opens

`connection-opens` · connection-type

Shows a temporary task surface.

<a id="connection-closes"></a>
## closes

`connection-closes` · connection-type

Dismisses a temporary task surface.

<a id="connection-submits"></a>
## submits

`connection-submits` · connection-type

Sends an explicit state change or response.

<a id="connection-refreshes"></a>
## refreshes

`connection-refreshes` · connection-type

Supplies updated values to a display.

<a id="connection-references"></a>
## references

`connection-references` · connection-type

Names a related domain record without implying navigation.

<a id="connection-external"></a>
## external

`connection-external` · connection-type

Leaves the owned surface through an artifact, callback or host-mediated link.

<a id="ui-s-factory"></a>
## Factory shell

`ui-s-factory` · ui-shell

The factory shell within the application interface.

<a id="ui-w-identity"></a>
## Identity widget

`ui-w-identity` · ui-widget

The identity widget within the application interface.

<a id="element-w-identity-mark"></a>
## mark

`element-w-identity-mark` · element · scope: ui-w-identity

The mark control or displayed value in the identity widget.

<a id="element-w-identity-name"></a>
## name

`element-w-identity-name` · element · scope: ui-w-identity

The name control or displayed value in the identity widget.

<a id="element-w-identity-subtitle"></a>
## subtitle

`element-w-identity-subtitle` · element · scope: ui-w-identity

The subtitle control or displayed value in the identity widget.

<a id="element-w-identity-connection"></a>
## connection

`element-w-identity-connection` · element · scope: ui-w-identity

The connection control or displayed value in the identity widget.

<a id="ui-w-navigation"></a>
## Navigation widget

`ui-w-navigation` · ui-widget

The navigation widget within the application interface.

<a id="element-w-navigation-inbox"></a>
## inbox

`element-w-navigation-inbox` · element · scope: ui-w-navigation

The inbox control or displayed value in the navigation widget.

<a id="element-w-navigation-agents"></a>
## agents

`element-w-navigation-agents` · element · scope: ui-w-navigation

The agents control or displayed value in the navigation widget.

<a id="element-w-navigation-settings"></a>
## settings

`element-w-navigation-settings` · element · scope: ui-w-navigation

The settings control or displayed value in the navigation widget.

<a id="element-w-navigation-current"></a>
## current

`element-w-navigation-current` · element · scope: ui-w-navigation

The current control or displayed value in the navigation widget.

<a id="element-w-navigation-agents-count"></a>
## agents-count

`element-w-navigation-agents-count` · element · scope: ui-w-navigation

The agents count control or displayed value in the navigation widget.

<a id="element-w-navigation-needs-attention"></a>
## needs-attention

`element-w-navigation-needs-attention` · element · scope: ui-w-navigation

The needs attention control or displayed value in the navigation widget.

<a id="element-w-navigation-attention-count"></a>
## attention-count

`element-w-navigation-attention-count` · element · scope: ui-w-navigation

The attention count control or displayed value in the navigation widget.

<a id="ui-w-global-error"></a>
## Global error widget

`ui-w-global-error` · ui-widget

The global error widget within the application interface.

<a id="element-w-global-error-message"></a>
## message

`element-w-global-error-message` · element · scope: ui-w-global-error

The message control or displayed value in the global error widget.

<a id="element-w-global-error-dismiss"></a>
## dismiss

`element-w-global-error-dismiss` · element · scope: ui-w-global-error

The dismiss control or displayed value in the global error widget.

<a id="ui-w-connection-blocker"></a>
## Connection blocker widget

`ui-w-connection-blocker` · ui-widget

The connection blocker widget within the application interface.

<a id="element-w-connection-blocker-connecting"></a>
## connecting

`element-w-connection-blocker-connecting` · element · scope: ui-w-connection-blocker

The connecting control or displayed value in the connection blocker widget.

<a id="element-w-connection-blocker-setup-hint"></a>
## setup-hint

`element-w-connection-blocker-setup-hint` · element · scope: ui-w-connection-blocker

The setup hint control or displayed value in the connection blocker widget.

<a id="element-w-connection-blocker-blocker"></a>
## blocker

`element-w-connection-blocker-blocker` · element · scope: ui-w-connection-blocker

The blocker control or displayed value in the connection blocker widget.

<a id="ui-w-continuity-notice"></a>
## Continuity notice widget

`ui-w-continuity-notice` · ui-widget

The continuity notice widget within the application interface.

<a id="element-w-continuity-notice-runs-survive-close"></a>
## runs-survive-close

`element-w-continuity-notice-runs-survive-close` · element · scope: ui-w-continuity-notice

The runs survive close control or displayed value in the continuity notice widget.

<a id="element-w-continuity-notice-computer-online"></a>
## computer-online

`element-w-continuity-notice-computer-online` · element · scope: ui-w-continuity-notice

The computer online control or displayed value in the continuity notice widget.

<a id="ui-w-inbox-query"></a>
## Inbox query widget

`ui-w-inbox-query` · ui-widget

The inbox query widget within the application interface.

<a id="element-w-inbox-query-heading"></a>
## heading

`element-w-inbox-query-heading` · element · scope: ui-w-inbox-query

The heading control or displayed value in the inbox query widget.

<a id="element-w-inbox-query-sync-help"></a>
## sync-help

`element-w-inbox-query-sync-help` · element · scope: ui-w-inbox-query

The sync help control or displayed value in the inbox query widget.

<a id="element-w-inbox-query-search"></a>
## search

`element-w-inbox-query-search` · element · scope: ui-w-inbox-query

The search control or displayed value in the inbox query widget.

<a id="element-w-inbox-query-filter"></a>
## filter

`element-w-inbox-query-filter` · element · scope: ui-w-inbox-query

The filter control or displayed value in the inbox query widget.

<a id="ui-w-sync-health"></a>
## Sync health widget

`ui-w-sync-health` · ui-widget

The sync health widget within the application interface.

<a id="element-w-sync-health-repository-id"></a>
## repository-id

`element-w-sync-health-repository-id` · element · scope: ui-w-sync-health

The repository id control or displayed value in the sync health widget.

<a id="element-w-sync-health-last-success"></a>
## last-success

`element-w-sync-health-last-success` · element · scope: ui-w-sync-health

The last success control or displayed value in the sync health widget.

<a id="element-w-sync-health-waiting"></a>
## waiting

`element-w-sync-health-waiting` · element · scope: ui-w-sync-health

The waiting control or displayed value in the sync health widget.

<a id="element-w-sync-health-error"></a>
## error

`element-w-sync-health-error` · element · scope: ui-w-sync-health

The error control or displayed value in the sync health widget.

<a id="ui-w-item-row"></a>
## Item row widget

`ui-w-item-row` · ui-widget

The item row widget within the application interface.

<a id="element-w-item-row-select"></a>
## select

`element-w-item-row-select` · element · scope: ui-w-item-row

The select control or displayed value in the item row widget.

<a id="element-w-item-row-kind"></a>
## kind

`element-w-item-row-kind` · element · scope: ui-w-item-row

The kind control or displayed value in the item row widget.

<a id="element-w-item-row-id"></a>
## id

`element-w-item-row-id` · element · scope: ui-w-item-row

The id control or displayed value in the item row widget.

<a id="element-w-item-row-priority"></a>
## priority

`element-w-item-row-priority` · element · scope: ui-w-item-row

The priority control or displayed value in the item row widget.

<a id="element-w-item-row-title"></a>
## title

`element-w-item-row-title` · element · scope: ui-w-item-row

The title control or displayed value in the item row widget.

<a id="element-w-item-row-unread-dot"></a>
## unread-dot

`element-w-item-row-unread-dot` · element · scope: ui-w-item-row

The unread dot control or displayed value in the item row widget.

<a id="element-w-item-row-author"></a>
## author

`element-w-item-row-author` · element · scope: ui-w-item-row

The author control or displayed value in the item row widget.

<a id="element-w-item-row-state"></a>
## state

`element-w-item-row-state` · element · scope: ui-w-item-row

The state control or displayed value in the item row widget.

<a id="element-w-item-row-draft"></a>
## draft

`element-w-item-row-draft` · element · scope: ui-w-item-row

The draft control or displayed value in the item row widget.

<a id="element-w-item-row-new-badge"></a>
## new-badge

`element-w-item-row-new-badge` · element · scope: ui-w-item-row

The new badge control or displayed value in the item row widget.

<a id="element-w-item-row-acted-on"></a>
## acted-on

`element-w-item-row-acted-on` · element · scope: ui-w-item-row

The acted on control or displayed value in the item row widget.

<a id="ui-w-inbox-empty"></a>
## Inbox empty widget

`ui-w-inbox-empty` · ui-widget

The inbox empty widget within the application interface.

<a id="element-w-inbox-empty-empty-heading"></a>
## empty-heading

`element-w-inbox-empty-empty-heading` · element · scope: ui-w-inbox-empty

The empty heading control or displayed value in the inbox empty widget.

<a id="element-w-inbox-empty-baseline-help"></a>
## baseline-help

`element-w-inbox-empty-baseline-help` · element · scope: ui-w-inbox-empty

The baseline help control or displayed value in the inbox empty widget.

<a id="element-w-inbox-empty-add-repository"></a>
## add-repository

`element-w-inbox-empty-add-repository` · element · scope: ui-w-inbox-empty

The add repository control or displayed value in the inbox empty widget.

<a id="ui-w-item-overview"></a>
## Item overview widget

`ui-w-item-overview` · ui-widget

The item overview widget within the application interface.

<a id="element-w-item-overview-loading"></a>
## loading

`element-w-item-overview-loading` · element · scope: ui-w-item-overview

The loading control or displayed value in the item overview widget.

<a id="element-w-item-overview-select-item"></a>
## select-item

`element-w-item-overview-select-item` · element · scope: ui-w-item-overview

The select item control or displayed value in the item overview widget.

<a id="element-w-item-overview-id"></a>
## id

`element-w-item-overview-id` · element · scope: ui-w-item-overview

The id control or displayed value in the item overview widget.

<a id="element-w-item-overview-kind"></a>
## kind

`element-w-item-overview-kind` · element · scope: ui-w-item-overview

The kind control or displayed value in the item overview widget.

<a id="element-w-item-overview-state"></a>
## state

`element-w-item-overview-state` · element · scope: ui-w-item-overview

The state control or displayed value in the item overview widget.

<a id="element-w-item-overview-title"></a>
## title

`element-w-item-overview-title` · element · scope: ui-w-item-overview

The title control or displayed value in the item overview widget.

<a id="element-w-item-overview-body"></a>
## body

`element-w-item-overview-body` · element · scope: ui-w-item-overview

The body control or displayed value in the item overview widget.

<a id="ui-w-item-actions"></a>
## Item actions widget

`ui-w-item-actions` · ui-widget

The item actions widget within the application interface.

<a id="element-w-item-actions-launch"></a>
## launch

`element-w-item-actions-launch` · element · scope: ui-w-item-actions

The launch control or displayed value in the item actions widget.

<a id="element-w-item-actions-unread"></a>
## unread

`element-w-item-actions-unread` · element · scope: ui-w-item-actions

The unread control or displayed value in the item actions widget.

<a id="element-w-item-actions-acknowledge"></a>
## acknowledge

`element-w-item-actions-acknowledge` · element · scope: ui-w-item-actions

The acknowledge control or displayed value in the item actions widget.

<a id="element-w-item-actions-record-action"></a>
## record-action

`element-w-item-actions-record-action` · element · scope: ui-w-item-actions

The record action control or displayed value in the item actions widget.

<a id="ui-w-priority"></a>
## Priority widget

`ui-w-priority` · ui-widget

The priority widget within the application interface.

<a id="element-w-priority-priority"></a>
## priority

`element-w-priority-priority` · element · scope: ui-w-priority

The priority control or displayed value in the priority widget.

<a id="element-w-priority-source"></a>
## source

`element-w-priority-source` · element · scope: ui-w-priority

The source control or displayed value in the priority widget.

<a id="element-w-priority-rationale-or-error"></a>
## rationale-or-error

`element-w-priority-rationale-or-error` · element · scope: ui-w-priority

The rationale or error control or displayed value in the priority widget.

<a id="element-w-priority-override"></a>
## override

`element-w-priority-override` · element · scope: ui-w-priority

The override control or displayed value in the priority widget.

<a id="element-w-priority-save"></a>
## save

`element-w-priority-save` · element · scope: ui-w-priority

The save control or displayed value in the priority widget.

<a id="ui-w-relationships"></a>
## Relationships widget

`ui-w-relationships` · ui-widget

The relationships widget within the application interface.

<a id="element-w-relationships-target"></a>
## target

`element-w-relationships-target` · element · scope: ui-w-relationships

The target control or displayed value in the relationships widget.

<a id="element-w-relationships-evidence-or-local"></a>
## evidence-or-local

`element-w-relationships-evidence-or-local` · element · scope: ui-w-relationships

The evidence or local control or displayed value in the relationships widget.

<a id="element-w-relationships-remove"></a>
## remove

`element-w-relationships-remove` · element · scope: ui-w-relationships

The remove control or displayed value in the relationships widget.

<a id="element-w-relationships-target-input"></a>
## target-input

`element-w-relationships-target-input` · element · scope: ui-w-relationships

The target input control or displayed value in the relationships widget.

<a id="element-w-relationships-add"></a>
## add

`element-w-relationships-add` · element · scope: ui-w-relationships

The add control or displayed value in the relationships widget.

<a id="ui-w-ticket-context"></a>
## Ticket context widget

`ui-w-ticket-context` · ui-widget

The ticket context widget within the application interface.

<a id="element-w-ticket-context-ticket-id"></a>
## ticket-id

`element-w-ticket-context-ticket-id` · element · scope: ui-w-ticket-context

The ticket id control or displayed value in the ticket context widget.

<a id="element-w-ticket-context-summary-or-error"></a>
## summary-or-error

`element-w-ticket-context-summary-or-error` · element · scope: ui-w-ticket-context

The summary or error control or displayed value in the ticket context widget.

<a id="element-w-ticket-context-status"></a>
## status

`element-w-ticket-context-status` · element · scope: ui-w-ticket-context

The status control or displayed value in the ticket context widget.

<a id="ui-w-pr-evidence"></a>
## Pr evidence widget

`ui-w-pr-evidence` · ui-widget

The pr evidence widget within the application interface.

<a id="element-w-pr-evidence-head"></a>
## head

`element-w-pr-evidence-head` · element · scope: ui-w-pr-evidence

The head control or displayed value in the pr evidence widget.

<a id="element-w-pr-evidence-branch"></a>
## branch

`element-w-pr-evidence-branch` · element · scope: ui-w-pr-evidence

The branch control or displayed value in the pr evidence widget.

<a id="element-w-pr-evidence-checks-disclosure"></a>
## checks-disclosure

`element-w-pr-evidence-checks-disclosure` · element · scope: ui-w-pr-evidence

The checks disclosure control or displayed value in the pr evidence widget.

<a id="element-w-pr-evidence-checks-content"></a>
## checks-content

`element-w-pr-evidence-checks-content` · element · scope: ui-w-pr-evidence

The checks content control or displayed value in the pr evidence widget.

<a id="ui-w-item-history"></a>
## Item history widget

`ui-w-item-history` · ui-widget

The item history widget within the application interface.

<a id="element-w-item-history-activity"></a>
## activity

`element-w-item-history-activity` · element · scope: ui-w-item-history

The activity control or displayed value in the item history widget.

<a id="element-w-item-history-action-history"></a>
## action-history

`element-w-item-history-action-history` · element · scope: ui-w-item-history

The action history control or displayed value in the item history widget.

<a id="ui-w-run-row"></a>
## Run row widget

`ui-w-run-row` · ui-widget

The run row widget within the application interface.

<a id="element-w-run-row-select"></a>
## select

`element-w-run-row-select` · element · scope: ui-w-run-row

The select control or displayed value in the run row widget.

<a id="element-w-run-row-item-title-or-id"></a>
## item-title-or-id

`element-w-run-row-item-title-or-id` · element · scope: ui-w-run-row

The item title or id control or displayed value in the run row widget.

<a id="element-w-run-row-profile"></a>
## profile

`element-w-run-row-profile` · element · scope: ui-w-run-row

The profile control or displayed value in the run row widget.

<a id="element-w-run-row-status"></a>
## status

`element-w-run-row-status` · element · scope: ui-w-run-row

The status control or displayed value in the run row widget.

<a id="element-w-run-row-attention-badge"></a>
## attention-badge

`element-w-run-row-attention-badge` · element · scope: ui-w-run-row

The attention badge control or displayed value in the run row widget.

<a id="ui-w-run-empty"></a>
## Run empty widget

`ui-w-run-empty` · ui-widget

The run empty widget within the application interface.

<a id="element-w-run-empty-no-runs"></a>
## no-runs

`element-w-run-empty-no-runs` · element · scope: ui-w-run-empty

The no runs control or displayed value in the run empty widget.

<a id="element-w-run-empty-select-run"></a>
## select-run

`element-w-run-empty-select-run` · element · scope: ui-w-run-empty

The select run control or displayed value in the run empty widget.

<a id="ui-w-run-controls"></a>
## Run controls widget

`ui-w-run-controls` · ui-widget

The run controls widget within the application interface.

<a id="element-w-run-controls-status"></a>
## status

`element-w-run-controls-status` · element · scope: ui-w-run-controls

The status control or displayed value in the run controls widget.

<a id="element-w-run-controls-profile-name"></a>
## profile-name

`element-w-run-controls-profile-name` · element · scope: ui-w-run-controls

The profile name control or displayed value in the run controls widget.

<a id="element-w-run-controls-workspace"></a>
## workspace

`element-w-run-controls-workspace` · element · scope: ui-w-run-controls

The workspace control or displayed value in the run controls widget.

<a id="element-w-run-controls-run-error"></a>
## run-error

`element-w-run-controls-run-error` · element · scope: ui-w-run-controls

The run error control or displayed value in the run controls widget.

<a id="element-w-run-controls-interrupt"></a>
## interrupt

`element-w-run-controls-interrupt` · element · scope: ui-w-run-controls

The interrupt control or displayed value in the run controls widget.

<a id="element-w-run-controls-resume"></a>
## resume

`element-w-run-controls-resume` · element · scope: ui-w-run-controls

The resume control or displayed value in the run controls widget.

<a id="element-w-run-controls-cleanup"></a>
## cleanup

`element-w-run-controls-cleanup` · element · scope: ui-w-run-controls

The cleanup control or displayed value in the run controls widget.

<a id="ui-w-attention-notice"></a>
## Attention notice widget

`ui-w-attention-notice` · ui-widget

The attention notice widget within the application interface.

<a id="element-w-attention-notice-answer"></a>
## answer

`element-w-attention-notice-answer` · element · scope: ui-w-attention-notice

The answer control or displayed value in the attention notice widget.

<a id="element-w-attention-notice-blocking-label"></a>
## blocking-label

`element-w-attention-notice-blocking-label` · element · scope: ui-w-attention-notice

The blocking label control or displayed value in the attention notice widget.

<a id="element-w-attention-notice-continuing-label"></a>
## continuing-label

`element-w-attention-notice-continuing-label` · element · scope: ui-w-attention-notice

The continuing label control or displayed value in the attention notice widget.

<a id="ui-w-transcript"></a>
## Transcript widget

`ui-w-transcript` · ui-widget

The transcript widget within the application interface.

<a id="element-w-transcript-heading"></a>
## heading

`element-w-transcript-heading` · element · scope: ui-w-transcript

The heading control or displayed value in the transcript widget.

<a id="element-w-transcript-live-region"></a>
## live-region

`element-w-transcript-live-region` · element · scope: ui-w-transcript

The live region control or displayed value in the transcript widget.

<a id="element-w-transcript-block-label"></a>
## block-label

`element-w-transcript-block-label` · element · scope: ui-w-transcript

The block label control or displayed value in the transcript widget.

<a id="element-w-transcript-block-text"></a>
## block-text

`element-w-transcript-block-text` · element · scope: ui-w-transcript

The block text control or displayed value in the transcript widget.

<a id="element-w-transcript-waiting"></a>
## waiting

`element-w-transcript-waiting` · element · scope: ui-w-transcript

The waiting control or displayed value in the transcript widget.

<a id="element-w-transcript-loading-more"></a>
## loading-more

`element-w-transcript-loading-more` · element · scope: ui-w-transcript

The loading more control or displayed value in the transcript widget.

<a id="element-w-transcript-restart-history"></a>
## restart-history

`element-w-transcript-restart-history` · element · scope: ui-w-transcript

The restart history control or displayed value in the transcript widget.

<a id="ui-w-message"></a>
## Message widget

`ui-w-message` · ui-widget

The message widget within the application interface.

<a id="element-w-message-text"></a>
## text

`element-w-message-text` · element · scope: ui-w-message

The text control or displayed value in the message widget.

<a id="element-w-message-send"></a>
## send

`element-w-message-send` · element · scope: ui-w-message

The send control or displayed value in the message widget.

<a id="ui-w-dialog-frame"></a>
## Dialog frame widget

`ui-w-dialog-frame` · ui-widget

The dialog frame widget within the application interface.

<a id="element-w-dialog-frame-heading"></a>
## heading

`element-w-dialog-frame-heading` · element · scope: ui-w-dialog-frame

The heading control or displayed value in the dialog frame widget.

<a id="element-w-dialog-frame-close"></a>
## close

`element-w-dialog-frame-close` · element · scope: ui-w-dialog-frame

The close control or displayed value in the dialog frame widget.

<a id="element-w-dialog-frame-item"></a>
## item

`element-w-dialog-frame-item` · element · scope: ui-w-dialog-frame

The item control or displayed value in the dialog frame widget.

<a id="element-w-dialog-frame-blocking-status"></a>
## blocking-status

`element-w-dialog-frame-blocking-status` · element · scope: ui-w-dialog-frame

The blocking status control or displayed value in the dialog frame widget.

<a id="element-w-dialog-frame-response-error"></a>
## response-error

`element-w-dialog-frame-response-error` · element · scope: ui-w-dialog-frame

The response error control or displayed value in the dialog frame widget.

<a id="element-w-dialog-frame-open-chat"></a>
## open-chat

`element-w-dialog-frame-open-chat` · element · scope: ui-w-dialog-frame

The open chat control or displayed value in the dialog frame widget.

<a id="ui-w-launch-editor"></a>
## Launch editor widget

`ui-w-launch-editor` · ui-widget

The launch editor widget within the application interface.

<a id="element-w-launch-editor-profile"></a>
## profile

`element-w-launch-editor-profile` · element · scope: ui-w-launch-editor

The profile control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-task-prompt"></a>
## task-prompt

`element-w-launch-editor-task-prompt` · element · scope: ui-w-launch-editor

The task prompt control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-model"></a>
## model

`element-w-launch-editor-model` · element · scope: ui-w-launch-editor

The model control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-effort"></a>
## effort

`element-w-launch-editor-effort` · element · scope: ui-w-launch-editor

The effort control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-base"></a>
## base

`element-w-launch-editor-base` · element · scope: ui-w-launch-editor

The base control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-skills"></a>
## skills

`element-w-launch-editor-skills` · element · scope: ui-w-launch-editor

The skills control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-workspace-policy"></a>
## workspace-policy

`element-w-launch-editor-workspace-policy` · element · scope: ui-w-launch-editor

The workspace policy control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-approval-policy"></a>
## approval-policy

`element-w-launch-editor-approval-policy` · element · scope: ui-w-launch-editor

The approval policy control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-publishing-authority"></a>
## publishing-authority

`element-w-launch-editor-publishing-authority` · element · scope: ui-w-launch-editor

The publishing authority control or displayed value in the launch editor widget.

<a id="element-w-launch-editor-review"></a>
## review

`element-w-launch-editor-review` · element · scope: ui-w-launch-editor

The review control or displayed value in the launch editor widget.

<a id="ui-w-launch-review"></a>
## Launch review widget

`ui-w-launch-review` · ui-widget

The launch review widget within the application interface.

<a id="element-w-launch-review-workspace"></a>
## workspace

`element-w-launch-review-workspace` · element · scope: ui-w-launch-review

The workspace control or displayed value in the launch review widget.

<a id="element-w-launch-review-branch-and-base"></a>
## branch-and-base

`element-w-launch-review-branch-and-base` · element · scope: ui-w-launch-review

The branch and base control or displayed value in the launch review widget.

<a id="element-w-launch-review-permissions"></a>
## permissions

`element-w-launch-review-permissions` · element · scope: ui-w-launch-review

The permissions control or displayed value in the launch review widget.

<a id="element-w-launch-review-publishing"></a>
## publishing

`element-w-launch-review-publishing` · element · scope: ui-w-launch-review

The publishing control or displayed value in the launch review widget.

<a id="element-w-launch-review-skills"></a>
## skills

`element-w-launch-review-skills` · element · scope: ui-w-launch-review

The skills control or displayed value in the launch review widget.

<a id="element-w-launch-review-prepared-prompt"></a>
## prepared-prompt

`element-w-launch-review-prepared-prompt` · element · scope: ui-w-launch-review

The prepared prompt control or displayed value in the launch review widget.

<a id="element-w-launch-review-edit"></a>
## edit

`element-w-launch-review-edit` · element · scope: ui-w-launch-review

The edit control or displayed value in the launch review widget.

<a id="element-w-launch-review-start"></a>
## start

`element-w-launch-review-start` · element · scope: ui-w-launch-review

The start control or displayed value in the launch review widget.

<a id="ui-w-questions"></a>
## Questions widget

`ui-w-questions` · ui-widget

The questions widget within the application interface.

<a id="element-w-questions-question"></a>
## question

`element-w-questions-question` · element · scope: ui-w-questions

The question control or displayed value in the questions widget.

<a id="element-w-questions-options"></a>
## options

`element-w-questions-options` · element · scope: ui-w-questions

The options control or displayed value in the questions widget.

<a id="element-w-questions-answer-text"></a>
## answer-text

`element-w-questions-answer-text` · element · scope: ui-w-questions

The answer text control or displayed value in the questions widget.

<a id="element-w-questions-secret-answer"></a>
## secret-answer

`element-w-questions-secret-answer` · element · scope: ui-w-questions

The secret answer control or displayed value in the questions widget.

<a id="element-w-questions-submit-answer"></a>
## submit-answer

`element-w-questions-submit-answer` · element · scope: ui-w-questions

The submit answer control or displayed value in the questions widget.

<a id="ui-w-approval"></a>
## Approval widget

`ui-w-approval` · ui-widget

The approval widget within the application interface.

<a id="element-w-approval-request-details"></a>
## request-details

`element-w-approval-request-details` · element · scope: ui-w-approval

The request details control or displayed value in the approval widget.

<a id="element-w-approval-deny"></a>
## deny

`element-w-approval-deny` · element · scope: ui-w-approval

The deny control or displayed value in the approval widget.

<a id="element-w-approval-approve-requested-for-turn"></a>
## approve-requested-for-turn

`element-w-approval-approve-requested-for-turn` · element · scope: ui-w-approval

The approve requested for turn control or displayed value in the approval widget.

<a id="element-w-approval-available-decision"></a>
## available-decision

`element-w-approval-available-decision` · element · scope: ui-w-approval

The available decision control or displayed value in the approval widget.

<a id="ui-w-elicitation"></a>
## Elicitation widget

`ui-w-elicitation` · ui-widget

The elicitation widget within the application interface.

<a id="element-w-elicitation-message"></a>
## message

`element-w-elicitation-message` · element · scope: ui-w-elicitation

The message control or displayed value in the elicitation widget.

<a id="element-w-elicitation-open-authorization"></a>
## open-authorization

`element-w-elicitation-open-authorization` · element · scope: ui-w-elicitation

The open authorization control or displayed value in the elicitation widget.

<a id="element-w-elicitation-continue"></a>
## continue

`element-w-elicitation-continue` · element · scope: ui-w-elicitation

The continue control or displayed value in the elicitation widget.

<a id="element-w-elicitation-field-title"></a>
## field-title

`element-w-elicitation-field-title` · element · scope: ui-w-elicitation

The field title control or displayed value in the elicitation widget.

<a id="element-w-elicitation-description"></a>
## description

`element-w-elicitation-description` · element · scope: ui-w-elicitation

The description control or displayed value in the elicitation widget.

<a id="element-w-elicitation-boolean-answer"></a>
## boolean-answer

`element-w-elicitation-boolean-answer` · element · scope: ui-w-elicitation

Checkbox value for a true or false form answer.

<a id="element-w-elicitation-enum-answer"></a>
## enum-answer

`element-w-elicitation-enum-answer` · element · scope: ui-w-elicitation

Single choice selected from a fixed set of form answer values.

<a id="element-w-elicitation-multi-enum-answer"></a>
## multi-enum-answer

`element-w-elicitation-multi-enum-answer` · element · scope: ui-w-elicitation

Multiple choices selected from a fixed set of form answer values.

<a id="element-w-elicitation-number-answer"></a>
## number-answer

`element-w-elicitation-number-answer` · element · scope: ui-w-elicitation

Numeric form answer with integer or fractional values and applicable bounds.

<a id="element-w-elicitation-email-answer"></a>
## email-answer

`element-w-elicitation-email-answer` · element · scope: ui-w-elicitation

Email address entered as a form answer.

<a id="element-w-elicitation-text-answer"></a>
## text-answer

`element-w-elicitation-text-answer` · element · scope: ui-w-elicitation

Text entered as a form answer.

<a id="element-w-elicitation-native-support-notice"></a>
## native-support-notice

`element-w-elicitation-native-support-notice` · element · scope: ui-w-elicitation

Notice that a verification request requires native host support.

<a id="element-w-elicitation-submit-form"></a>
## submit-form

`element-w-elicitation-submit-form` · element · scope: ui-w-elicitation

The submit form control or displayed value in the elicitation widget.

<a id="element-w-elicitation-decline"></a>
## decline

`element-w-elicitation-decline` · element · scope: ui-w-elicitation

The decline control or displayed value in the elicitation widget.

<a id="element-w-elicitation-cancel"></a>
## cancel

`element-w-elicitation-cancel` · element · scope: ui-w-elicitation

The cancel control or displayed value in the elicitation widget.

<a id="ui-w-repository-settings"></a>
## Repository settings widget

`ui-w-repository-settings` · ui-widget

The repository settings widget within the application interface.

<a id="element-w-repository-settings-repository-summary"></a>
## repository-summary

`element-w-repository-settings-repository-summary` · element · scope: ui-w-repository-settings

The repository summary control or displayed value in the repository settings widget.

<a id="element-w-repository-settings-repository"></a>
## repository

`element-w-repository-settings-repository` · element · scope: ui-w-repository-settings

The repository control or displayed value in the repository settings widget.

<a id="element-w-repository-settings-checkout"></a>
## checkout

`element-w-repository-settings-checkout` · element · scope: ui-w-repository-settings

The checkout control or displayed value in the repository settings widget.

<a id="element-w-repository-settings-default-base"></a>
## default-base

`element-w-repository-settings-default-base` · element · scope: ui-w-repository-settings

The default base control or displayed value in the repository settings widget.

<a id="element-w-repository-settings-save"></a>
## save

`element-w-repository-settings-save` · element · scope: ui-w-repository-settings

The save control or displayed value in the repository settings widget.

<a id="element-w-repository-settings-discover-skills"></a>
## discover-skills

`element-w-repository-settings-discover-skills` · element · scope: ui-w-repository-settings

The discover skills control or displayed value in the repository settings widget.

<a id="ui-w-policy-settings"></a>
## Policy settings widget

`ui-w-policy-settings` · ui-widget

The policy settings widget within the application interface.

<a id="element-w-policy-settings-priority-labels"></a>
## priority-labels

`element-w-policy-settings-priority-labels` · element · scope: ui-w-policy-settings

The priority labels control or displayed value in the policy settings widget.

<a id="element-w-policy-settings-assessment-prompt"></a>
## assessment-prompt

`element-w-policy-settings-assessment-prompt` · element · scope: ui-w-policy-settings

The assessment prompt control or displayed value in the policy settings widget.

<a id="element-w-policy-settings-notifications"></a>
## notifications

`element-w-policy-settings-notifications` · element · scope: ui-w-policy-settings

The notifications control or displayed value in the policy settings widget.

<a id="element-w-policy-settings-jira-url"></a>
## jira-url

`element-w-policy-settings-jira-url` · element · scope: ui-w-policy-settings

The jira url control or displayed value in the policy settings widget.

<a id="element-w-policy-settings-jira-email"></a>
## jira-email

`element-w-policy-settings-jira-email` · element · scope: ui-w-policy-settings

The jira email control or displayed value in the policy settings widget.

<a id="element-w-policy-settings-save"></a>
## save

`element-w-policy-settings-save` · element · scope: ui-w-policy-settings

The save control or displayed value in the policy settings widget.

<a id="ui-w-jira-credential"></a>
## Jira credential widget

`ui-w-jira-credential` · ui-widget

The jira credential widget within the application interface.

<a id="element-w-jira-credential-token"></a>
## token

`element-w-jira-credential-token` · element · scope: ui-w-jira-credential

The token control or displayed value in the jira credential widget.

<a id="element-w-jira-credential-save-securely"></a>
## save-securely

`element-w-jira-credential-save-securely` · element · scope: ui-w-jira-credential

The save securely control or displayed value in the jira credential widget.

<a id="element-w-jira-credential-read-only-notice"></a>
## read-only-notice

`element-w-jira-credential-read-only-notice` · element · scope: ui-w-jira-credential

The read only notice control or displayed value in the jira credential widget.

<a id="ui-w-profile-settings"></a>
## Profile settings widget

`ui-w-profile-settings` · ui-widget

The profile settings widget within the application interface.

<a id="element-w-profile-settings-existing-profile"></a>
## existing-profile

`element-w-profile-settings-existing-profile` · element · scope: ui-w-profile-settings

The existing profile control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-new"></a>
## new

`element-w-profile-settings-new` · element · scope: ui-w-profile-settings

The new control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-name"></a>
## name

`element-w-profile-settings-name` · element · scope: ui-w-profile-settings

The name control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-prompt-template"></a>
## prompt-template

`element-w-profile-settings-prompt-template` · element · scope: ui-w-profile-settings

The prompt template control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-model"></a>
## model

`element-w-profile-settings-model` · element · scope: ui-w-profile-settings

The model control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-effort"></a>
## effort

`element-w-profile-settings-effort` · element · scope: ui-w-profile-settings

The effort control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-mode"></a>
## mode

`element-w-profile-settings-mode` · element · scope: ui-w-profile-settings

The mode control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-sandbox"></a>
## sandbox

`element-w-profile-settings-sandbox` · element · scope: ui-w-profile-settings

The sandbox control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-approval"></a>
## approval

`element-w-profile-settings-approval` · element · scope: ui-w-profile-settings

The approval control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-publishing"></a>
## publishing

`element-w-profile-settings-publishing` · element · scope: ui-w-profile-settings

The publishing control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-default-skills"></a>
## default-skills

`element-w-profile-settings-default-skills` · element · scope: ui-w-profile-settings

The default skills control or displayed value in the profile settings widget.

<a id="element-w-profile-settings-save"></a>
## save

`element-w-profile-settings-save` · element · scope: ui-w-profile-settings

The save control or displayed value in the profile settings widget.

<a id="ui-w-skill-settings"></a>
## Skill settings widget

`ui-w-skill-settings` · ui-widget

The skill settings widget within the application interface.

<a id="element-w-skill-settings-managed-skill"></a>
## managed-skill

`element-w-skill-settings-managed-skill` · element · scope: ui-w-skill-settings

The managed skill control or displayed value in the skill settings widget.

<a id="element-w-skill-settings-name"></a>
## name

`element-w-skill-settings-name` · element · scope: ui-w-skill-settings

The name control or displayed value in the skill settings widget.

<a id="element-w-skill-settings-instructions"></a>
## instructions

`element-w-skill-settings-instructions` · element · scope: ui-w-skill-settings

The instructions control or displayed value in the skill settings widget.

<a id="element-w-skill-settings-save"></a>
## save

`element-w-skill-settings-save` · element · scope: ui-w-skill-settings

The save control or displayed value in the skill settings widget.

<a id="ui-w-diagnostics"></a>
## Diagnostics widget

`ui-w-diagnostics` · ui-widget

The diagnostics widget within the application interface.

<a id="element-w-diagnostics-check-connection"></a>
## check-connection

`element-w-diagnostics-check-connection` · element · scope: ui-w-diagnostics

The check connection control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-version"></a>
## version

`element-w-diagnostics-version` · element · scope: ui-w-diagnostics

The version control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-timestamp"></a>
## timestamp

`element-w-diagnostics-timestamp` · element · scope: ui-w-diagnostics

The timestamp control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-check-state"></a>
## check-state

`element-w-diagnostics-check-state` · element · scope: ui-w-diagnostics

The check state control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-check-id"></a>
## check-id

`element-w-diagnostics-check-id` · element · scope: ui-w-diagnostics

The check id control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-detail"></a>
## detail

`element-w-diagnostics-detail` · element · scope: ui-w-diagnostics

The detail control or displayed value in the diagnostics widget.

<a id="element-w-diagnostics-provider-sign-in-help"></a>
## provider-sign-in-help

`element-w-diagnostics-provider-sign-in-help` · element · scope: ui-w-diagnostics

The provider sign in help control or displayed value in the diagnostics widget.

<a id="ui-w-package-overview"></a>
## Package overview widget

`ui-w-package-overview` · ui-widget

The package overview widget within the application interface.

<a id="element-w-package-overview-branding"></a>
## branding

`element-w-package-overview-branding` · element · scope: ui-w-package-overview

The branding control or displayed value in the package overview widget.

<a id="element-w-package-overview-version"></a>
## version

`element-w-package-overview-version` · element · scope: ui-w-package-overview

The version control or displayed value in the package overview widget.

<a id="element-w-package-overview-capability-summary"></a>
## capability-summary

`element-w-package-overview-capability-summary` · element · scope: ui-w-package-overview

The capability summary control or displayed value in the package overview widget.

<a id="element-w-package-overview-portable-download"></a>
## portable-download

`element-w-package-overview-portable-download` · element · scope: ui-w-package-overview

The portable download control or displayed value in the package overview widget.

<a id="element-w-package-overview-checksum"></a>
## checksum

`element-w-package-overview-checksum` · element · scope: ui-w-package-overview

The checksum control or displayed value in the package overview widget.

<a id="element-w-package-overview-privacy-notice"></a>
## privacy-notice

`element-w-package-overview-privacy-notice` · element · scope: ui-w-package-overview

The privacy notice control or displayed value in the package overview widget.

<a id="ui-w-connection-guide"></a>
## Connection guide widget

`ui-w-connection-guide` · ui-widget

The connection guide widget within the application interface.

<a id="element-w-connection-guide-browser-steps"></a>
## browser-steps

`element-w-connection-guide-browser-steps` · element · scope: ui-w-connection-guide

The browser steps control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-endpoint"></a>
## endpoint

`element-w-connection-guide-endpoint` · element · scope: ui-w-connection-guide

The endpoint control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-browser-package-download"></a>
## browser-package-download

`element-w-connection-guide-browser-package-download` · element · scope: ui-w-connection-guide

The browser package download control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-mapping-warning"></a>
## mapping-warning

`element-w-connection-guide-mapping-warning` · element · scope: ui-w-connection-guide

The mapping warning control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-portable-package-help"></a>
## portable-package-help

`element-w-connection-guide-portable-package-help` · element · scope: ui-w-connection-guide

The portable package help control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-manifest"></a>
## manifest

`element-w-connection-guide-manifest` · element · scope: ui-w-connection-guide

The manifest control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-connection-config"></a>
## connection-config

`element-w-connection-guide-connection-config` · element · scope: ui-w-connection-guide

The connection config control or displayed value in the connection guide widget.

<a id="element-w-connection-guide-release-details"></a>
## release-details

`element-w-connection-guide-release-details` · element · scope: ui-w-connection-guide

The release details control or displayed value in the connection guide widget.

<a id="ui-w-owner-consent"></a>
## Owner consent widget

`ui-w-owner-consent` · ui-widget

The owner consent widget within the application interface.

<a id="element-w-owner-consent-connection-heading"></a>
## connection-heading

`element-w-owner-consent-connection-heading` · element · scope: ui-w-owner-consent

The connection heading control or displayed value in the owner consent widget.

<a id="element-w-owner-consent-client-name"></a>
## client-name

`element-w-owner-consent-client-name` · element · scope: ui-w-owner-consent

The client name control or displayed value in the owner consent widget.

<a id="element-w-owner-consent-callback-destination"></a>
## callback-destination

`element-w-owner-consent-callback-destination` · element · scope: ui-w-owner-consent

The callback destination control or displayed value in the owner consent widget.

<a id="element-w-owner-consent-owner-access-warning"></a>
## owner-access-warning

`element-w-owner-consent-owner-access-warning` · element · scope: ui-w-owner-consent

The owner access warning control or displayed value in the owner consent widget.

<a id="element-w-owner-consent-allow"></a>
## allow

`element-w-owner-consent-allow` · element · scope: ui-w-owner-consent

The allow control or displayed value in the owner consent widget.

<a id="element-w-owner-consent-deny"></a>
## deny

`element-w-owner-consent-deny` · element · scope: ui-w-owner-consent

The deny control or displayed value in the owner consent widget.

<a id="ui-w-local-tunnel-key"></a>
## Local tunnel key widget

`ui-w-local-tunnel-key` · ui-widget

The local tunnel key widget within the application interface.

<a id="element-w-local-tunnel-key-heading"></a>
## heading

`element-w-local-tunnel-key-heading` · element · scope: ui-w-local-tunnel-key

The heading control or displayed value in the local tunnel key widget.

<a id="element-w-local-tunnel-key-restricted-key-help"></a>
## restricted-key-help

`element-w-local-tunnel-key-restricted-key-help` · element · scope: ui-w-local-tunnel-key

The restricted key help control or displayed value in the local tunnel key widget.

<a id="element-w-local-tunnel-key-key"></a>
## key

`element-w-local-tunnel-key-key` · element · scope: ui-w-local-tunnel-key

The key control or displayed value in the local tunnel key widget.

<a id="element-w-local-tunnel-key-save-and-start"></a>
## save-and-start

`element-w-local-tunnel-key-save-and-start` · element · scope: ui-w-local-tunnel-key

The save and start control or displayed value in the local tunnel key widget.

<a id="element-w-local-tunnel-key-status"></a>
## status

`element-w-local-tunnel-key-status` · element · scope: ui-w-local-tunnel-key

The status control or displayed value in the local tunnel key widget.

<a id="ui-w-browser-lab"></a>
## Browser lab widget

`ui-w-browser-lab` · ui-widget

The browser lab widget within the application interface.

<a id="element-w-browser-lab-sign-in-boundary-notice"></a>
## sign-in-boundary-notice

`element-w-browser-lab-sign-in-boundary-notice` · element · scope: ui-w-browser-lab

The sign in boundary notice control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-screenshot"></a>
## screenshot

`element-w-browser-lab-screenshot` · element · scope: ui-w-browser-lab

The screenshot control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-location"></a>
## location

`element-w-browser-lab-location` · element · scope: ui-w-browser-lab

The location control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-url"></a>
## url

`element-w-browser-lab-url` · element · scope: ui-w-browser-lab

The url control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-navigate"></a>
## navigate

`element-w-browser-lab-navigate` · element · scope: ui-w-browser-lab

The navigate control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-refresh"></a>
## refresh

`element-w-browser-lab-refresh` · element · scope: ui-w-browser-lab

The refresh control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-text-to-send"></a>
## text-to-send

`element-w-browser-lab-text-to-send` · element · scope: ui-w-browser-lab

The text to send control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-send-text"></a>
## send-text

`element-w-browser-lab-send-text` · element · scope: ui-w-browser-lab

The send text control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-key-controls"></a>
## key-controls

`element-w-browser-lab-key-controls` · element · scope: ui-w-browser-lab

The key controls control or displayed value in the browser lab widget.

<a id="element-w-browser-lab-scroll-up-down"></a>
## scroll-up/down

`element-w-browser-lab-scroll-up-down` · element · scope: ui-w-browser-lab

The upward and downward scroll controls in the browser lab widget.

<a id="element-w-browser-lab-message"></a>
## message

`element-w-browser-lab-message` · element · scope: ui-w-browser-lab

The message control or displayed value in the browser lab widget.

<a id="ui-p-identity"></a>
## Identity pane

`ui-p-identity` · ui-pane

The identity pane within the application interface.

<a id="ui-p-navigation"></a>
## Navigation pane

`ui-p-navigation` · ui-pane

The navigation pane within the application interface.

<a id="ui-p-feedback"></a>
## Feedback pane

`ui-p-feedback` · ui-pane

The feedback pane within the application interface.

<a id="ui-p-continuity"></a>
## Continuity pane

`ui-p-continuity` · ui-pane

The continuity pane within the application interface.

<a id="ui-p-inbox-query"></a>
## Inbox query pane

`ui-p-inbox-query` · ui-pane

The inbox query pane within the application interface.

<a id="ui-p-inbox-list"></a>
## Inbox list pane

`ui-p-inbox-list` · ui-pane

The inbox list pane within the application interface.

<a id="ui-p-item-detail"></a>
## Item detail pane

`ui-p-item-detail` · ui-pane

The item detail pane within the application interface.

<a id="ui-p-run-list"></a>
## Run list pane

`ui-p-run-list` · ui-pane

The run list pane within the application interface.

<a id="ui-p-run-detail"></a>
## Run detail pane

`ui-p-run-detail` · ui-pane

The run detail pane within the application interface.

<a id="ui-p-repositories"></a>
## Repositories pane

`ui-p-repositories` · ui-pane

The repositories pane within the application interface.

<a id="ui-p-policies"></a>
## Policies pane

`ui-p-policies` · ui-pane

The policies pane within the application interface.

<a id="ui-p-profiles"></a>
## Profiles pane

`ui-p-profiles` · ui-pane

The profiles pane within the application interface.

<a id="ui-p-skills"></a>
## Skills pane

`ui-p-skills` · ui-pane

The skills pane within the application interface.

<a id="ui-p-diagnostics"></a>
## Diagnostics pane

`ui-p-diagnostics` · ui-pane

The diagnostics pane within the application interface.

<a id="ui-p-launch-frame"></a>
## Launch frame pane

`ui-p-launch-frame` · ui-pane

The launch frame pane within the application interface.

<a id="ui-p-launch-editor"></a>
## Launch editor pane

`ui-p-launch-editor` · ui-pane

The launch editor pane within the application interface.

<a id="ui-p-launch-review"></a>
## Launch review pane

`ui-p-launch-review` · ui-pane

The launch review pane within the application interface.

<a id="ui-p-attention-frame"></a>
## Attention frame pane

`ui-p-attention-frame` · ui-pane

The attention frame pane within the application interface.

<a id="ui-p-questions"></a>
## Questions pane

`ui-p-questions` · ui-pane

The questions pane within the application interface.

<a id="ui-p-approval"></a>
## Approval pane

`ui-p-approval` · ui-pane

The approval pane within the application interface.

<a id="ui-p-elicitation"></a>
## Elicitation pane

`ui-p-elicitation` · ui-pane

The elicitation pane within the application interface.

<a id="ui-p-package-overview"></a>
## Package overview pane

`ui-p-package-overview` · ui-pane

The package overview pane within the application interface.

<a id="ui-p-connection-guide"></a>
## Connection guide pane

`ui-p-connection-guide` · ui-pane

The connection guide pane within the application interface.

<a id="ui-p-owner-consent"></a>
## Owner consent pane

`ui-p-owner-consent` · ui-pane

The owner consent pane within the application interface.

<a id="ui-p-local-tunnel-key"></a>
## Local tunnel key pane

`ui-p-local-tunnel-key` · ui-pane

The local tunnel key pane within the application interface.

<a id="ui-p-browser-lab"></a>
## Browser lab pane

`ui-p-browser-lab` · ui-pane

The browser lab pane within the application interface.

<a id="ui-page-inbox"></a>
## Inbox page

`ui-page-inbox` · ui-page

The inbox page within the application interface.

<a id="ui-page-agents"></a>
## Agents page

`ui-page-agents` · ui-page

The agents page within the application interface.

<a id="ui-page-settings"></a>
## Settings page

`ui-page-settings` · ui-page

The settings page within the application interface.

<a id="ui-d-launch"></a>
## Launch dialog

`ui-d-launch` · ui-dialog

The launch dialog within the application interface.

<a id="ui-d-attention"></a>
## Attention dialog

`ui-d-attention` · ui-dialog

The attention dialog within the application interface.

<a id="ui-page-package"></a>
## Package page

`ui-page-package` · ui-page

The package page within the application interface.

<a id="ui-page-owner-consent"></a>
## Owner consent page

`ui-page-owner-consent` · ui-page

The owner consent page within the application interface.

<a id="ui-page-local-tunnel-setup"></a>
## Local tunnel setup page

`ui-page-local-tunnel-setup` · ui-page

The local tunnel setup page within the application interface.

<a id="ui-page-browser-lab"></a>
## Browser lab page

`ui-page-browser-lab` · ui-page

The browser lab page within the application interface.

<a id="ui-c-navigate"></a>
## Navigate connection

`ui-c-navigate` · ui-connection

The navigate connection within the application interface.

<a id="ui-c-filter"></a>
## Filter connection

`ui-c-filter` · ui-connection

The filter connection within the application interface.

<a id="ui-c-select-item"></a>
## Select item connection

`ui-c-select-item` · ui-connection

The select item connection within the application interface.

<a id="ui-c-empty-setup"></a>
## Empty setup connection

`ui-c-empty-setup` · ui-connection

The empty setup connection within the application interface.

<a id="ui-c-item-state"></a>
## Item state connection

`ui-c-item-state` · ui-connection

The item state connection within the application interface.

<a id="ui-c-priority"></a>
## Priority connection

`ui-c-priority` · ui-connection

The priority connection within the application interface.

<a id="ui-c-link"></a>
## Link connection

`ui-c-link` · ui-connection

The link connection within the application interface.

<a id="ui-c-launch-open"></a>
## Launch open connection

`ui-c-launch-open` · ui-connection

The launch open connection within the application interface.

<a id="ui-c-launch-profile"></a>
## Launch profile connection

`ui-c-launch-profile` · ui-connection

The launch profile connection within the application interface.

<a id="ui-c-prepare"></a>
## Prepare connection

`ui-c-prepare` · ui-connection

The prepare connection within the application interface.

<a id="ui-c-launch-edit"></a>
## Launch edit connection

`ui-c-launch-edit` · ui-connection

The launch edit connection within the application interface.

<a id="ui-c-start"></a>
## Start connection

`ui-c-start` · ui-connection

The start connection within the application interface.

<a id="ui-c-select-run"></a>
## Select run connection

`ui-c-select-run` · ui-connection

The select run connection within the application interface.

<a id="ui-c-run-action"></a>
## Run action connection

`ui-c-run-action` · ui-connection

The run action connection within the application interface.

<a id="ui-c-follow-up"></a>
## Follow up connection

`ui-c-follow-up` · ui-connection

The follow up connection within the application interface.

<a id="ui-c-run-attention"></a>
## Run attention connection

`ui-c-run-attention` · ui-connection

The run attention connection within the application interface.

<a id="ui-c-global-attention"></a>
## Global attention connection

`ui-c-global-attention` · ui-connection

The global attention connection within the application interface.

<a id="ui-c-attention-chat"></a>
## Attention chat connection

`ui-c-attention-chat` · ui-connection

The attention chat connection within the application interface.

<a id="ui-c-answer"></a>
## Answer connection

`ui-c-answer` · ui-connection

The answer connection within the application interface.

<a id="ui-c-dismiss"></a>
## Dismiss connection

`ui-c-dismiss` · ui-connection

The dismiss connection within the application interface.

<a id="ui-c-repository-save"></a>
## Repository save connection

`ui-c-repository-save` · ui-connection

The repository save connection within the application interface.

<a id="ui-c-discover-skills"></a>
## Discover skills connection

`ui-c-discover-skills` · ui-connection

The discover skills connection within the application interface.

<a id="ui-c-policy-save"></a>
## Policy save connection

`ui-c-policy-save` · ui-connection

The policy save connection within the application interface.

<a id="ui-c-credential-save"></a>
## Credential save connection

`ui-c-credential-save` · ui-connection

The credential save connection within the application interface.

<a id="ui-c-profile-save"></a>
## Profile save connection

`ui-c-profile-save` · ui-connection

The profile save connection within the application interface.

<a id="ui-c-skill-save"></a>
## Skill save connection

`ui-c-skill-save` · ui-connection

The skill save connection within the application interface.

<a id="ui-c-diagnose"></a>
## Diagnose connection

`ui-c-diagnose` · ui-connection

The diagnose connection within the application interface.

<a id="ui-c-state-refresh"></a>
## State refresh connection

`ui-c-state-refresh` · ui-connection

The state refresh connection within the application interface.

<a id="ui-c-transcript"></a>
## Transcript connection

`ui-c-transcript` · ui-connection

The transcript connection within the application interface.

<a id="ui-c-package"></a>
## Package connection

`ui-c-package` · ui-connection

The package connection within the application interface.

<a id="ui-c-consent"></a>
## Consent connection

`ui-c-consent` · ui-connection

The consent connection within the application interface.

<a id="ui-c-external-auth"></a>
## External auth connection

`ui-c-external-auth` · ui-connection

The external auth connection within the application interface.

<a id="ui-c-tunnel-key"></a>
## Tunnel key connection

`ui-c-tunnel-key` · ui-connection

The tunnel key connection within the application interface.

<a id="ui-c-browser-lab"></a>
## Browser lab connection

`ui-c-browser-lab` · ui-connection

The browser lab connection within the application interface.

<a id="question-recovery"></a>
## Question recovery

`question-recovery` · domain

A bounded effort to recover an unanswered human question while preserving the original task and authority.

<a id="state-recovering"></a>
## Recovering

`state-recovering` · state

A pending human question is being checked or restated before task continuation.

<a id="element-w-run-controls-waiting-icon"></a>
## waiting-icon

`element-w-run-controls-waiting-icon` · element · scope: ui-w-run-controls

The decorative clock beside a readable waiting status.

<a id="element-w-run-controls-recovery-progress"></a>
## recovery-progress

`element-w-run-controls-recovery-progress` · element · scope: ui-w-run-controls

The readable status of question recovery and its bounded attempts.

<a id="element-w-run-controls-recovery-icon"></a>
## recovery-icon

`element-w-run-controls-recovery-icon` · element · scope: ui-w-run-controls

The decorative circular arrow beside recovery progress.

<a id="element-w-run-controls-takeover"></a>
## takeover

`element-w-run-controls-takeover` · element · scope: ui-w-run-controls

The control that stops question recovery for owner takeover.

<a id="element-w-run-controls-confirmation"></a>
## confirmation

`element-w-run-controls-confirmation` · element · scope: ui-w-run-controls

The temporary acknowledgement of a successful response in its owning run.

<a id="element-w-attention-notice-waiting-icon"></a>
## waiting-icon

`element-w-attention-notice-waiting-icon` · element · scope: ui-w-attention-notice

The decorative clock beside a readable waiting request label.

<a id="element-w-questions-dismiss"></a>
## dismiss

`element-w-questions-dismiss` · element · scope: ui-w-questions

The control that dismisses a textual question without replying or granting authority.

<a id="element-w-policy-settings-question-helper-model"></a>
## question-helper-model

`element-w-policy-settings-question-helper-model` · element · scope: ui-w-policy-settings

The optional model used to check stopped task questions, with an empty value meaning the task model.

<a id="ui-c-question-recovery"></a>
## Question recovery data connection

`ui-c-question-recovery` · ui-connection

The connection between question recovery progress and its owning question and run.

<a id="ui-c-dismiss-question"></a>
## Question dismissal connection

`ui-c-dismiss-question` · ui-connection

The local dismissal connection for a textual question.

<a id="issue-report"></a>
## Issue report

`issue-report` · domain

A retained observation of suspected defective behavior with its investigation and delivery evidence.

<a id="report-ledger"></a>
## Report ledger

`report-ledger` · domain

The persistent collection of issue reports and their independent histories.

<a id="report-processing"></a>
## Report processing state

`report-processing` · domain

The progress of an observation through investigation, information requests, publication and recovery.

<a id="report-outcome"></a>
## Report outcome

`report-outcome` · domain

The investigation disposition identifying a filed issue, a duplicate or explained behavior.

<a id="evidence-manifest"></a>
## Evidence manifest

`evidence-manifest` · domain

An inventory of evidence origins, capture times, content identities, omissions and truncation.

<a id="delivery-evidence"></a>
## Delivery evidence

`delivery-evidence` · domain

Verified relationships among a fix, its component version and a published package.

<a id="fixed-issue"></a>
## Fixed-issue reference

`fixed-issue` · domain

An explicit association of an issue with the source commit that corrects it.

<a id="element-w-navigation-reports"></a>
## reported issues

`element-w-navigation-reports` · element · scope: ui-w-navigation

The reported issues destination control in the navigation widget.

<a id="ui-page-reports"></a>
## Reported issues page

`ui-page-reports` · ui-page

The page presenting retained issue reports.

<a id="ui-p-report-list"></a>
## Report list pane

`ui-p-report-list` · ui-pane

The pane containing report discovery and filtering controls.

<a id="ui-p-report-detail"></a>
## Report detail pane

`ui-p-report-detail` · ui-pane

The pane presenting the selected report and its recovery controls.

<a id="ui-w-report-list"></a>
## Report list widget

`ui-w-report-list` · ui-widget

The searchable and paginated presentation of retained reports.

<a id="ui-w-report-detail"></a>
## Report detail widget

`ui-w-report-detail` · ui-widget

The presentation of one report, its investigation, evidence and delivery state.

<a id="ui-c-report-select"></a>
## Report selection connection

`ui-c-report-select` · ui-connection

The transition from a report row to its details.

<a id="ui-c-report-evidence"></a>
## Report evidence connection

`ui-c-report-evidence` · ui-connection

The addition of an observation to a retained report.

<a id="ui-c-report-recover"></a>
## Report recovery connection

`ui-c-report-recover` · ui-connection

The explicit recovery of retained report work.

<a id="ui-c-report-refresh"></a>
## Report refresh connection

`ui-c-report-refresh` · ui-connection

The refresh of issue and delivery evidence.
<!-- /specs:terms -->
