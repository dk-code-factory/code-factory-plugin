"""Presentation and compatibility for legacy and versioned open-gap declarations."""
import json

from specs_lib import gaps


def render_gap(value):
    if isinstance(value, str):
        return value
    if isinstance(value, dict) and "version" not in value:
        return value["reason"]
    return gaps.render(value)


def inventory(ui, catalog):
    """Keep historical untyped reasons separate; never invent their metadata."""
    typed_ui = ui if ui.get("version") == 2 else {"entities": [], "coverage_groups": []}
    typed_catalog = catalog if catalog.get("version") == 2 else {"flows": []}
    result = gaps.report(typed_ui, typed_catalog)
    legacy = []
    if ui.get("version") == 1:
        for entity in ui.get("entities", []):
            legacy.extend({"subject": "ui:" + entity["id"], "reason": value} for value in entity["gaps"])
        legacy.extend({"subject": "coverage:" + group["name"], "reason": group["gap"]}
                      for group in ui.get("coverage_groups", []))
    if catalog.get("version") == 1:
        for flow in catalog.get("flows", []):
            legacy.extend({"subject": "flow:" + flow["id"], **value} for value in flow["gaps"])
    legacy.sort(key=lambda row: (row["subject"], row.get("scenario", ""), row["reason"]))
    typed_count = result["counts"]["total"]
    result["counts"].update(total=typed_count + len(legacy), typed=typed_count,
                            legacy=len(legacy), with_next_verification=typed_count)
    result["legacy_gaps"] = legacy
    return result


def format_inventory(result):
    counts = result["counts"]
    lines = [f"Open gaps: {counts['total']} ({counts['typed']} typed, {counts['legacy']} legacy)",
             "Kinds: " + json.dumps(counts["kind"], sort_keys=True),
             "Severities: " + json.dumps(counts["severity"], sort_keys=True),
             "Owners: " + json.dumps(counts["owner"], sort_keys=True),
             f"Declared next verification: {counts['with_next_verification']}"]
    for record in result["gaps"]:
        lines.append(record["subject"] + ": " + gaps.render({key: value for key, value in record.items() if key != "subject"}))
    for record in result["legacy_gaps"]:
        lines.append(record["subject"] + ": legacy untyped gap: " + record["reason"])
    return "\n".join(lines)
