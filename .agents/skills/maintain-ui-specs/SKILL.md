---
name: maintain-ui-specs
description: Maintain UI registry structure, interaction endpoints and implementation/assertion coverage for UI changes.
---

# Maintain ui specs

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

Own `docs/ui/entities.json` and the UI Markdown documents, generated blocks and
structural prose. Keep coverage independent of behavioral specifications.

## Inputs

Read affected glossary terms, entity/instance records, widget states, pane/page
membership, connection endpoints and exact source/test assertions. Read consumer
behavior for context without linking back to it.

## Permitted edits

Preserve stable entity IDs. Widget types are reusable; displayed instances own
pane context and repetition. Keep element IDs scoped to their widget. Map every
entity to source references and existing assertions or explicit gaps. Maintain
conditional-state text, term references, containment and connection endpoints.
Do not edit glossary definitions or consumer behavior. Request missing terms or
consumer updates from their owners. Keep accessibility and real-host gaps open
until corresponding observations or assertions exist.

Version-2 gaps keep stable IDs, typed kind/severity, their layer owner, exact
reason and next verification; flow gaps also retain scenario ownership. Narrow
only assertions actually exercised. Use `python3 scripts/specs.py gaps --json`
to inspect the inventory; do not invent metadata for historical prose gaps or
convert a fixture result into real-host/accessibility acceptance.

## Verification

Run `python3 scripts/specs.py render`, `python3 scripts/specs.py validate` and
`python3 -m unittest discover -s scripts -p 'test_specs*.py'`. Run mapped UI suites appropriate to the change;
fixture assertions do not establish complete widget coverage. Give review notes
to the coordinator for `python3 scripts/specs.py reconcile` and the final check.
Read the browser runbook before any browser or live acceptance work.

## Handoff

Return affected entity and instance IDs, containment/endpoint changes, concrete
assertions checked, remaining gaps, consumer implications and verification.

