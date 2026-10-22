"""Flow receipts for local specification tooling."""



def contract_errors(fid, item):
    if item["description_changed"] and not item["adopted"]:
        missing = [role for role in ("implementation", "tests") if not item[role]]
        if missing:
            return [fid + ": description changed; change related " + " and ".join(missing)]
    return []


def receipt_errors(files, fid, item, base):
    import user_flows as uf
    errors = uf.contract_errors(fid, item)
    try:
        record = uf.load(files, uf.RECORDS + fid + ".json")
    except uf.FlowError:
        return errors + [fid + ": reconciliation required (run reconcile)"]
    if type(record.get("version")) is not int or record["version"] != 1 or record.get("flow") != fid or record.get("base") != base or record.get("fingerprint") != item["fingerprint"] or record.get("environment") != uf.FIXTURE_ENV:
        errors.append(fid + ": reconciliation stale or for a different base")
    for role in ("description", "implementation", "tests"):
        if not isinstance(record.get("review", {}).get(role), str) or not record["review"][role].strip():
            errors.append(fid + ": missing review of " + role)
    expected = [command for suite in item["suites"].values() for command in suite["commands"]]
    results = record.get("verification", [])
    if [r.get("command") for r in results] != expected or any(type(r.get("exit_code")) is not int or r["exit_code"] != 0 for r in results):
        errors.append(fid + ": successful required verification missing")
    return errors
