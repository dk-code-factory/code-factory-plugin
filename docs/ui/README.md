# UI entity specification

Maintained UI contracts, updated 2026-10-21 for draft preservation and daily workflows. This describes the owned UI without claiming complete visual/accessibility acceptance. The [acceptance ledger](../acceptance.md) records observed results.
Canonical vocabulary lives in the [glossary](../glossary/README.md).

The layout follows [Compact, single-page UI design](design.md).
The entity contracts below describe its implemented pages and controls.

Read in this order:

1. [Widgets and their elements](widgets.md): controls, displays, inputs, outputs
   and conditional states.
2. [Panes](panes.md): related widgets sharing a selected item, task or settings
   context.
3. [Pages and dialogs](pages.md): complete surfaces and their pane composition.
4. [Connections](connections.md): selection, navigation, submission, refresh and
   external transitions, including state ownership.
5. [Implementation and test references](references.md): source locations, existing
   assertions and explicit gaps.

## Entity model

| Entity | Definition | Identity and relationships |
| --- | --- | --- |
| Element | One named input, action, label, indicator or repeated display field within a widget. | Scoped to its widget, for example `W-INBOX-QUERY.search`. An element is not necessarily one DOM node. |
| Widget | A cohesive control or display with a defined purpose, element inventory and interaction contract. | Stable `W-*` type ID. Repeated instances use a record key, for example `W-ITEM-ROW[itemId]`. A widget type may appear in several panes. |
| Pane | A group of widgets that share a task or selected-record context. | Stable `P-*` ID. It can remain present while its widgets display loading, empty, ready or error states. |
| Page | A complete logical task surface with a pane layout and entry/exit connections. | Stable `PAGE-*` ID. The embedded app's tabs are pages even though they do not have separate browser routes. |
| Dialog | A temporary task surface above a page, with its own panes and close/submit transitions. | Stable `D-*` ID. Launch and attention are dialogs, not extra navigation pages. |
| Shell | An independently opened application surface with its pages and feedback. | `S-FACTORY` for inbox/agents; `S-SETTINGS` for configuration. The host calls the complete embedded app a widget; this specification reserves `W-*` for controls/displays inside it. |

Containment is `page/dialog → pane → widget instance → named elements`. Shared
compact navigation and feedback accompany the active page. Each widget instance belongs
to its displayed pane; reuse of a widget type does not imply shared draft state.
These IDs are specification IDs, not existing HTML IDs or component names.

Structural prose specifies visible meaning and interaction. Framework components,
transport methods and test locations live in [references](references.md). A logical
widget can be inline in a larger component; this catalog does not require a code
refactor into one component per entity.

## Useful charts

Two diagrams are useful and answer different questions:

- A **containment diagram** answers “what is this screen made of?” Keep the full
  inventory in tables and expand only representative widget elements in the chart.
- A **navigation/state diagram** answers “what opens, changes or submits when the
  user acts?” It should distinguish selecting a pane, opening a dialog and changing
  a page. The [connection catalog](connections.md) contains that diagram and a
  launch phase boundaries.

A sequence diagram is useful for a particular round trip, such as answering a
question, when discussing UI/service timing. It is not necessary for the overall
entity inventory. Numeric charts, such as bar or pie charts, do not describe these
relationships well. Mermaid keeps these structural diagrams editable in the repo.

<!-- specs:containment -->
```mermaid
flowchart TD
    N0["S-FACTORY"]
    N1["W-IDENTITY"]
    N2["W-NAVIGATION"]
    N3["W-GLOBAL-ERROR"]
    N4["W-CONNECTION-BLOCKER"]
    N5["W-INBOX-QUERY"]
    N6["W-SYNC-HEALTH"]
    N7["W-ITEM-ROW"]
    N8["W-INBOX-EMPTY"]
    N9["W-ITEM-OVERVIEW"]
    N10["W-ITEM-ACTIONS"]
    N11["W-PRIORITY"]
    N12["W-RELATIONSHIPS"]
    N13["W-TICKET-CONTEXT"]
    N14["W-PR-EVIDENCE"]
    N15["W-ITEM-HISTORY"]
    N16["W-RUN-ROW"]
    N17["W-RUN-EMPTY"]
    N18["W-RUN-CONTROLS"]
    N19["W-ATTENTION-NOTICE"]
    N20["W-TRANSCRIPT"]
    N21["W-MESSAGE"]
    N22["W-DIALOG-FRAME"]
    N23["W-LAUNCH-EDITOR"]
    N24["W-LAUNCH-REVIEW"]
    N25["W-QUESTIONS"]
    N26["W-APPROVAL"]
    N27["W-ELICITATION"]
    N28["W-REPOSITORY-SETTINGS"]
    N29["W-POLICY-SETTINGS"]
    N30["W-PROFILE-SETTINGS"]
    N31["W-SKILL-SETTINGS"]
    N32["W-DIAGNOSTICS"]
    N33["W-PACKAGE-OVERVIEW"]
    N34["W-CONNECTION-GUIDE"]
    N35["W-OWNER-CONSENT"]
    N36["W-LOCAL-TUNNEL-KEY"]
    N37["W-BROWSER-LAB"]
    N38["P-IDENTITY"]
    N39["P-NAVIGATION"]
    N40["P-FEEDBACK"]
    N41["P-INBOX-QUERY"]
    N42["P-INBOX-LIST"]
    N43["P-ITEM-DETAIL"]
    N44["P-RUN-LIST"]
    N45["P-RUN-DETAIL"]
    N46["P-REPOSITORIES"]
    N47["P-POLICIES"]
    N48["P-PROFILES"]
    N49["P-SKILLS"]
    N50["P-DIAGNOSTICS"]
    N51["P-LAUNCH-FRAME"]
    N52["P-LAUNCH-EDITOR"]
    N53["P-LAUNCH-REVIEW"]
    N54["P-ATTENTION-FRAME"]
    N55["P-QUESTIONS"]
    N56["P-APPROVAL"]
    N57["P-ELICITATION"]
    N58["P-PACKAGE-OVERVIEW"]
    N59["P-CONNECTION-GUIDE"]
    N60["P-OWNER-CONSENT"]
    N61["P-LOCAL-TUNNEL-KEY"]
    N62["P-BROWSER-LAB"]
    N63["PAGE-INBOX"]
    N64["PAGE-AGENTS"]
    N65["PAGE-SETTINGS"]
    N66["D-LAUNCH"]
    N67["D-ATTENTION"]
    N68["PAGE-PACKAGE"]
    N69["PAGE-OWNER-CONSENT"]
    N70["PAGE-LOCAL-TUNNEL-SETUP"]
    N71["PAGE-BROWSER-LAB"]
    N104["PAGE-AGENT"]
    N107["S-SETTINGS"]
    N109["PAGE-MY-PRS"]
    N110["PAGE-REVIEW-PRS"]
    N111["P-WAITING-QUEUE"]
    N112["P-NEW-CHAT"]
    N113["W-WAITING-ROW"]
    N114["W-NEW-CHAT"]
    N116["W-WORK-STATUS"]
    N117["PAGE-WORKING"]
    N118["W-SESSION-CONSOLE"]
    N119["W-QUESTION-RECOVERY"]
    N0 --> N38
    N0 --> N39
    N0 --> N40
    N0 --> N63
    N0 --> N64
    N0 --> N66
    N0 --> N67
    N0 --> N104
    N0 --> N109
    N0 --> N110
    N0 --> N117
    N63 --> N41
    N63 --> N42
    N64 --> N111
    N65 --> N46
    N65 --> N47
    N65 --> N48
    N65 --> N49
    N65 --> N50
    N66 --> N51
    N66 --> N52
    N66 --> N53
    N67 --> N54
    N67 --> N55
    N67 --> N56
    N67 --> N57
    N68 --> N58
    N68 --> N59
    N69 --> N60
    N70 --> N61
    N71 --> N62
    N104 --> N43
    N104 --> N45
    N104 --> N112
    N104 --> N44
    N107 --> N38
    N107 --> N40
    N107 --> N65
    N109 --> N41
    N109 --> N42
    N110 --> N41
    N110 --> N42
    N117 --> N41
    N117 --> N42
    I0["P-IDENTITY/W-IDENTITY"]
    N38 --> I0
    I0 -.->|"type"| N1
    I1["P-NAVIGATION/W-NAVIGATION"]
    N39 --> I1
    I1 -.->|"type"| N2
    I2["P-FEEDBACK/W-GLOBAL-ERROR"]
    N40 --> I2
    I2 -.->|"type"| N3
    I3["P-FEEDBACK/W-CONNECTION-BLOCKER"]
    N40 --> I3
    I3 -.->|"type"| N4
    I4["P-INBOX-QUERY/W-INBOX-QUERY"]
    N41 --> I4
    I4 -.->|"type"| N5
    I5["P-INBOX-QUERY/W-SYNC-HEALTH"]
    N41 --> I5
    I5 -.->|"type"| N6
    I6["P-INBOX-LIST/W-ITEM-ROW"]
    N42 --> I6
    I6 -.->|"type"| N7
    I7["P-INBOX-LIST/W-INBOX-EMPTY"]
    N42 --> I7
    I7 -.->|"type"| N8
    I8["P-ITEM-DETAIL/W-ITEM-OVERVIEW"]
    N43 --> I8
    I8 -.->|"type"| N9
    I9["P-ITEM-DETAIL/W-ITEM-ACTIONS"]
    N43 --> I9
    I9 -.->|"type"| N10
    I10["P-ITEM-DETAIL/W-PRIORITY"]
    N43 --> I10
    I10 -.->|"type"| N11
    I11["P-ITEM-DETAIL/W-RELATIONSHIPS"]
    N43 --> I11
    I11 -.->|"type"| N12
    I12["P-ITEM-DETAIL/W-TICKET-CONTEXT"]
    N43 --> I12
    I12 -.->|"type"| N13
    I13["P-ITEM-DETAIL/W-PR-EVIDENCE"]
    N43 --> I13
    I13 -.->|"type"| N14
    I14["P-ITEM-DETAIL/W-ITEM-HISTORY"]
    N43 --> I14
    I14 -.->|"type"| N15
    I15["P-RUN-LIST/W-RUN-ROW"]
    N44 --> I15
    I15 -.->|"type"| N16
    I16["P-RUN-LIST/W-RUN-EMPTY"]
    N44 --> I16
    I16 -.->|"type"| N17
    I17["P-RUN-DETAIL/W-RUN-CONTROLS"]
    N45 --> I17
    I17 -.->|"type"| N18
    I18["P-RUN-DETAIL/W-ATTENTION-NOTICE"]
    N45 --> I18
    I18 -.->|"type"| N19
    I19["P-RUN-DETAIL/W-TRANSCRIPT"]
    N45 --> I19
    I19 -.->|"type"| N20
    I20["P-RUN-DETAIL/W-MESSAGE"]
    N45 --> I20
    I20 -.->|"type"| N21
    I21["P-RUN-DETAIL/W-RUN-EMPTY"]
    N45 --> I21
    I21 -.->|"type"| N17
    I22["P-REPOSITORIES/W-REPOSITORY-SETTINGS"]
    N46 --> I22
    I22 -.->|"type"| N28
    I23["P-POLICIES/W-POLICY-SETTINGS"]
    N47 --> I23
    I23 -.->|"type"| N29
    I24["P-PROFILES/W-PROFILE-SETTINGS"]
    N48 --> I24
    I24 -.->|"type"| N30
    I25["P-SKILLS/W-SKILL-SETTINGS"]
    N49 --> I25
    I25 -.->|"type"| N31
    I26["P-DIAGNOSTICS/W-DIAGNOSTICS"]
    N50 --> I26
    I26 -.->|"type"| N32
    I27["P-LAUNCH-FRAME/W-DIALOG-FRAME"]
    N51 --> I27
    I27 -.->|"type"| N22
    I28["P-LAUNCH-EDITOR/W-LAUNCH-EDITOR"]
    N52 --> I28
    I28 -.->|"type"| N23
    I29["P-LAUNCH-REVIEW/W-LAUNCH-REVIEW"]
    N53 --> I29
    I29 -.->|"type"| N24
    I30["P-ATTENTION-FRAME/W-DIALOG-FRAME"]
    N54 --> I30
    I30 -.->|"type"| N22
    I31["P-QUESTIONS/W-QUESTIONS"]
    N55 --> I31
    I31 -.->|"type"| N25
    I32["P-APPROVAL/W-APPROVAL"]
    N56 --> I32
    I32 -.->|"type"| N26
    I33["P-ELICITATION/W-ELICITATION"]
    N57 --> I33
    I33 -.->|"type"| N27
    I34["P-ELICITATION/W-APPROVAL"]
    N57 --> I34
    I34 -.->|"type"| N26
    I35["P-PACKAGE-OVERVIEW/W-PACKAGE-OVERVIEW"]
    N58 --> I35
    I35 -.->|"type"| N33
    I36["P-CONNECTION-GUIDE/W-CONNECTION-GUIDE"]
    N59 --> I36
    I36 -.->|"type"| N34
    I37["P-OWNER-CONSENT/W-OWNER-CONSENT"]
    N60 --> I37
    I37 -.->|"type"| N35
    I38["P-LOCAL-TUNNEL-KEY/W-LOCAL-TUNNEL-KEY"]
    N61 --> I38
    I38 -.->|"type"| N36
    I39["P-BROWSER-LAB/W-BROWSER-LAB"]
    N62 --> I39
    I39 -.->|"type"| N37
    I40["P-ITEM-DETAIL/W-NAVIGATION-back"]
    N43 --> I40
    I40 -.->|"type"| N2
    I41["P-RUN-DETAIL/W-NAVIGATION-back"]
    N45 --> I41
    I41 -.->|"type"| N2
    I42["P-WAITING-QUEUE/W-WAITING-ROW"]
    N111 --> I42
    I42 -.->|"type"| N113
    I43["P-NEW-CHAT/W-NEW-CHAT"]
    N112 --> I43
    I43 -.->|"type"| N114
    I44["P-INBOX-LIST/W-WORK-STATUS"]
    N42 --> I44
    I44 -.->|"type"| N116
    I45["P-ITEM-DETAIL/W-WORK-STATUS"]
    N43 --> I45
    I45 -.->|"type"| N116
    I46["P-RUN-DETAIL/W-WORK-STATUS"]
    N45 --> I46
    I46 -.->|"type"| N116
    I47["P-INBOX-LIST/W-PR-EVIDENCE"]
    N42 --> I47
    I47 -.->|"type"| N14
    I48["P-RUN-DETAIL/W-SESSION-CONSOLE"]
    N45 --> I48
    I48 -.->|"type"| N118
    I49["P-RUN-DETAIL/W-QUESTION-RECOVERY"]
    N45 --> I49
    I49 -.->|"type"| N119
```
<!-- /specs:containment -->

Solid arrows show containment. Dotted type arrows connect each displayed
instance to its reusable widget type. Dialogs belong to the shell; auxiliary
pages remain separate roots. Connections have their own relationship diagram.

## Maintenance boundary

For a UI change, reconcile the affected widget's elements and states, its pane
membership, page/dialog placement, and connection endpoints. Keep stable IDs when
the purpose remains the same. Retire an ID when the entity is actually removed;
update its incoming/outgoing connections and test references together.

The [entity registry](entities.json) owns the inventory and generated tables.
Run `python3 scripts/specs.py render` after edits and
`python3 scripts/specs.py validate` to check structure, references and output.
UI specifications depend on canonical terminology; higher-level behavior is
maintained by consumers. Coverage tables remain independent.

Only source inspection and documentation checks were needed for this baseline.
No installed UI, authentication, service or browser-session change is implied.

## Displayed widget instances

<!-- specs:instances -->
| Instance | Widget type | Pane | Multiplicity/context |
| --- | --- | --- | --- |
| `P-IDENTITY/W-IDENTITY` | `W-IDENTITY` | `P-IDENTITY` | one per displayed pane |
| `P-NAVIGATION/W-NAVIGATION` | `W-NAVIGATION` | `P-NAVIGATION` | one per displayed pane |
| `P-FEEDBACK/W-GLOBAL-ERROR` | `W-GLOBAL-ERROR` | `P-FEEDBACK` | one per displayed pane |
| `P-FEEDBACK/W-CONNECTION-BLOCKER` | `W-CONNECTION-BLOCKER` | `P-FEEDBACK` | one per displayed pane |
| `P-INBOX-QUERY/W-INBOX-QUERY` | `W-INBOX-QUERY` | `P-INBOX-QUERY` | one per displayed pane |
| `P-INBOX-QUERY/W-SYNC-HEALTH` | `W-SYNC-HEALTH` | `P-INBOX-QUERY` | one per displayed pane |
| `P-INBOX-LIST/W-ITEM-ROW` | `W-ITEM-ROW` | `P-INBOX-LIST` | item ID |
| `P-INBOX-LIST/W-INBOX-EMPTY` | `W-INBOX-EMPTY` | `P-INBOX-LIST` | one per displayed pane |
| `P-ITEM-DETAIL/W-ITEM-OVERVIEW` | `W-ITEM-OVERVIEW` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-ITEM-ACTIONS` | `W-ITEM-ACTIONS` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-PRIORITY` | `W-PRIORITY` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-RELATIONSHIPS` | `W-RELATIONSHIPS` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-TICKET-CONTEXT` | `W-TICKET-CONTEXT` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-PR-EVIDENCE` | `W-PR-EVIDENCE` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-ITEM-DETAIL/W-ITEM-HISTORY` | `W-ITEM-HISTORY` | `P-ITEM-DETAIL` | one per displayed pane |
| `P-RUN-LIST/W-RUN-ROW` | `W-RUN-ROW` | `P-RUN-LIST` | run ID |
| `P-RUN-LIST/W-RUN-EMPTY` | `W-RUN-EMPTY` | `P-RUN-LIST` | one per displayed pane |
| `P-RUN-DETAIL/W-RUN-CONTROLS` | `W-RUN-CONTROLS` | `P-RUN-DETAIL` | one per displayed pane |
| `P-RUN-DETAIL/W-ATTENTION-NOTICE` | `W-ATTENTION-NOTICE` | `P-RUN-DETAIL` | request ID |
| `P-RUN-DETAIL/W-TRANSCRIPT` | `W-TRANSCRIPT` | `P-RUN-DETAIL` | one per displayed pane |
| `P-RUN-DETAIL/W-MESSAGE` | `W-MESSAGE` | `P-RUN-DETAIL` | one per displayed pane |
| `P-RUN-DETAIL/W-RUN-EMPTY` | `W-RUN-EMPTY` | `P-RUN-DETAIL` | one per displayed pane |
| `P-REPOSITORIES/W-REPOSITORY-SETTINGS` | `W-REPOSITORY-SETTINGS` | `P-REPOSITORIES` | one per displayed pane |
| `P-POLICIES/W-POLICY-SETTINGS` | `W-POLICY-SETTINGS` | `P-POLICIES` | one per displayed pane |
| `P-PROFILES/W-PROFILE-SETTINGS` | `W-PROFILE-SETTINGS` | `P-PROFILES` | one per displayed pane |
| `P-SKILLS/W-SKILL-SETTINGS` | `W-SKILL-SETTINGS` | `P-SKILLS` | one per displayed pane |
| `P-DIAGNOSTICS/W-DIAGNOSTICS` | `W-DIAGNOSTICS` | `P-DIAGNOSTICS` | one per displayed pane |
| `P-LAUNCH-FRAME/W-DIALOG-FRAME` | `W-DIALOG-FRAME` | `P-LAUNCH-FRAME` | one per displayed pane |
| `P-LAUNCH-EDITOR/W-LAUNCH-EDITOR` | `W-LAUNCH-EDITOR` | `P-LAUNCH-EDITOR` | one per displayed pane |
| `P-LAUNCH-REVIEW/W-LAUNCH-REVIEW` | `W-LAUNCH-REVIEW` | `P-LAUNCH-REVIEW` | one per displayed pane |
| `P-ATTENTION-FRAME/W-DIALOG-FRAME` | `W-DIALOG-FRAME` | `P-ATTENTION-FRAME` | one per displayed pane |
| `P-QUESTIONS/W-QUESTIONS` | `W-QUESTIONS` | `P-QUESTIONS` | one per displayed pane |
| `P-APPROVAL/W-APPROVAL` | `W-APPROVAL` | `P-APPROVAL` | one per displayed pane |
| `P-ELICITATION/W-ELICITATION` | `W-ELICITATION` | `P-ELICITATION` | one per displayed pane |
| `P-ELICITATION/W-APPROVAL` | `W-APPROVAL` | `P-ELICITATION` | one per displayed pane |
| `P-PACKAGE-OVERVIEW/W-PACKAGE-OVERVIEW` | `W-PACKAGE-OVERVIEW` | `P-PACKAGE-OVERVIEW` | one per displayed pane |
| `P-CONNECTION-GUIDE/W-CONNECTION-GUIDE` | `W-CONNECTION-GUIDE` | `P-CONNECTION-GUIDE` | one per displayed pane |
| `P-OWNER-CONSENT/W-OWNER-CONSENT` | `W-OWNER-CONSENT` | `P-OWNER-CONSENT` | one per displayed pane |
| `P-LOCAL-TUNNEL-KEY/W-LOCAL-TUNNEL-KEY` | `W-LOCAL-TUNNEL-KEY` | `P-LOCAL-TUNNEL-KEY` | one per displayed pane |
| `P-BROWSER-LAB/W-BROWSER-LAB` | `W-BROWSER-LAB` | `P-BROWSER-LAB` | one per displayed pane |
| `P-ITEM-DETAIL/W-NAVIGATION-back` | `W-NAVIGATION` | `P-ITEM-DETAIL` | one contextual Back-only instance on the focused record page |
| `P-RUN-DETAIL/W-NAVIGATION-back` | `W-NAVIGATION` | `P-RUN-DETAIL` | one contextual Back-only instance on the focused record page |
| `P-WAITING-QUEUE/W-WAITING-ROW` | `W-WAITING-ROW` | `P-WAITING-QUEUE` | one per pending request |
| `P-NEW-CHAT/W-NEW-CHAT` | `W-NEW-CHAT` | `P-NEW-CHAT` | one per item without retained work |
| `P-INBOX-LIST/W-WORK-STATUS` | `W-WORK-STATUS` | `P-INBOX-LIST` | item ID |
| `P-ITEM-DETAIL/W-WORK-STATUS` | `W-WORK-STATUS` | `P-ITEM-DETAIL` | selected item |
| `P-RUN-DETAIL/W-WORK-STATUS` | `W-WORK-STATUS` | `P-RUN-DETAIL` | fallback conversation without a retained item |
| `P-INBOX-LIST/W-PR-EVIDENCE` | `W-PR-EVIDENCE` | `P-INBOX-LIST` | per PR or linked PR summary in list metadata |
| `P-RUN-DETAIL/W-SESSION-CONSOLE` | `W-SESSION-CONSOLE` | `P-RUN-DETAIL` | one per displayed retained session |
| `P-RUN-DETAIL/W-QUESTION-RECOVERY` | `W-QUESTION-RECOVERY` | `P-RUN-DETAIL` | one per displayed retained session |
<!-- /specs:instances -->

## Canonical entity names

<!-- specs:terminology -->
| Entity | Kind | Canonical glossary name |
| --- | --- | --- |
| <a id="S-FACTORY"></a>`S-FACTORY` | shell | [Factory shell](../glossary/README.md#ui-s-factory), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-IDENTITY"></a>`W-IDENTITY` | widget | [Identity widget](../glossary/README.md#ui-w-identity), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-NAVIGATION"></a>`W-NAVIGATION` | widget | [Navigation widget](../glossary/README.md#ui-w-navigation), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-GLOBAL-ERROR"></a>`W-GLOBAL-ERROR` | widget | [Global error widget](../glossary/README.md#ui-w-global-error), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-CONNECTION-BLOCKER"></a>`W-CONNECTION-BLOCKER` | widget | [Connection blocker widget](../glossary/README.md#ui-w-connection-blocker), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-INBOX-QUERY"></a>`W-INBOX-QUERY` | widget | [Inbox query widget](../glossary/README.md#ui-w-inbox-query), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-SYNC-HEALTH"></a>`W-SYNC-HEALTH` | widget | [Sync health widget](../glossary/README.md#ui-w-sync-health), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ITEM-ROW"></a>`W-ITEM-ROW` | widget | [Item row widget](../glossary/README.md#ui-w-item-row), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-INBOX-EMPTY"></a>`W-INBOX-EMPTY` | widget | [Inbox empty widget](../glossary/README.md#ui-w-inbox-empty), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ITEM-OVERVIEW"></a>`W-ITEM-OVERVIEW` | widget | [Item overview widget](../glossary/README.md#ui-w-item-overview), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ITEM-ACTIONS"></a>`W-ITEM-ACTIONS` | widget | [Item actions widget](../glossary/README.md#ui-w-item-actions), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-PRIORITY"></a>`W-PRIORITY` | widget | [Priority widget](../glossary/README.md#ui-w-priority), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-RELATIONSHIPS"></a>`W-RELATIONSHIPS` | widget | [Relationships widget](../glossary/README.md#ui-w-relationships), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-TICKET-CONTEXT"></a>`W-TICKET-CONTEXT` | widget | [Ticket context widget](../glossary/README.md#ui-w-ticket-context), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-PR-EVIDENCE"></a>`W-PR-EVIDENCE` | widget | [Pr evidence widget](../glossary/README.md#ui-w-pr-evidence), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ITEM-HISTORY"></a>`W-ITEM-HISTORY` | widget | [Item history widget](../glossary/README.md#ui-w-item-history), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-RUN-ROW"></a>`W-RUN-ROW` | widget | [Run row widget](../glossary/README.md#ui-w-run-row), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-RUN-EMPTY"></a>`W-RUN-EMPTY` | widget | [Run empty widget](../glossary/README.md#ui-w-run-empty), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-RUN-CONTROLS"></a>`W-RUN-CONTROLS` | widget | [Run controls widget](../glossary/README.md#ui-w-run-controls), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ATTENTION-NOTICE"></a>`W-ATTENTION-NOTICE` | widget | [Attention notice widget](../glossary/README.md#ui-w-attention-notice), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-TRANSCRIPT"></a>`W-TRANSCRIPT` | widget | [Transcript widget](../glossary/README.md#ui-w-transcript), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-MESSAGE"></a>`W-MESSAGE` | widget | [Message widget](../glossary/README.md#ui-w-message), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-DIALOG-FRAME"></a>`W-DIALOG-FRAME` | widget | [Dialog frame widget](../glossary/README.md#ui-w-dialog-frame), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior), [Keyboard focus](../glossary/README.md#keyboard-focus), [Modal dialog](../glossary/README.md#modal-dialog) |
| <a id="W-LAUNCH-EDITOR"></a>`W-LAUNCH-EDITOR` | widget | [Launch editor widget](../glossary/README.md#ui-w-launch-editor), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-LAUNCH-REVIEW"></a>`W-LAUNCH-REVIEW` | widget | [Launch review widget](../glossary/README.md#ui-w-launch-review), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-QUESTIONS"></a>`W-QUESTIONS` | widget | [Questions widget](../glossary/README.md#ui-w-questions), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-APPROVAL"></a>`W-APPROVAL` | widget | [Approval widget](../glossary/README.md#ui-w-approval), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-ELICITATION"></a>`W-ELICITATION` | widget | [Elicitation widget](../glossary/README.md#ui-w-elicitation), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-REPOSITORY-SETTINGS"></a>`W-REPOSITORY-SETTINGS` | widget | [Repository settings widget](../glossary/README.md#ui-w-repository-settings), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-POLICY-SETTINGS"></a>`W-POLICY-SETTINGS` | widget | [Policy settings widget](../glossary/README.md#ui-w-policy-settings), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-PROFILE-SETTINGS"></a>`W-PROFILE-SETTINGS` | widget | [Profile settings widget](../glossary/README.md#ui-w-profile-settings), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-SKILL-SETTINGS"></a>`W-SKILL-SETTINGS` | widget | [Skill settings widget](../glossary/README.md#ui-w-skill-settings), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-DIAGNOSTICS"></a>`W-DIAGNOSTICS` | widget | [Diagnostics widget](../glossary/README.md#ui-w-diagnostics), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-PACKAGE-OVERVIEW"></a>`W-PACKAGE-OVERVIEW` | widget | [Package overview widget](../glossary/README.md#ui-w-package-overview), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-CONNECTION-GUIDE"></a>`W-CONNECTION-GUIDE` | widget | [Connection guide widget](../glossary/README.md#ui-w-connection-guide), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-OWNER-CONSENT"></a>`W-OWNER-CONSENT` | widget | [Owner consent widget](../glossary/README.md#ui-w-owner-consent), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="W-LOCAL-TUNNEL-KEY"></a>`W-LOCAL-TUNNEL-KEY` | widget | [Local tunnel key widget](../glossary/README.md#ui-w-local-tunnel-key), [Coverage gap](../glossary/README.md#coverage-gap), [Known limitation](../glossary/README.md#known-limitation) |
| <a id="W-BROWSER-LAB"></a>`W-BROWSER-LAB` | widget | [Browser lab widget](../glossary/README.md#ui-w-browser-lab), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-IDENTITY"></a>`P-IDENTITY` | pane | [Identity pane](../glossary/README.md#ui-p-identity), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-NAVIGATION"></a>`P-NAVIGATION` | pane | [Navigation pane](../glossary/README.md#ui-p-navigation), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-FEEDBACK"></a>`P-FEEDBACK` | pane | [Feedback pane](../glossary/README.md#ui-p-feedback), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-INBOX-QUERY"></a>`P-INBOX-QUERY` | pane | [Inbox query pane](../glossary/README.md#ui-p-inbox-query), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-INBOX-LIST"></a>`P-INBOX-LIST` | pane | [Inbox list pane](../glossary/README.md#ui-p-inbox-list), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-ITEM-DETAIL"></a>`P-ITEM-DETAIL` | pane | [Item detail pane](../glossary/README.md#ui-p-item-detail), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-RUN-LIST"></a>`P-RUN-LIST` | pane | [Run list pane](../glossary/README.md#ui-p-run-list), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-RUN-DETAIL"></a>`P-RUN-DETAIL` | pane | [Run detail pane](../glossary/README.md#ui-p-run-detail), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-REPOSITORIES"></a>`P-REPOSITORIES` | pane | [Repositories pane](../glossary/README.md#ui-p-repositories), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-POLICIES"></a>`P-POLICIES` | pane | [Policies pane](../glossary/README.md#ui-p-policies), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-PROFILES"></a>`P-PROFILES` | pane | [Profiles pane](../glossary/README.md#ui-p-profiles), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-SKILLS"></a>`P-SKILLS` | pane | [Skills pane](../glossary/README.md#ui-p-skills), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-DIAGNOSTICS"></a>`P-DIAGNOSTICS` | pane | [Diagnostics pane](../glossary/README.md#ui-p-diagnostics), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-LAUNCH-FRAME"></a>`P-LAUNCH-FRAME` | pane | [Launch frame pane](../glossary/README.md#ui-p-launch-frame), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-LAUNCH-EDITOR"></a>`P-LAUNCH-EDITOR` | pane | [Launch editor pane](../glossary/README.md#ui-p-launch-editor), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-LAUNCH-REVIEW"></a>`P-LAUNCH-REVIEW` | pane | [Launch review pane](../glossary/README.md#ui-p-launch-review), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-ATTENTION-FRAME"></a>`P-ATTENTION-FRAME` | pane | [Attention frame pane](../glossary/README.md#ui-p-attention-frame), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-QUESTIONS"></a>`P-QUESTIONS` | pane | [Questions pane](../glossary/README.md#ui-p-questions), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-APPROVAL"></a>`P-APPROVAL` | pane | [Approval pane](../glossary/README.md#ui-p-approval), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-ELICITATION"></a>`P-ELICITATION` | pane | [Elicitation pane](../glossary/README.md#ui-p-elicitation), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-PACKAGE-OVERVIEW"></a>`P-PACKAGE-OVERVIEW` | pane | [Package overview pane](../glossary/README.md#ui-p-package-overview), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-CONNECTION-GUIDE"></a>`P-CONNECTION-GUIDE` | pane | [Connection guide pane](../glossary/README.md#ui-p-connection-guide), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-OWNER-CONSENT"></a>`P-OWNER-CONSENT` | pane | [Owner consent pane](../glossary/README.md#ui-p-owner-consent), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="P-LOCAL-TUNNEL-KEY"></a>`P-LOCAL-TUNNEL-KEY` | pane | [Local tunnel key pane](../glossary/README.md#ui-p-local-tunnel-key), [Coverage gap](../glossary/README.md#coverage-gap), [Known limitation](../glossary/README.md#known-limitation) |
| <a id="P-BROWSER-LAB"></a>`P-BROWSER-LAB` | pane | [Browser lab pane](../glossary/README.md#ui-p-browser-lab), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-INBOX"></a>`PAGE-INBOX` | page | [Issues page](../glossary/README.md#ui-page-inbox), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-AGENTS"></a>`PAGE-AGENTS` | page | [Messages page](../glossary/README.md#ui-page-agents), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-SETTINGS"></a>`PAGE-SETTINGS` | page | [Settings page](../glossary/README.md#ui-page-settings), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="D-LAUNCH"></a>`D-LAUNCH` | dialog | [Launch dialog](../glossary/README.md#ui-d-launch), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior), [Keyboard focus](../glossary/README.md#keyboard-focus), [Modal dialog](../glossary/README.md#modal-dialog) |
| <a id="D-ATTENTION"></a>`D-ATTENTION` | dialog | [Attention dialog](../glossary/README.md#ui-d-attention), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior), [Keyboard focus](../glossary/README.md#keyboard-focus), [Modal dialog](../glossary/README.md#modal-dialog) |
| <a id="PAGE-PACKAGE"></a>`PAGE-PACKAGE` | page | [Package page](../glossary/README.md#ui-page-package), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-OWNER-CONSENT"></a>`PAGE-OWNER-CONSENT` | page | [Owner consent page](../glossary/README.md#ui-page-owner-consent), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-LOCAL-TUNNEL-SETUP"></a>`PAGE-LOCAL-TUNNEL-SETUP` | page | [Local tunnel setup page](../glossary/README.md#ui-page-local-tunnel-setup), [Coverage gap](../glossary/README.md#coverage-gap), [Known limitation](../glossary/README.md#known-limitation) |
| <a id="PAGE-BROWSER-LAB"></a>`PAGE-BROWSER-LAB` | page | [Browser lab page](../glossary/README.md#ui-page-browser-lab), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-NAVIGATE"></a>`C-NAVIGATE` | connection | [Navigate connection](../glossary/README.md#ui-c-navigate), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-FILTER"></a>`C-FILTER` | connection | [Filter connection](../glossary/README.md#ui-c-filter), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-SELECT-ITEM"></a>`C-SELECT-ITEM` | connection | [Select item connection](../glossary/README.md#ui-c-select-item), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-ITEM-STATE"></a>`C-ITEM-STATE` | connection | [Item state connection](../glossary/README.md#ui-c-item-state), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-PRIORITY"></a>`C-PRIORITY` | connection | [Priority connection](../glossary/README.md#ui-c-priority), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-LINK"></a>`C-LINK` | connection | [Link connection](../glossary/README.md#ui-c-link), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-LAUNCH-OPEN"></a>`C-LAUNCH-OPEN` | connection | [Launch open connection](../glossary/README.md#ui-c-launch-open), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-LAUNCH-PROFILE"></a>`C-LAUNCH-PROFILE` | connection | [Launch profile connection](../glossary/README.md#ui-c-launch-profile), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-PREPARE"></a>`C-PREPARE` | connection | [Prepare connection](../glossary/README.md#ui-c-prepare), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-LAUNCH-EDIT"></a>`C-LAUNCH-EDIT` | connection | [Launch edit connection](../glossary/README.md#ui-c-launch-edit), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-START"></a>`C-START` | connection | [Start connection](../glossary/README.md#ui-c-start), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-SELECT-RUN"></a>`C-SELECT-RUN` | connection | [Select run connection](../glossary/README.md#ui-c-select-run), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-RUN-ACTION"></a>`C-RUN-ACTION` | connection | [Run action connection](../glossary/README.md#ui-c-run-action), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-FOLLOW-UP"></a>`C-FOLLOW-UP` | connection | [Follow up connection](../glossary/README.md#ui-c-follow-up), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-RUN-ATTENTION"></a>`C-RUN-ATTENTION` | connection | [Run attention connection](../glossary/README.md#ui-c-run-attention), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-GLOBAL-ATTENTION"></a>`C-GLOBAL-ATTENTION` | connection | [Global attention connection](../glossary/README.md#ui-c-global-attention), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-ATTENTION-CHAT"></a>`C-ATTENTION-CHAT` | connection | [Attention chat connection](../glossary/README.md#ui-c-attention-chat), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-ANSWER"></a>`C-ANSWER` | connection | [Answer connection](../glossary/README.md#ui-c-answer), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-DISMISS"></a>`C-DISMISS` | connection | [Dismiss connection](../glossary/README.md#ui-c-dismiss), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-REPOSITORY-SAVE"></a>`C-REPOSITORY-SAVE` | connection | [Repository save connection](../glossary/README.md#ui-c-repository-save), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-DISCOVER-SKILLS"></a>`C-DISCOVER-SKILLS` | connection | [Discover skills connection](../glossary/README.md#ui-c-discover-skills), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-POLICY-SAVE"></a>`C-POLICY-SAVE` | connection | [Policy save connection](../glossary/README.md#ui-c-policy-save), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-PROFILE-SAVE"></a>`C-PROFILE-SAVE` | connection | [Profile save connection](../glossary/README.md#ui-c-profile-save), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-SKILL-SAVE"></a>`C-SKILL-SAVE` | connection | [Skill save connection](../glossary/README.md#ui-c-skill-save), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-DIAGNOSE"></a>`C-DIAGNOSE` | connection | [Diagnose connection](../glossary/README.md#ui-c-diagnose), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-STATE-REFRESH"></a>`C-STATE-REFRESH` | connection | [State refresh connection](../glossary/README.md#ui-c-state-refresh), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-TRANSCRIPT"></a>`C-TRANSCRIPT` | connection | [Transcript connection](../glossary/README.md#ui-c-transcript), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-PACKAGE"></a>`C-PACKAGE` | connection | [Package connection](../glossary/README.md#ui-c-package), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-CONSENT"></a>`C-CONSENT` | connection | [Consent connection](../glossary/README.md#ui-c-consent), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-EXTERNAL-AUTH"></a>`C-EXTERNAL-AUTH` | connection | [External auth connection](../glossary/README.md#ui-c-external-auth), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="C-TUNNEL-KEY"></a>`C-TUNNEL-KEY` | connection | [Tunnel key connection](../glossary/README.md#ui-c-tunnel-key), [Coverage gap](../glossary/README.md#coverage-gap), [Known limitation](../glossary/README.md#known-limitation) |
| <a id="C-BROWSER-LAB"></a>`C-BROWSER-LAB` | connection | [Browser lab connection](../glossary/README.md#ui-c-browser-lab), [Coverage gap](../glossary/README.md#coverage-gap), [Unverified behavior](../glossary/README.md#unverified-behavior) |
| <a id="PAGE-AGENT"></a>`PAGE-AGENT` | page | [Agent page](../glossary/README.md#ui-page-agent) |
| <a id="C-BACK"></a>`C-BACK` | connection | [Back navigation](../glossary/README.md#ui-c-back) |
| <a id="C-ITEM-SOURCE"></a>`C-ITEM-SOURCE` | connection | [Original item navigation](../glossary/README.md#ui-c-item-source) |
| <a id="S-SETTINGS"></a>`S-SETTINGS` | shell | [Settings surface](../glossary/README.md#ui-s-settings) |
| <a id="C-OPEN-SETTINGS"></a>`C-OPEN-SETTINGS` | connection | [Dedicated settings entry](../glossary/README.md#ui-c-open-settings) |
| <a id="PAGE-MY-PRS"></a>`PAGE-MY-PRS` | page | [My pull requests page](../glossary/README.md#ui-page-my-prs) |
| <a id="PAGE-REVIEW-PRS"></a>`PAGE-REVIEW-PRS` | page | [Review pull requests page](../glossary/README.md#ui-page-review-prs) |
| <a id="P-WAITING-QUEUE"></a>`P-WAITING-QUEUE` | pane | [Waiting messages pane](../glossary/README.md#ui-p-waiting-queue) |
| <a id="P-NEW-CHAT"></a>`P-NEW-CHAT` | pane | [First conversation pane](../glossary/README.md#ui-p-new-chat) |
| <a id="W-WAITING-ROW"></a>`W-WAITING-ROW` | widget | [Waiting message widget](../glossary/README.md#ui-w-waiting-row) |
| <a id="W-NEW-CHAT"></a>`W-NEW-CHAT` | widget | [First message widget](../glossary/README.md#ui-w-new-chat) |
| <a id="C-START-CHAT"></a>`C-START-CHAT` | connection | [First message start connection](../glossary/README.md#ui-c-start-chat) |
| <a id="W-WORK-STATUS"></a>`W-WORK-STATUS` | widget | [Work status widget](../glossary/README.md#ui-w-work-status) |
| <a id="PAGE-WORKING"></a>`PAGE-WORKING` | page | [Working page](../glossary/README.md#ui-page-working) |
| <a id="W-SESSION-CONSOLE"></a>`W-SESSION-CONSOLE` | widget | [Session console widget](../glossary/README.md#ui-w-session-console) |
| <a id="W-QUESTION-RECOVERY"></a>`W-QUESTION-RECOVERY` | widget | [Question recovery widget](../glossary/README.md#ui-w-question-recovery) |
| <a id="C-SESSION-CONSOLE"></a>`C-SESSION-CONSOLE` | connection | [Session console connection](../glossary/README.md#ui-c-session-console) |
| <a id="C-PIPELINE-SELECT"></a>`C-PIPELINE-SELECT` | connection | [Pipeline selection connection](../glossary/README.md#ui-c-pipeline-select) |
| <a id="C-TEXT-DISMISS"></a>`C-TEXT-DISMISS` | connection | [Text question dismissal connection](../glossary/README.md#ui-c-text-dismiss) |
<!-- /specs:terminology -->
