#!/usr/bin/env python3
"""Coverage-gap schema, migration and reporting tests without external tools."""
import copy
import json
import unittest

from specs_lib import gaps


def record(gap_id="gap-ui-example-01", **changes):
    value = {"version": 1, "id": gap_id, "kind": "unverified-behavior", "severity": "P2",
             "owner": "ui-specification-maintainer", "reason": "The failure response lacks an assertion.",
             "next_verification": "Exercise a rejected save and assert its retained draft and visible error."}
    value.update(changes)
    return value


class GapSchemaTests(unittest.TestCase):
    def test_types_distinguish_observed_limitation_from_missing_verification(self):
        limitation = record(kind="known-limitation", reason="The current host has not registered subscriptions.",
                            owner="specification-coordinator", severity="P1",
                            next_verification="Recheck host subscription registration and retain dated callback evidence.")
        unverified = record()
        self.assertEqual(gaps.validate(limitation), [])
        self.assertEqual(gaps.validate(unverified), [])
        self.assertNotEqual(gaps.render(limitation), gaps.render(unverified))
        self.assertIn("Known limitation", gaps.render(limitation))
        self.assertIn("Unverified behavior", gaps.render(unverified))

    def test_rejects_incomplete_or_untyped_records_without_coercion(self):
        invalid = [None, "Legacy text", [], True, {}]
        for field in gaps.FIELDS:
            value = record(); del value[field]; invalid.append(value)
        for field, values in {"version": [True, 1.0, "1", 2], "id": ["", "GAP-X", "gap-x/01", []],
                              "kind": ["verified", [], None], "severity": ["P0", 2, False],
                              "owner": ["", "unassigned", {}, None], "reason": [" ", [], None],
                              "next_verification": ["\n", {}, False]}.items():
            for value in values:
                invalid.append(record(**{field: value}))
        invalid.append(record(closed=True))
        for value in invalid:
            with self.subTest(value=value):
                before = copy.deepcopy(value)
                self.assertTrue(gaps.validate(value))
                self.assertEqual(value, before)

    def test_flow_scenarios_are_required_known_and_forbidden_on_ui(self):
        flow = record("gap-flow-example-s1-01", owner="user-flow-maintainer", scenario="S1")
        self.assertEqual(gaps.validate(flow, scenario_required=True, allowed_scenarios=["S1"]), [])
        self.assertTrue(gaps.validate(flow, scenario_required=True, allowed_scenarios=["S2"]))
        self.assertTrue(gaps.validate(flow))
        self.assertTrue(gaps.validate(record(), scenario_required=True, allowed_scenarios=["S1"]))
        self.assertTrue(gaps.validate(record(scenario=[]), scenario_required=True))

    def test_normalization_requires_explicit_metadata_and_preserves_reason(self):
        reason = "Original `assertion` text.\n  Retain whitespace and ../acceptance.md."
        metadata = {"gap_id": "gap-ui-example-01", "kind": "unverified-behavior", "severity": "P2",
                    "owner": "ui-specification-maintainer", "next_verification": record()["next_verification"]}
        with self.assertRaises(ValueError):
            gaps.normalize(reason)
        migrated = gaps.normalize(reason, **metadata)
        self.assertEqual(migrated["reason"], reason)
        self.assertEqual(migrated["id"], metadata["gap_id"])
        self.assertEqual(gaps.normalize(migrated), migrated)
        edited = {**migrated, "reason": "A more precise source-derived description."}
        self.assertEqual(gaps.normalize(edited)["id"], migrated["id"])
        with self.assertRaises(ValueError):
            gaps.normalize(migrated, owner="specification-coordinator")

    def test_legacy_flow_normalization_cannot_drop_or_invent_scenario(self):
        metadata = {"gap_id": "gap-flow-example-s1-01", "kind": "unverified-behavior", "severity": "P2",
                    "owner": "user-flow-maintainer", "next_verification": "Assert the first scenario's recovery response."}
        legacy = {"scenario": "S1", "reason": "A recovery assertion is missing."}
        migrated = gaps.normalize(legacy, **metadata)
        self.assertEqual(migrated["scenario"], legacy["scenario"])
        self.assertEqual(migrated["reason"], legacy["reason"])
        for value in [{**legacy, "ignored": "must not disappear"}, {**legacy, "scenario": None},
                      {"reason": legacy["reason"]}]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                gaps.normalize(value, **metadata)

    def test_render_keeps_reason_identity_owner_priority_and_next_step(self):
        value = record()
        text = gaps.render(value)
        for field in ("id", "reason", "severity", "owner", "next_verification"):
            self.assertIn(value[field], text)
        self.assertEqual(gaps.render(value), text)
        with self.assertRaises(ValueError):
            gaps.render("Legacy text")

    def test_no_loss_across_ui_flow_and_coverage_migration(self):
        legacy_ui = ["A repeated gap.", "A repeated gap."]
        legacy_group = "A distinct coverage summary."
        legacy_flow = {"scenario": "S1", "reason": "A source-derived lifecycle gap."}
        def convert(value, gid, owner="ui-specification-maintainer"):
            return gaps.normalize(value, gap_id=gid, kind="unverified-behavior", severity="P2", owner=owner,
                                  next_verification="Exercise the described boundary and assert its observable result.")
        ui = {"entities": [{"id": "W-EXAMPLE", "gaps": [convert(v, f"gap-ui-example-{i:02}")
                                                           for i, v in enumerate(legacy_ui, 1)]}],
              "coverage_groups": [{"name": "Example", "gap": convert(legacy_group, "gap-coverage-example-01")}]}
        catalog = {"flows": [{"id": "example", "scenarios": ["S1"],
                              "gaps": [convert(legacy_flow, "gap-flow-example-s1-01", "user-flow-maintainer")]}]}
        inventory = gaps.report(ui, catalog)
        self.assertEqual(inventory["counts"]["total"], 4)
        self.assertCountEqual([r["reason"] for r in inventory["gaps"]],
                              legacy_ui + [legacy_group, legacy_flow["reason"]])
        self.assertEqual(len({r["id"] for r in inventory["gaps"]}), 4)
        self.assertEqual(next(r for r in inventory["gaps"] if r["subject"] == "flow:example")["scenario"], "S1")

    def test_report_rejects_global_id_collision_and_wrong_layer_owner(self):
        ui = {"entities": [{"id": "W-EXAMPLE", "gaps": [record()]}],
              "coverage_groups": [{"name": "Example", "gap": record()}]}
        with self.assertRaisesRegex(ValueError, "duplicate global gap id"):
            gaps.report(ui, {"flows": []})
        ui["coverage_groups"] = []
        ui["entities"][0]["gaps"][0]["owner"] = "user-flow-maintainer"
        with self.assertRaisesRegex(ValueError, "does not maintain this layer"):
            gaps.report(ui, {"flows": []})
        flow = record("gap-flow-example-s1-01", scenario="S1")
        with self.assertRaisesRegex(ValueError, "does not maintain this layer"):
            gaps.report({"entities": []}, {"flows": [{"id": "example", "scenarios": ["S1"], "gaps": [flow]}]})

    def test_report_and_normalize_are_deterministic_and_do_not_mutate(self):
        values = [record("gap-ui-z-01"), record("gap-ui-a-01", severity="P1", kind="known-limitation")]
        ui = {"entities": [{"id": "W-EXAMPLE", "gaps": values}], "coverage_groups": []}
        before = copy.deepcopy(ui)
        first = gaps.report(ui, {"flows": []})
        self.assertEqual(ui, before)
        ui["entities"][0]["gaps"] = [dict(reversed(list(value.items()))) for value in reversed(values)]
        self.assertEqual(json.dumps(gaps.report(ui, {"flows": []})), json.dumps(first))
        self.assertEqual(first["counts"]["kind"], {"known-limitation": 1, "unverified-behavior": 1})
        self.assertEqual(first["counts"]["severity"], {"P1": 1, "P2": 1})
        self.assertEqual(first["gaps"][0]["id"], "gap-ui-a-01")


if __name__ == "__main__":
    unittest.main()
