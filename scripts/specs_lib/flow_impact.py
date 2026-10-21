"""Flow impact for local specification tooling."""
import hashlib


def paths(flow, role):
    return {ref["path"] for ref in (flow or {}).get(role, [])}


def policy(catalog):
    return {key: value for key, value in catalog.items() if key != "flows"}


def analyze(before, after, base, extra_covered=()):
    import user_flows as uf
    current = uf.validate(after, extra_covered)
    old = uf.load(before, uf.CATALOG) if uf.CATALOG in before else {"flows": []}
    if uf.CATALOG in before:
        uf.catalog_collection_shapes(old)
    old_flows = {f["id"]: f for f in old["flows"]}
    new_flows = {f["id"]: f for f in current["flows"]}
    changed = {p for p in before.keys() | after.keys() if uf.content_id(before, p) != uf.content_id(after, p)}
    modules = uf.module_paths(before) | uf.module_paths(after)
    affected = {}
    policy_changed = uf.CATALOG in before and uf.policy(old) != uf.policy(current)
    adoption = current.get("adoption", {})
    adopting = uf.CATALOG not in before and adoption.get("base") == base
    for fid in sorted(old_flows.keys() | new_flows.keys()):
        previous, flow = old_flows.get(fid), new_flows.get(fid)
        implementation = (uf.paths(previous, "implementation") | uf.paths(flow, "implementation")) & changed
        tests = (uf.paths(previous, "tests") | uf.paths(flow, "tests")) & changed
        description_changed = (uf.prose(before, previous) != uf.prose(after, flow)
                               or (previous or {}).get("scenarios") != (flow or {}).get("scenarios"))
        if not (implementation or tests or description_changed or previous != flow or policy_changed or modules & changed):
            continue
        adopted = adopting and fid in adoption.get("flows", []) and previous is None
        # Adoption documents behavior already present at the pinned initial commit.
        # Every adopted implementation/test reference must actually exist there.
        if adopted:
            adopted = all(p in before for p in uf.paths(flow, "implementation") | uf.paths(flow, "tests"))
        relevant = uf.paths(previous, "implementation") | uf.paths(flow, "implementation") | uf.paths(previous, "tests") | uf.paths(flow, "tests")
        relevant |= {f["spec"] for f in (previous, flow) if f}
        relevant |= modules
        suite_names = sorted(set((previous or {}).get("suites", []) + (flow or {}).get("suites", [])))
        suite_definitions = {name: current["suites"].get(name, old.get("suites", {}).get(name)) for name in suite_names}
        digest = hashlib.sha256(uf.canonical({
            "previous": previous, "flow": flow, "policy": uf.policy(current),
            "inputs": {p: uf.content_id(after, p) for p in sorted(relevant)},
            "suites": suite_definitions,
        })).hexdigest()
        affected[fid] = {"description_changed": description_changed, "adopted": adopted,
                         "implementation": sorted(implementation), "tests": sorted(tests),
                         "fingerprint": digest, "suites": suite_definitions}
    # Consult both catalogs so removing mappings/exclusions cannot hide a change.
    known = {uf.CATALOG} | set(extra_covered) | modules
    for flow in list(old_flows.values()) + list(new_flows.values()):
        known |= uf.paths(flow, "implementation") | uf.paths(flow, "tests") | {flow["spec"]}
    unknown = sorted(p for p in changed if p not in known and not p.startswith(uf.RECORDS)
                     and (not uf.excluded(p, current) or (uf.CATALOG in before and not uf.excluded(p, old))))
    if unknown:
        raise uf.FlowError("Unmapped changed files: " + ", ".join(unknown))
    return current, affected
