"""Receipts for local specification tooling."""



def _note_errors(subject, notes, kinds, *, fields=None):
    if not isinstance(notes, dict):
        return [subject + ": review decisions must be an object"]
    errors = []
    kind = notes.get("kind")
    if not isinstance(kind, str) or kind not in kinds:
        errors.append(subject + ": review kind must be behavior, refactor, terminology, structure or adoption")
    if fields is not None and set(notes) != fields:
        errors.append(subject + ": review fields must be exactly " + ", ".join(sorted(fields)))
    if any(not isinstance(notes.get(role), str) or len(notes[role].strip()) < 40
           for role in ("description", "implementation", "tests")):
        errors.append(subject + ": substantive description, implementation and assertion review decisions required (40+ characters each)")
    return errors


def _cochange_errors(subject, changes, kind):
    if kind == "behavior" or changes.get("lifecycle_changed"):
        missing = [role for role in ("implementation", "tests") if not changes[role]]
        if missing:
            return [subject + ": behavior requires changes to related " + " and ".join(missing)]
    return []


def constraints(subject, item, notes):
    import specs as api
    errors = _note_errors(subject, notes, api.KINDS | {"mixed"})
    if not isinstance(notes, dict):
        return errors
    kind = notes.get("kind")
    if kind == "mixed":
        if subject != "ui":
            return errors + [subject + ": mixed reviews are limited to UI contracts"]
        fields = {"kind", "description", "implementation", "tests", "contracts"}
        if set(notes) != fields:
            errors.append(subject + ": mixed review fields must be exactly " + ", ".join(sorted(fields)))
        contracts, decisions = item.get("contracts"), notes.get("contracts")
        if not isinstance(contracts, dict) or not contracts:
            return errors + [subject + ": mixed reviews require direct UI contracts"]
        if not isinstance(decisions, dict):
            return errors + [subject + ": mixed contracts decisions must be an object"]
        missing, unknown = set(contracts) - set(decisions), set(decisions) - set(contracts)
        if missing:
            errors.append(subject + ": missing contracts decisions: " + ", ".join(sorted(missing)))
        if unknown:
            errors.append(subject + ": unknown contracts decisions: " + ", ".join(sorted(map(str, unknown))))
        for contract in sorted(set(contracts) & set(decisions)):
            child, changes = decisions[contract], contracts[contract]
            label = subject + " (" + contract + ")"
            errors.extend(_note_errors(label, child, api.KINDS, fields=fields - {"contracts"}))
            if not isinstance(child, dict):
                continue
            child_kind = child.get("kind")
            if child_kind == "adoption" and not changes.get("adopted", False):
                errors.append(label + ": adoption is limited to the established source baseline")
            if not (child_kind == "adoption" and changes.get("adopted", False)):
                errors.extend(_cochange_errors(label, changes, child_kind))
        return errors
    if "contracts" in notes:
        errors.append(subject + ": contracts decisions require an explicit UI mixed review")
    if kind == "adoption" and not item["adopted"]:
        errors.append(subject + ": adoption is limited to the established source baseline")
    # Eligibility for initial adoption is not itself a review classification.
    # Only a permitted adoption review can waive behavioral cochanges.
    if not (kind == "adoption" and item["adopted"]):
        contracts = item.get("contracts") or {subject: item}
        for contract, changes in contracts.items():
            # Explicit behavioral review requires cochanges even when the
            # existing prose already describes the newly implemented behavior.
            # New/retired contracts cannot use a structural classification to
            # bypass the same requirement.
            errors.extend(_cochange_errors(subject + " (" + contract + ")", changes, kind))
    return errors


def receipt_errors(files, subject, item, base):
    import specs as api
    import user_flows as uf
    try:
        record = uf.load(files, api.RECORDS + subject + ".json")
    except uf.FlowError:
        return [subject + ": reconciliation required (run reconcile)"]
    notes = record.get("review", {})
    errors = api.constraints(subject, item, notes)
    if record.get("dependencies") != item.get("dependencies"):
        errors.append(subject + ": dependency fingerprints stale or missing")
    # Reuse command/environment/base/fingerprint verification from the flow gate.
    alias = dict(files)
    alias[uf.RECORDS + subject + ".json"] = files[api.RECORDS + subject + ".json"]
    adjusted = {**item, "description_changed": False}
    return errors + uf.receipt_errors(alias, subject, adjusted, base)
