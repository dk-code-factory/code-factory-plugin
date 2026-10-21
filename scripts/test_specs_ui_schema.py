#!/usr/bin/env python3
"""UI ownership and interaction schema regression fixtures."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import fixture, put, render


class UISchemaTests(unittest.TestCase):
    def setUp(self):
        self.files = fixture()
        self.ui = uf.load(self.files, specs.UI)

    def validate(self, ui=None):
        put(self.files, specs.UI, self.ui if ui is None else ui)
        return specs.validate(self.files, rendered=False)

    def test_reusable_widget_elements_resolve_in_connections_and_dependencies(self):
        self.ui["entities"][2]["elements"] = [{"id": "W-EXAMPLE.submit-action2", "term": "action"}]
        self.ui["entities"][4]["to"] = ["W-EXAMPLE.submit-action2"]
        pane = copy.deepcopy(self.ui["entities"][1])
        pane["id"] = "P-OTHER"
        self.ui["entities"].append(pane)
        self.ui["entities"][0]["contains"].append("P-OTHER")
        self.ui["instances"].append({"id": "other", "type": "W-EXAMPLE", "pane": "P-OTHER", "repeat": "another displayed instance"})
        self.validate()
        render(self.files)
        specs.validate(self.files)
        _, deps, _ = specs.model(self.files)
        self.assertIn("ui:W-EXAMPLE", deps["ui:C-EXAMPLE"])
        self.assertIn("ui:C-EXAMPLE", deps["ui:W-EXAMPLE"])
        self.assertIn("ui:W-EXAMPLE", deps["ui:P-OTHER"])

    def test_elements_belong_only_to_widgets(self):
        for kind, eid in [("pane", "P-EXAMPLE"), ("page", "PAGE-EXAMPLE"),
                          ("dialog", "D-OTHER"), ("shell", "S-OTHER"),
                          ("connection", "C-EXAMPLE")]:
            with self.subTest(kind=kind):
                ui = copy.deepcopy(self.ui)
                entity = next((e for e in ui["entities"] if e["id"] == eid), None)
                if entity is None:
                    entity = copy.deepcopy(ui["entities"][1])
                    entity.update(id=eid, kind=kind)
                    ui["entities"].append(entity)
                entity["elements"] = [{"id": eid + ".submit", "term": "action"}]
                ui["entities"][4]["to"] = [eid + ".submit"]
                with self.assertRaisesRegex(uf.FlowError, "only widgets own elements"):
                    self.validate(ui)

    def test_element_names_are_scoped_single_nonempty_local_ids(self):
        for eid in ["W-EXAMPLE.", "W-EXAMPLE.submit.child", "W-EXAMPLE. submit",
                    "W-EXAMPLE.submit ", "W-EXAMPLE.two words", "W-ACTION.submit",
                    "W-EXAMPLE.Submit", "W-EXAMPLE.submit_name"]:
            with self.subTest(eid=eid):
                ui = copy.deepcopy(self.ui)
                ui["entities"][2]["elements"] = [{"id": eid, "term": "action"}]
                ui["entities"][4]["to"] = [eid]
                with self.assertRaisesRegex(uf.FlowError, "invalid element"):
                    self.validate(ui)

    def test_malformed_element_records_are_validation_errors(self):
        for elements in [None, "submit", [None], [{}], [{"id": 1, "term": "action"}],
                         [{"id": "W-EXAMPLE.submit", "term": []}],
                         [{"id": "W-EXAMPLE.submit", "term": "action", "to": ["W-ACTION"]}]]:
            with self.subTest(elements=elements):
                ui = copy.deepcopy(self.ui)
                ui["entities"][2]["elements"] = elements
                with self.assertRaisesRegex(uf.FlowError, "elements require|elements must"):
                    self.validate(ui)

    def test_endpoints_require_known_entities_or_declared_widget_elements(self):
        for endpoint in ["W-EXAMPLE.", "W-EXAMPLE.missing", "P-EXAMPLE.submit",
                         "C-EXAMPLE", "W-MISSING", " W-EXAMPLE", "W-EXAMPLE "]:
            with self.subTest(endpoint=endpoint):
                ui = copy.deepcopy(self.ui)
                ui["entities"][4]["to"] = [endpoint]
                with self.assertRaisesRegex(uf.FlowError, "invalid connection endpoint"):
                    self.validate(ui)
        for eid in ["PAGE-EXAMPLE", "P-EXAMPLE", "W-EXAMPLE"]:
            ui = copy.deepcopy(self.ui)
            ui["entities"][4]["to"] = [eid]
            self.validate(ui)

    def test_endpoint_arrays_and_connection_metadata_are_typed(self):
        for field in ["from", "to"]:
            for value in [None, [], "W-EXAMPLE", [None], [1], [{}], [""], [" "], ["W-EXAMPLE", "W-EXAMPLE"]]:
                with self.subTest(field=field, value=value):
                    ui = copy.deepcopy(self.ui)
                    ui["entities"][4][field] = value
                    with self.assertRaisesRegex(uf.FlowError, "connection endpoint"):
                        self.validate(ui)
        for field, value in [("connection_type", []), ("connection_type", "missing"),
                             ("external_boundary", "false"), ("external_boundary", 0),
                             ("endpoint_description", []), ("trigger", " "),
                             ("endpoint", ["W-EXAMPLE"]), ("connection_types", ["edge"])]:
            with self.subTest(field=field, value=value):
                ui = copy.deepcopy(self.ui)
                ui["entities"][4][field] = value
                with self.assertRaises(uf.FlowError):
                    self.validate(ui)

    def test_connection_fields_are_rejected_on_every_other_kind(self):
        for kind, eid in [("widget", "W-EXAMPLE"), ("pane", "P-EXAMPLE"),
                          ("page", "PAGE-EXAMPLE"), ("dialog", "D-OTHER"), ("shell", "S-OTHER")]:
            for field, value in [("from", []), ("to", ["W-ACTION"]), ("connection_type", "edge"),
                                 ("external_boundary", False), ("endpoint_description", "An endpoint"), ("trigger", "Click")]:
                with self.subTest(kind=kind, field=field):
                    ui = copy.deepcopy(self.ui)
                    entity = next((e for e in ui["entities"] if e["id"] == eid), None)
                    if entity is None:
                        entity = copy.deepcopy(ui["entities"][1])
                        entity.update(id=eid, kind=kind)
                        ui["entities"].append(entity)
                    entity[field] = value
                    with self.assertRaisesRegex(uf.FlowError, "connection fields require connection kind"):
                        self.validate(ui)

    def test_interaction_prose_remains_valid_on_widgets(self):
        self.ui["entities"][2].update(interactions="Click submits the current answer.",
                                       element_description="A named submit action.",
                                       state_description="The action waits while saving.")
        self.validate()

    def test_wrong_kind_fields_cannot_create_model_or_diagram_edges(self):
        self.ui["entities"][2].update(connection_type="edge", **{"from": ["W-ACTION"], "to": ["PAGE-EXAMPLE"]})
        self.ui["documents"][0]["blocks"].append({"name": "connections", "kind": "connections"})
        self.files["docs/ui/README.md"] += ("\n" + specs.marker("connections")[0] + "\n" + specs.marker("connections")[1] + "\n").encode()
        put(self.files, specs.UI, self.ui)
        with self.assertRaisesRegex(uf.FlowError, "connection fields require connection kind"):
            specs.validate(self.files, rendered=False)
        # Historical snapshots are consumed by model without current validation.
        _, deps, _ = specs.model(self.files)
        self.assertNotIn("ui:W-ACTION", deps["ui:W-EXAMPLE"])
        self.assertNotIn("ui:PAGE-EXAMPLE", deps["ui:W-EXAMPLE"])
        self.assertNotIn("term:edge", deps["ui:W-EXAMPLE"])
        self.assertNotIn("ui:W-EXAMPLE", deps["ui:PAGE-EXAMPLE"])
        diagram = specs.generated(self.files)["docs/ui/README.md"]
        self.assertNotIn('-->|"W-EXAMPLE"|', diagram)
        self.assertIn('-->|"C-EXAMPLE"|', diagram)


if __name__ == "__main__":
    unittest.main()
