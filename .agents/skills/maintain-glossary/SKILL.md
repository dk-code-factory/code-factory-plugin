---
name: maintain-glossary
description: Maintain the project glossary and identify terminology impact without editing consuming specifications.
---

# Maintain glossary

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

Own only `docs/glossary/terms.json` and generated `docs/glossary/README.md`.

## Inputs

Read changed terminology and impacted consumers for meaning. Stable term IDs,
canonical names, aliases, categories, definitions and optional naming scopes are
the vocabulary contract.

## Permitted edits

Edit canonical terminology only. Definitions state meanings, not procedures,
implementation references, paths, links or specification IDs. Do not add usage
lists, backlinks or “used by” sections. Keep same-scope names/aliases unambiguous;
scopes refer to stable term IDs. Report UI/behavior implications without editing
those layers. Do not rename product behavior merely to simplify a definition.

## Verification

Run `python3 scripts/specs.py render` and `python3 scripts/specs.py validate`.
Run `python3 -m unittest discover -s scripts -p 'test_specs*.py'`; inspect the impact report for consumers.
Hand substantive terminology decisions to the coordinator for fingerprinted
reconciliation and the final `python3 scripts/specs.py check --base BASE`.

## Handoff

Return changed term IDs, canonical/alias/scope decisions, affected consumers,
verification and unresolved meaning questions. Do not add consumer lookup to
the glossary; return it in the tooling report.
