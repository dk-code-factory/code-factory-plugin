"""Pure helpers for versioned, open specification coverage gaps.

Legacy conversion requires explicit human-assigned metadata. Validation never
accepts legacy strings as complete records or infers evidence from their text.
"""
import copy
import re
from collections import Counter

VERSION = 1
KINDS = frozenset({"known-limitation", "unverified-behavior"})
SEVERITIES = frozenset({"P1", "P2", "P3"})
OWNERS = frozenset({"ui-specification-maintainer", "user-flow-maintainer", "specification-coordinator"})
FIELDS = frozenset({"version", "id", "kind", "severity", "owner", "reason", "next_verification"})
FIELD_ORDER = ("version", "id", "scenario", "kind", "severity", "owner", "reason", "next_verification")
ID_PATTERN = re.compile(r"gap-[a-z0-9]+(?:-[a-z0-9]+)*\Z")


def validate(record, *, scenario_required=False, allowed_scenarios=None):
    """Return deterministic schema errors without mutating or coercing input."""
    if not isinstance(record, dict):
        return ["gap must be a versioned object"]
    errors = []
    fields = FIELDS | ({"scenario"} if scenario_required else set())
    missing, unknown = fields - record.keys(), record.keys() - fields
    if missing:
        errors.append("gap missing fields: " + ", ".join(sorted(missing)))
    if unknown:
        errors.append("gap unknown fields: " + ", ".join(sorted(map(str, unknown))))
    if type(record.get("version")) is not int or record["version"] != VERSION:
        errors.append("gap version must be integer 1")
    if not isinstance(record.get("id"), str) or not ID_PATTERN.fullmatch(record["id"]):
        errors.append("gap id must be a stable lowercase gap-* identifier")
    for field, choices in (("kind", KINDS), ("severity", SEVERITIES), ("owner", OWNERS)):
        if not isinstance(record.get(field), str) or record[field] not in choices:
            errors.append("gap " + field + " must be one of: " + ", ".join(sorted(choices)))
    for field in ("reason", "next_verification"):
        if not isinstance(record.get(field), str) or not record[field].strip():
            errors.append("gap " + field + " must be nonempty text")
    if "scenario" in fields:
        scenario = record.get("scenario")
        if not isinstance(scenario, str) or not scenario.strip():
            errors.append("flow gap scenario must be nonempty text")
        elif allowed_scenarios is not None and scenario not in allowed_scenarios:
            errors.append("flow gap scenario is undefined: " + scenario)
    return errors


def normalize(value, *, gap_id=None, kind=None, severity=None, owner=None,
              next_verification=None):
    """Copy a complete record, or convert legacy text with explicit metadata.

    Stored IDs are never derived again from position or prose. Supplying metadata
    for an existing record is rejected rather than silently rewriting ownership.
    """
    metadata = (gap_id, kind, severity, owner, next_verification)
    if isinstance(value, dict) and "version" in value:
        if any(item is not None for item in metadata):
            raise ValueError("existing gap metadata must be edited explicitly")
        record = copy.deepcopy(value)
    else:
        if any(item is None for item in metadata):
            raise ValueError("legacy gap conversion requires explicit id, kind, severity, owner and next verification")
        if isinstance(value, str):
            reason, scenario, has_scenario = value, None, False
        elif isinstance(value, dict) and set(value) == {"scenario", "reason"}:
            reason, scenario, has_scenario = value["reason"], value["scenario"], True
        else:
            raise ValueError("legacy gap must be text or an exact scenario/reason object")
        record = {"version": VERSION, "id": gap_id, "kind": kind,
                  "severity": severity, "owner": owner, "reason": reason,
                  "next_verification": next_verification}
        if has_scenario:
            record["scenario"] = scenario
    errors = validate(record, scenario_required="scenario" in record)
    if errors:
        raise ValueError("; ".join(errors))
    return {key: record[key] for key in FIELD_ORDER if key in record}


def render(record):
    """Return readable text containing the complete open-gap metadata."""
    errors = validate(record, scenario_required=isinstance(record, dict) and "scenario" in record)
    if errors:
        raise ValueError("; ".join(errors))
    kind = {"known-limitation": "Known limitation", "unverified-behavior": "Unverified behavior"}[record["kind"]]
    return (f'`{record["id"]}` · {kind} · {record["severity"]} · owner: {record["owner"]}. '
            + record["reason"] + " Next verification: " + record["next_verification"])


def report(ui, catalog):
    """Return a sorted inventory and counts; reject ambiguity across registries."""
    records, errors, seen = [], [], set()

    def add(record, subject, *, scenarios=None):
        issues = validate(record, scenario_required=scenarios is not None, allowed_scenarios=scenarios)
        if not issues:
            gid = record["id"]
            if gid in seen:
                issues.append("duplicate global gap id: " + gid)
            seen.add(gid)
            allowed = {"specification-coordinator", "user-flow-maintainer" if scenarios is not None else "ui-specification-maintainer"}
            if record["owner"] not in allowed:
                issues.append("gap owner does not maintain this layer: " + record["owner"])
        errors.extend(subject + ": " + issue for issue in issues)
        if not issues:
            records.append({"subject": subject, **normalize(record)})

    for entity in ui.get("entities", []):
        gaps = entity.get("gaps")
        subject = "ui:" + entity["id"]
        if not isinstance(gaps, list):
            errors.append(subject + ": gaps must be an array")
            continue
        for record in gaps:
            add(record, subject)
    for group in ui.get("coverage_groups", []):
        add(group.get("gap"), "coverage:" + group["name"])
    for flow in catalog.get("flows", []):
        gaps = flow.get("gaps")
        subject = "flow:" + flow["id"]
        if not isinstance(gaps, list):
            errors.append(subject + ": gaps must be an array")
            continue
        for record in gaps:
            add(record, subject, scenarios=flow["scenarios"])
    if errors:
        raise ValueError("\n".join(sorted(errors)))
    records.sort(key=lambda record: record["id"])
    return {"version": VERSION, "gaps": records,
            "counts": {"total": len(records), **{field: dict(sorted(Counter(r[field] for r in records).items()))
                                                   for field in ("kind", "severity", "owner")}}}
