---
name: reconcile-specs
description: Coordinate glossary, UI and user-flow impact, reviewed reconciliation and deterministic checks for repository changes.
---

# Reconcile specs

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

Own impact analysis and `docs/specs/reconciliations/`. Coordinate edits with the
appropriate layer maintainer. A reviewer inspects consistency read-only; ordinary
maintenance does not itself authorize spawning agents.

## Inputs

Read intended behavior, affected registry entries, both comparison snapshots,
source/test anchors and existing gaps. Review actual assertions before claiming
coverage. Foundation maintainers report implications to consumer owners.

## Permitted edits

Edit reconciliation decisions and coordinate authorized layer edits. Classify
reviews as behavior, refactor, terminology, structure or pinned initial adoption.
Behavior-bearing changes require related implementation and meaningful test
changes. Refactors, naming corrections and structural-reference updates may keep
accurate behavior with substantive notes. Generated output is not an independent
behavior change. Never broaden the product-flow adoption list or hand-edit
receipts. Missing foundation definitions require an owner handoff, not backlinks.

Use `report` for compact impact and exact next argv; use `gaps --json` for typed
open-gap ownership/severity/next-verification inventory. Neither command records
successful verification. UI-only mixed reviews require complete direct-contract
child decisions with three substantive notes each; only eligible original-baseline
contracts may use adoption. Actual new UI/helper behavior needs its own code/tests.
Never use mixed reviews to override product-flow policy or change the pinned list.

## Verification

Run `python3 scripts/specs.py render` and `python3 scripts/specs.py validate`.
Prepare a temporary JSON review outside the repository keyed by `glossary`, `ui`
or `flow-FLOW_ID`, with `kind`, `description`, `implementation` and `tests` decisions.
Cover every affected subject and explain specific IDs, scenarios and assertions.
Inspect catalog commands, then run `python3 scripts/specs.py reconcile --base BASE
--review /tmp/spec-review.json` with the intended comparison options. It executes
fixture suites and writes fingerprinted records only on success; a failed rerun
invalidates selected older successes. Run `python3 scripts/specs.py check` with
the same options and `make ci FLOW_BASE=BASE` for branch validation. CI runs no
agents and needs no model credentials. Real-provider tests remain opt-in.

## Handoff

Return affected IDs, review classifications and rationale, verification actually
run, record paths, unresolved gaps and any owner handoffs. A passing fixture gate
never closes real-host, accessibility, soak or login acceptance.

