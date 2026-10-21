---
name: reconcile-user-flows
description: Maintain Code Factory behavior contracts, structured diagrams and implementation/assertion traceability when behavior, source or tests change.
---

# Reconcile user flows

Choose the intended comparison: `--base HEAD` for current edits, explicit
`--base BRANCH --merge-base` for branch work, or pinned `--base BASE --head HEAD`
for committed comparisons. `--staged` selects only the index. Never narrow a
comparison to obtain a passing gate. Work from the repository root. Read
[the specification guide](../../../docs/specs/README.md) for schemas and reviews.

The dependency direction is behavior → UI → glossary, with behavior → glossary
also allowed. Operational tooling can read all layers. Never store consumer
backlinks in foundational documents. Inspect `python3 scripts/specs.py report
--base BASE --json`; it computes consumers across both old and new mappings.

## Ownership

Own `docs/user-flows/catalog.json` and the 15 behavior documents, authored
behavior, scenarios, structured graphs and generated consumer reference blocks.

## Inputs

Read `python3 scripts/specs.py impact --base BASE --json`, affected behavior,
old and new source/test references, UI entities and glossary definitions. Inspect
actual assertions for each scenario; names or a passing suite do not prove it.

## Permitted edits

Keep Goal, Preconditions, Main flow, Alternatives and recovery, and Scenarios
implementation agnostic. Keep paths and unique source/test anchors in metadata.
Scenario IDs need an assertion or explicit gap. Graph nodes/edges must cover
scenarios, alternative/failure/recovery paths, be reachable from the declared
start and reach terminal outcomes. Author labels as plain text; the renderer
escapes Mermaid syntax. Generated diagrams and references supplement written
prose and do not count as independent behavioral changes.

Update related implementation and meaningful tests for changed behavior. For a
refactor, terminology correction or structural-reference change, preserve accurate
behavior and explain the decision. New/retired product behavior requires code
and tests; the product adoption list remains pinned to `53ff8be` and must not be
extended or repinned. UI/glossary references go in the flow catalog, never reverse
links in foundations. Report needed foundation changes to their owners.

Version-2 gaps keep stable IDs, typed kind/severity, their layer owner, exact
reason and next verification; flow gaps also retain scenario ownership. Narrow
only assertions actually exercised. Use `python3 scripts/specs.py gaps --json`
to inspect the inventory; do not invent metadata for historical prose gaps or
convert a fixture result into real-host/accessibility acceptance.

## Verification

Run `python3 scripts/specs.py render`, `python3 scripts/specs.py validate` and
relevant mapped suites. Send substantive scenario-specific description,
implementation and assertion decisions to the coordinator for `python3
scripts/specs.py reconcile --base BASE --review /tmp/spec-review.json`.
Run `make flows-check` for current edits or `make ci FLOW_BASE=BASE` for branch
work. Historical `scripts/user_flows.py` commands are unified-gate aliases in a
registry checkout. Stage all relevant inputs and records together for `--staged`.
Keep real-provider, real-host and fixture verification separate.

## Handoff

Return affected flows/scenarios, behavior decisions, assertion evidence,
structured-graph changes, successful verification, explicit gaps and foundation
handoffs. Never claim fixture success closes outstanding host acceptance.
