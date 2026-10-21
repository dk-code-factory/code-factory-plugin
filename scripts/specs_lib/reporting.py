"""Pure presentation of a completed specification impact analysis.

``base`` is the effective, resolved base used by the analyzer. Snapshot arguments
describe that same comparison; this module never resolves refs or reads files.
The machine report retains complete inventories; only the human view is bounded.
"""
import hashlib
import json
import shlex


TERMS = "docs/glossary/terms.json"
UI = "docs/ui/entities.json"
CATALOG = "docs/user-flows/catalog.json"


def _load(files, path, field):
    return json.loads(files[path]).get(field, []) if path in files else []


def _identity(files, path):
    if path not in files:
        return None
    return (getattr(files, "modes", {}).get(path, "100644"),
            hashlib.sha256(files[path]).hexdigest())


def _gaps(values):
    """Read legacy UI prose and structured UI/flow reasons without rewriting either."""
    for value in values:
        yield (value.get("scenario", ""), value["reason"]) if isinstance(value, dict) else ("", value)


def build_report(before, after, report, affected, base, *, head=None,
                 staged=False, merge_base=False, root=None,
                 review_path="/tmp/spec-review.json"):
    """Build a deterministic, compact inventory without executing next steps.

    ``report``/``affected`` are the analyzer's outputs, not receipt success.
    References include both snapshots and transitive dependencies already chosen
    by the analyzer. Selection flags are retained in every comparison command.
    Working-tree write commands are omitted for index/committed selections.
    """
    if staged and head is not None:
        raise ValueError("staged and head selections are exclusive")
    selection = ["--base=" + str(base)]
    if head is not None:
        selection.append("--head=" + str(head))
    if staged:
        selection.append("--staged")
    if merge_base:
        selection.append("--merge-base")
    root_args = ["--root=" + str(root)] if root is not None else []

    artifacts = []
    for path in sorted(before.keys() | after.keys()):
        old, new = _identity(before, path), _identity(after, path)
        if old == new:
            continue
        change = ("added" if old is None else "deleted" if new is None else
                  "mode" if old[1] == new[1] else "modified")
        artifacts.append({"path": path, "change": change,
                          "before_mode": old[0] if old else None,
                          "after_mode": new[0] if new else None})

    reasons = sorted({reason for entries in report.values() for entry in entries
                      for reason in entry.get("reasons", [])})
    reason_ids = {reason: index for index, reason in enumerate(reasons)}
    impact = {}
    key_reasons = {}
    for layer, prefix in (("terms", "term:"), ("entities", "ui:"), ("flows", "flow:")):
        impact[layer] = []
        for entry in sorted(report.get(layer, []), key=lambda row: row["id"]):
            ids = sorted({reason_ids[reason] for reason in entry.get("reasons", [])})
            impact[layer].append({"id": entry["id"], "reason_ids": ids})
            key_reasons[prefix + entry["id"]] = ids

    references, reviews, verification = {}, [], {}
    for subject, item in sorted(affected.items()):
        seeds = item.get("subjects", ["flow:" + subject[5:]] if subject.startswith("flow-") else [])
        relevant = set(item.get("dependencies", {})) | set(seeds)
        gaps = set()
        coverage_gaps = {}

        def add(role, reference, snapshot):
            key = (role, reference["path"], reference.get("anchor", ""))
            row = references.setdefault(key, {"role": role, "path": key[1], "anchor": key[2],
                                              "subjects": set(), "snapshots": set(),
                                              "scenarios": set(), "descriptions": set()})
            row["subjects"].add(subject)
            row["snapshots"].add(snapshot)
            row["scenarios"].update(reference.get("scenarios", []))
            description = reference.get("assertion") or reference.get("purpose")
            if description:
                row["descriptions"].add(description)
            return key

        for snapshot, files in (("before", before), ("after", after)):
            entities = _load(files, UI, "entities")
            assertions = {a["id"]: a for a in _load(files, UI, "assertions")}
            for entity in entities:
                key = "ui:" + entity["id"]
                if key not in relevant:
                    continue
                for reference in entity["implementation"]:
                    add("implementation", reference, snapshot)
                for aid in entity["assertions"]:
                    if aid in assertions:
                        add("assertion", assertions[aid], snapshot)
                gaps.update((key, scenario, reason) for scenario, reason in _gaps(entity.get("gaps", [])))
            selected_entities = {entity["id"] for entity in entities if "ui:" + entity["id"] in relevant}
            for group in _load(files, UI, "coverage_groups"):
                if not selected_entities.intersection(group["entities"]):
                    continue
                value = group["gap"]
                row = {"id": "coverage:" + group["name"], "scenario": "",
                       "entities": sorted(set(group["entities"])),
                       "assertion_ids": sorted(set(group["assertions"]))}
                if isinstance(value, dict):
                    row.update({"gap_id" if field == "id" else field: content
                                for field, content in value.items()})
                else:
                    row["reason"] = value
                # Membership, metadata and reason are part of a record's evidence.
                # Preserve differing old/new records instead of overwriting one.
                identity = json.dumps(row, sort_keys=True, ensure_ascii=False)
                entry = coverage_gaps.setdefault(identity, {**row, "snapshots": set(),
                                                            "_reference_keys": set()})
                entry["snapshots"].add(snapshot)
                for aid in group["assertions"]:
                    if aid in assertions:
                        entry["_reference_keys"].add(add("assertion", assertions[aid], snapshot))
            # Historical ownerless inventory is reviewed conservatively; never
            # imply that those references provide an entity's behavior coverage.
            owned = {aid for entity in entities for aid in entity["assertions"]}
            if any(key.startswith("ui:") for key in relevant) or not entities:
                for aid in assertions.keys() - owned:
                    add("historical-inventory", assertions[aid], snapshot)
            for flow in _load(files, CATALOG, "flows"):
                key = "flow:" + flow["id"]
                if key not in relevant:
                    continue
                for role in ("implementation", "tests"):
                    for reference in flow[role]:
                        add("assertion" if role == "tests" else role, reference, snapshot)
                gaps.update((key, scenario, reason) for scenario, reason in _gaps(flow.get("gaps", [])))
        suites = sorted(item.get("suites", {}))
        for name, suite in item.get("suites", {}).items():
            verification[name] = suite["commands"]
        reviews.append({"subject": subject, "ids": sorted(seeds),
                        "reason_ids": sorted({i for key in seeds for i in key_reasons.get(key, [])}),
                        "suites": suites,
                        "gaps": [{"id": key, "scenario": scenario, "reason": reason}
                                 for key, scenario, reason in sorted(gaps)] +
                                [coverage_gaps[key] for key in sorted(coverage_gaps)]})
    inventory = []
    reference_ids = {key: index for index, key in enumerate(sorted(references))}
    for _, row in sorted(references.items()):
        inventory.append({key: sorted(value) if isinstance(value, set) else value
                          for key, value in row.items()})
    for review in reviews:
        review["reference_ids"] = [i for i, row in enumerate(inventory)
                                   if review["subject"] in row["subjects"]]
        for gap in review["gaps"]:
            if "_reference_keys" in gap:
                gap["reference_ids"] = sorted(reference_ids[key] for key in gap.pop("_reference_keys"))
                gap["snapshots"] = sorted(gap["snapshots"])

    def command(action, comparison=False, extra=(), requires=None):
        row = {"action": action, "argv": ["python3", "scripts/specs.py", action,
                                           *root_args, *(selection if comparison else []), *extra]}
        if requires:
            row["requires"] = requires
        return row

    readonly = staged or head is not None
    next_commands = []
    if affected and not readonly:
        next_commands.append(command("render"))
    next_commands.append(command("validate", extra=[a for a in selection if not a.startswith("--base=") and a != "--merge-base"]))
    if affected and not readonly:
        next_commands.append(command("reconcile", True, ["--review=" + str(review_path)],
                                     "Prepare substantive review JSON for every review subject at " + str(review_path)))
    next_commands.append(command("check", True, ["--json"]))
    return {"version": 1, "comparison": {"base": str(base), "head": head,
             "staged": staged, "merge_base": merge_base, "root": str(root) if root is not None else None},
            "artifacts": artifacts, "reasons": reasons, "impact": impact,
            "reviews": reviews, "references": inventory,
            "verification": [{"suite": name, "commands": commands}
                             for name, commands in sorted(verification.items())],
            "next_commands": next_commands,
            "selection_note": ("Reconciliation writes require the working tree; these commands preserve the selected snapshot."
                               if readonly else "Review and verification are required; this report records no successful execution.")}


def report_json(summary):
    """Compact deterministic machine format; no fingerprints or duplicate bodies."""
    return json.dumps(summary, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def format_report(summary, *, max_items=12, max_chars=12000):
    """Return a bounded human view; the JSON report remains complete."""
    if max_items < 1 or max_chars < 80:
        raise ValueError("report bounds require at least one item and 80 characters")
    lines = [f"Comparison: {summary['comparison']['base']} → " +
             (summary['comparison']['head'] or ("index" if summary['comparison']['staged'] else "working tree"))]
    lines.append(summary["selection_note"])
    lines.append("Next commands:")
    for command in summary["next_commands"]:
        if command.get("requires"):
            lines.append("  Requires: " + command["requires"])
        lines.append("  " + shlex.join(command["argv"]))

    def section(title, rows):
        lines.append(title + f" ({len(rows)}):")
        lines.extend("  " + row for row in rows[:max_items])
        if len(rows) > max_items:
            lines.append(f"  … {len(rows) - max_items} more; see JSON report")

    section("Changed artifacts", [row["change"] + " " + row["path"] for row in summary["artifacts"]])
    section("Review subjects", [row["subject"] + ": " + ", ".join(row["ids"]) for row in summary["reviews"]])
    section("Reasons", summary["reasons"])
    section("Relevant source/assertion anchors", [row["role"] + " " + row["path"] +
            (" — " + row["anchor"] if row["anchor"] else "") for row in summary["references"]])
    gap_rows = {json.dumps(row, sort_keys=True, ensure_ascii=False): row
                for review in summary["reviews"] for row in review["gaps"]}
    section("Coverage gaps", [row["id"] +
            (" [" + row["gap_id"] + "]" if "gap_id" in row else "") +
            (" (" + ", ".join(row["snapshots"]) + ")" if "snapshots" in row else "") +
            ": " + row["reason"] for _, row in sorted(gap_rows.items())])
    text = "\n".join(lines)
    suffix = "\n… report truncated; complete inventories and argv are in JSON."
    return text if len(text) <= max_chars else text[:max_chars-len(suffix)] + suffix
