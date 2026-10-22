"""Impact for local specification tooling."""
import hashlib
from collections import defaultdict, deque


def digest(value):
    import user_flows as uf
    return hashlib.sha256(uf.canonical(value)).hexdigest()


def ui_paths(ui):
    return {r["path"] for e in ui["entities"] for r in e["implementation"]} | {a["path"] for a in ui["assertions"]} | {r["path"] for r in ui.get("source_map", [])}


def model(files):
    import specs as api
    import user_flows as uf
    terms = uf.load(files, api.TERMS) if api.TERMS in files else {"terms": []}
    ui = uf.load(files, api.UI) if api.UI in files else {"entities": [], "instances": [], "assertions": []}
    catalog = uf.load(files, uf.CATALOG) if uf.CATALOG in files else {"flows": []}
    objects, deps, inputs = {}, defaultdict(set), defaultdict(set)
    for t in terms["terms"]:
        key = "term:" + t["id"]
        objects[key] = t
        if t.get("scope"):
            deps[key].add("term:" + t["scope"])
    assertions = {a["id"]: a for a in ui["assertions"]}
    for e in ui["entities"]:
        key = "ui:" + e["id"]
        objects[key] = {"entity": e, "instances": [i for i in ui["instances"] if i["type"] == e["id"] or i["pane"] == e["id"]],
                        "assertions": [assertions[a] for a in e["assertions"] if a in assertions]}
        deps[key].update("term:" + t for t in e["terms"] + e["states"] + [el["term"] for el in e["elements"]] + ([e["connection_type"]] if e["kind"] == "connection" and e.get("connection_type") else []))
        deps[key].update("ui:" + c for c in e["contains"])
        deps[key].update("ui:" + i["type"] for i in ui["instances"] if i["pane"] == e["id"])
        endpoints = e.get("from", []) + e.get("to", []) if e["kind"] == "connection" else []
        deps[key].update("ui:" + c.split(".")[0] for c in endpoints)
        # Consumers of an interaction are affected by changes to its contract.
        for endpoint in endpoints:
            deps["ui:" + endpoint.split(".")[0]].add(key)
        inputs[key].update(r["path"] for r in e["implementation"])
        inputs[key].update(assertions[a]["path"] for a in e["assertions"] if a in assertions)
    for f in catalog["flows"]:
        key = "flow:" + f["id"]
        objects[key] = {"flow": f, "prose": (uf.prose(files, f) or b"").decode()}
        deps[key].update("term:" + t for t in f.get("terms", []))
        deps[key].update("ui:" + e for e in f.get("ui", []))
        inputs[key].update(uf.paths(f, "implementation") | uf.paths(f, "tests"))
    # Older registries admitted assertion inventory entries without an entity
    # owner. Retiring that inventory still needs conservative UI review. Keep
    # its old definitions/content without assigning it false behavioral owners;
    # a UI-free registry falls back to its existing flow review subjects.
    owned = {aid for e in ui["entities"] for aid in e["assertions"]}
    unowned = [a for a in ui["assertions"] if a["id"] not in owned]
    if unowned:
        unowned_inputs = {a["path"]: uf.content_id(files, a["path"]) for a in unowned}
        keys = [key for key in objects if key.startswith("ui:")] or [key for key in objects if key.startswith("flow:")]
        for key in keys:
            objects[key]["unowned_assertions"] = unowned
            objects[key]["unowned_assertion_inputs"] = unowned_inputs
    return objects, deps, inputs


def suite_path_files(files, path):
    path = path.removeprefix("./").rstrip("/")
    return {p for p in files if p == path or p.startswith(path + "/")}


def suite_inputs(files, suite):
    """Include declared dependencies and repository paths passed to executables.

    Expand a selector scope in one snapshot. Callers must expand historical and
    current scopes against both snapshots, including newly discovered files.
    Explicit inputs cover indirect build and discovery dependencies absent in argv.
    """
    import specs as api
    import user_flows as uf
    paths = set()
    for path in suite.get("inputs", []) + [a for c in suite.get("commands", []) for a in c]:
        path = path.removeprefix("./").rstrip("/")
        if uf.safe_path(path):
            paths.update(api.suite_path_files(files, path))
    return paths


def contract_suites(obj):
    """Return a contract's declared suites, without implicit tooling coverage."""
    if "flow" in obj:
        return set(obj["flow"]["suites"])
    if "entity" in obj:
        return set(obj["entity"]["suites"])
    return set()


def current_verification(old, new, deps, oldinputs, newinputs, suites):
    """Select runnable current requirements while retaining retired coverage.

    A retired contract can use a still-declared suite, or current requirements
    of contracts sharing its mapped inputs or explicitly consuming it. Mere
    shared glossary vocabulary does not establish replacement coverage.
    """
    import specs as api
    import user_flows as uf
    reverse = defaultdict(set)
    for consumer, dependencies in deps.items():
        for dependency in dependencies:
            reverse[dependency].add(consumer)
    selected = {}
    for key in old.keys() | new.keys():
        names = {"tooling"}
        if key in new:
            names.update(api.contract_suites(new[key]))
        else:
            previous = api.contract_suites(old[key])
            names.update(previous & suites.keys())
            retired = previous - suites.keys()
            if retired:
                consumers, queue = set(), deque([key])
                while queue:
                    for consumer in reverse[queue.popleft()] - consumers:
                        consumers.add(consumer)
                        queue.append(consumer)
                related = {candidate for candidate in new
                           if candidate in consumers or oldinputs[key] & newinputs[candidate]}
                replacements = set().union(*(api.contract_suites(new[candidate]) for candidate in related))
                if not replacements:
                    raise uf.FlowError(key + ": retired verification suite(s) " + ", ".join(sorted(retired)) +
                                       " need current replacement verification on a related contract "
                                       "(shared implementation/tests or a consuming contract)")
                names.update(replacements)
        selected[key] = names
    return selected


def analyze(before, after, base):
    import specs as api
    import user_flows as uf
    if all(path in before for path in (api.TERMS, api.UI, uf.CATALOG)):
        api.registry_collection_shapes(*(uf.load(before, path) for path in (api.TERMS, api.UI, uf.CATALOG)))
    terms, ui, catalog = api.validate(after, previous_catalog=uf.load(before, uf.CATALOG) if uf.CATALOG in before else None)
    _, legacy = uf.analyze(before, after, base, api.ui_paths(ui) | (api.ui_paths(uf.load(before, api.UI)) if api.UI in before else set()))
    old, olddeps, oldinputs = api.model(before)
    new, newdeps, newinputs = api.model(after)
    changed = {p for p in before.keys() | after.keys() if uf.content_id(before, p) != uf.content_id(after, p)}
    deps = {k: olddeps[k] | newdeps[k] for k in old.keys() | new.keys()}
    inputs = {k: oldinputs[k] | newinputs[k] for k in deps}
    verification_inputs = defaultdict(set)
    verification_suites = defaultdict(set)
    old_catalog = uf.load(before, uf.CATALOG) if uf.CATALOG in before else {"flows": [], "suites": {}}
    old_suites, new_suites = old_catalog.get("suites", {}), catalog["suites"]
    suite_definitions = {name: {"previous": old_suites.get(name), "current": new_suites.get(name)}
                         for name in old_suites.keys() | new_suites.keys()}
    for objects in (old, new):
        for key, obj in objects.items():
            names = {"tooling"}
            if key.startswith("flow:"):
                names.update(obj["flow"]["suites"])
            elif key.startswith("ui:"):
                names.update(obj["entity"]["suites"])
            verification_suites[key].update(names)
    executable_suites = api.current_verification(old, new, deps, oldinputs, newinputs, new_suites)
    for key, names in executable_suites.items():
        # Replacement coverage is also an input to freshness.
        verification_suites[key].update(names)
    for key, names in verification_suites.items():
        # Retain selector scopes as well as files: a removed directory input or
        # argv path still covers additions in the current tree. Current scopes
        # also retain paths deleted from the baseline. Never merge snapshots;
        # fingerprints must preserve each path's actual absence and file mode.
        for name in names:
            for suite in suite_definitions[name].values():
                for files in (before, after):
                    verification_inputs[key].update(api.suite_inputs(files, suite or {}))
    # Historical ownerless paths remain freshness inputs after removal, even
    # when the current catalog excludes them. They cannot supply an entity's
    # implementation/test cochanges because the old inventory had no owner.
    for key, obj in old.items():
        verification_inputs[key].update(obj.get("unowned_assertion_inputs", {}))
    reasons = defaultdict(set)
    for key in deps:
        if old.get(key) != new.get(key):
            reasons[key].add("definition or mapping changed")
        reasons[key].update("mapped file changed: " + p for p in inputs[key] & changed)
        reasons[key].update("verification input changed: " + p for p in verification_inputs[key] & changed)
        reasons[key].update("verification suite changed: " + name for name in verification_suites[key]
                            if old_suites.get(name) != new_suites.get(name))
    # The legacy policy includes every suite. Unified reviews instead select
    # actual suite consumers and let the dependency graph propagate impact.
    policy = {k: v for k, v in uf.policy(catalog).items() if k != "suites"}
    previous_policy = {k: v for k, v in uf.policy(old_catalog).items() if k != "suites"}
    for fid in legacy:
        key = "flow:" + fid
        if old.get(key) != new.get(key) or inputs[key] & changed or policy != previous_policy:
            reasons[key].add("flow contract or policy changed")
    # Authored structural prose is reviewed too, even when registries are intact.
    for directory, prefix in (("docs/glossary/", "term:"), ("docs/ui/", "ui:")):
        if any(p.startswith(directory) and p.endswith(".md") and api.authored(before, p) != api.authored(after, p) for p in changed):
            for key in deps:
                if key.startswith(prefix):
                    reasons[key].add("layer documentation changed")
    # Render recipes and source/coverage inventories also require review.
    if api.UI in before:
        prior_ui = uf.load(before, api.UI)
        if {k: v for k, v in prior_ui.items() if k not in ("entities", "instances", "assertions")} != {k: v for k, v in ui.items() if k not in ("entities", "instances", "assertions")}:
            for key in deps:
                if key.startswith("ui:"):
                    reasons[key].add("UI rendering or coverage metadata changed")
    tooling = {p for p in before.keys() | after.keys() if p.startswith((".codex/agents/", ".agents/skills/"))}
    tooling |= {"scripts/specs.py", "scripts/user_flows.py", "scripts/test_specs.py", "scripts/test_user_flows.py",
                "Makefile", "scripts/verify.sh", "AGENTS.md", "docs/specs/README.md"}
    tooling |= {p for p in before.keys() | after.keys() if p.startswith("scripts/test_specs") and p.endswith(".py")}
    tooling |= uf.module_paths(before) | uf.module_paths(after)
    if tooling & changed:
        for key in deps:
            reasons[key].add("specification tooling or maintenance instructions changed")
    reverse = defaultdict(set)
    for key, dependencies in deps.items():
        for d in dependencies:
            reverse[d].add(key)
    queue, affected_keys = deque(k for k, v in reasons.items() if v), {k for k, v in reasons.items() if v}
    while queue:
        key = queue.popleft()
        for consumer in sorted(reverse[key]):
            reasons[consumer].add("depends on " + key)
            if consumer not in affected_keys:
                affected_keys.add(consumer)
                queue.append(consumer)
    def closure(seeds):
        seen, queue = set(seeds), deque(seeds)
        while queue:
            for d in deps.get(queue.popleft(), set()) - seen:
                seen.add(d)
                queue.append(d)
        return seen
    def cochanges(seeds):
        related = set().union(*(inputs.get(k, set()) for k in closure(seeds))) & changed
        return {"implementation": sorted(p for p in related if not uf.is_test(p)),
                "tests": sorted(p for p in related if uf.is_test(p))}
    def ui_adoption_eligible(key):
        if api.UI in before or base != api.BASELINE or key not in new:
            return False
        obj = new[key]
        references = obj["entity"]["implementation"] + obj["assertions"]
        return all(ref["path"] in before and (not ref.get("anchor") or
                   before[ref["path"]].count(ref["anchor"].encode()) == 1) for ref in references)
    report = {"terms": [], "entities": [], "flows": []}
    for key in sorted(affected_keys):
        layer, name = key.split(":", 1)
        report[{"term": "terms", "ui": "entities", "flow": "flows"}[layer]].append({"id": name, "reasons": sorted(reasons[key]), "role": api.ROLES["glossary" if layer == "term" else layer]})
    groups = {}
    for key in sorted(affected_keys):
        layer, name = key.split(":", 1)
        subject = "flow-" + name if layer == "flow" else ("glossary" if layer == "term" else "ui")
        groups.setdefault(subject, set()).add(key)
    affected = {}
    all_flows = {f["id"]: f for f in catalog["flows"]}
    for subject, seeds in groups.items():
        relevant = closure(seeds)
        paths = set().union(*(inputs.get(k, set()) | verification_inputs[k] for k in relevant)) | tooling
        descriptions = {k: new.get(k) for k in relevant}
        suite_names = {"tooling"}
        if subject.startswith("flow-"):
            fid = subject[5:]
            item = legacy.get(fid, {"description_changed": False, "adopted": False, "implementation": [], "tests": [], "suites": {}}).copy()
            previous_flow = next((f for f in old_catalog["flows"] if f["id"] == fid), None)
            if previous_flow and fid in all_flows and previous_flow.get("graph") != all_flows[fid].get("graph"):
                item["description_changed"] = True
            # Consumed UI owns implementation/assertions too; callers need not
            # duplicate these mappings in every consuming flow.
            item.update(cochanges(seeds))
            # The legacy gate's catalog-controlled adoption is not authority for
            # the unified, one-time production migration.
            item["adopted"] = bool(item["adopted"] and base == api.BASELINE and fid in api.ADOPTION_FLOWS)
        else:
            item = {"description_changed": subject == "ui" and any(old.get(k) != new.get(k) for k in seeds),
                    "adopted": subject == "ui" and api.UI not in before and base == api.BASELINE and all(p in before for k in seeds for p in inputs[k]),
                    "implementation": sorted(p for k in seeds for p in inputs[k] & changed if not uf.is_test(p)),
                    "tests": sorted(p for k in seeds for p in inputs[k] & changed if uf.is_test(p))}
        for key in relevant:
            suite_names.update(executable_suites[key])
        if subject == "glossary":
            item["adopted"] = api.TERMS not in before and base == api.BASELINE
        item["lifecycle_changed"] = subject != "glossary" and any((k not in old) != (k not in new) for k in seeds)
        if subject == "ui":
            # Aggregate reports must not let an unrelated entity's edits satisfy
            # a new, retired or directly changed entity's behavioral contract.
            # Merely affected consumers still receive review, without acquiring
            # an independent implementation/test change requirement.
            item["contracts"] = {
                k: {**cochanges({k}), "lifecycle_changed": (k not in old) != (k not in new),
                    "adopted": ui_adoption_eligible(k)}
                for k in sorted(seeds) if old.get(k) != new.get(k) or inputs[k] & changed
            }
            # Plain adoption still applies only when the whole direct scope is
            # eligible. A mixed review must explicitly classify every contract.
            item["adopted"] = bool(item["contracts"]) and all(contract["adopted"] for contract in item["contracts"].values())
        item["suites"] = {s: new_suites[s] for s in sorted(suite_names)}
        item["subjects"] = sorted(seeds)
        item["dependencies"] = {k: api.digest({"definition": new.get(k), "previous": old.get(k),
                                          "suites": {s: suite_definitions[s] for s in sorted(verification_suites[k])},
                                          "inputs": {p: uf.content_id(after, p) for p in sorted(inputs.get(k, set()) | verification_inputs[k])}}) for k in sorted(relevant)}
        item["fingerprint"] = api.digest({"base": base, "subjects": descriptions, "previous": {k: old.get(k) for k in relevant},
                                      "inputs": {p: uf.content_id(after, p) for p in sorted(paths)}, "policy": policy,
                                      "verification_suites": {s: suite_definitions[s] for k in sorted(relevant) for s in sorted(verification_suites[k])},
                                      "layer_prose": {p: api.digest(api.authored(after, p).decode()) for p in before.keys() | after.keys()
                                                      if p.endswith(".md") and (p.startswith("docs/glossary/") or p.startswith("docs/ui/") and any(k.startswith("ui:") for k in relevant))},
                                      "ui_metadata": {k: v for k, v in ui.items() if k not in ("entities", "instances", "assertions")} if any(k.startswith("ui:") for k in relevant) else None,
                                      "review_contracts": item.get("contracts", {}),
                                      "suites": item["suites"]})
        affected[subject] = item
    return report, affected
