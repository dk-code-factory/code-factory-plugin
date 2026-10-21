"""Flow schema for local specification tooling."""
import re
from fnmatch import fnmatchcase


def excluded(path, catalog):
    return any(fnmatchcase(path, rule["glob"]) for rule in catalog.get("exclusions", []))


def validate(files, extra_covered=(), *, rendered=True):
    import user_flows as uf
    catalog = uf.load(files, uf.CATALOG)
    uf.catalog_collection_shapes(catalog)
    errors = []

    def need(condition, message):
        if not condition:
            errors.append(message)

    need(type(catalog.get("version")) is int and catalog["version"] in (1, 2), "Unsupported catalog version")
    suites = catalog.get("suites", {})
    need(isinstance(suites, dict) and bool(suites), "Declare verification suites")
    for name, suite in suites.items():
        commands = suite.get("commands", [])
        need(bool(commands), name + ": commands required")
        for command in commands:
            need(isinstance(command, list) and bool(command)
                 and all(isinstance(p, str) and p for p in command), name + ": command must be an argv array")
    for rule in catalog.get("exclusions", []):
        need(bool(rule.get("glob")) and bool(rule.get("reason", "").strip()), "Exclusions require a glob and reason")
    flows = catalog.get("flows", [])
    ids, specs, covered = set(), set(), set(extra_covered) | uf.module_paths(files)
    need(bool(flows), "Declare at least one flow")
    for flow in flows:
        fid = flow.get("id", "")
        need(bool(re.fullmatch(r"[a-z][a-z0-9-]*", fid)), "Invalid flow ID: " + fid)
        need(fid not in ids, "Duplicate flow: " + fid)
        ids.add(fid)
        spec = flow.get("spec", "")
        need(spec == uf.DIRECTORY + fid + ".md", fid + ": spec must match flow ID")
        need(spec not in specs, "Duplicate spec: " + spec)
        specs.add(spec)
        text = files.get(spec, b"").decode(errors="replace")
        need(bool(flow.get("title")), fid + ": title required")
        need(text.count(uf.MARKER) == 1 and text.count(uf.END) == 1 and text.index(uf.MARKER) < text.index(uf.END), fid + ": reference markers required exactly once in order")
        for section in uf.SECTIONS:
            need("## " + section + "\n" in text.split(uf.MARKER)[0], fid + ": missing " + section)
        scenarios = flow.get("scenarios", [])
        need(bool(scenarios) and len(set(scenarios)) == len(scenarios), fid + ": unique scenario IDs required")
        for scenario in scenarios:
            need("**" + scenario + "**" in text.split(uf.MARKER)[0], fid + ": scenario absent from description: " + scenario)
        accounted = set()
        need(bool(flow.get("implementation")), fid + ": implementation references required")
        need(bool(flow.get("tests")), fid + ": test references required")
        for role in ("implementation", "tests"):
            for ref in flow.get(role, []):
                path = ref.get("path", "")
                need(uf.safe_path(path) and path in files, fid + ": missing/unsafe reference: " + path)
                need(bool(ref.get("purpose", "").strip()), fid + ": reference purpose required: " + path)
                need(uf.is_test(path) == (role == "tests"), fid + ": incorrect reference role: " + path)
                covered.add(path)
                anchor = ref.get("anchor", "")
                if role == "tests":
                    need(bool(anchor), fid + ": test anchor required: " + path)
                    need(bool(ref.get("scenarios")), fid + ": test scenarios required: " + path)
                    accounted.update(ref.get("scenarios", []))
                if anchor:
                    need(files.get(path, b"").count(anchor.encode()) == 1,
                         fid + ": missing/ambiguous anchor in " + path + ": " + anchor)
        if catalog["version"] == 2:
            from specs_lib import gaps
            for gap in flow.get("gaps", []):
                errors.extend(fid + ": " + error for error in gaps.validate(
                    gap, scenario_required=True, allowed_scenarios=scenarios))
        else:
            need(all(set(gap) == {"scenario", "reason"} for gap in flow.get("gaps", [])),
                 fid + ": legacy gaps require scenario and reason only")
        for gap in flow.get("gaps", []):
            scenario, reason = gap.get("scenario"), gap.get("reason")
            need(isinstance(scenario, str) and bool(scenario.strip()), fid + ": gap scenario must be text")
            if isinstance(scenario, str):
                accounted.add(scenario)
            need(isinstance(reason, str) and bool(reason.strip()), fid + ": gap reason required")
        need(accounted == set(scenarios), fid + ": every scenario needs test coverage or an explicit gap; no unknown scenarios")
        need(bool(flow.get("suites")) and set(flow.get("suites", [])) <= set(suites), fid + ": unknown/missing suites")
        if rendered:
            try:
                need(text.split(uf.MARKER)[-1].split(uf.END)[0] == uf.references(flow).split(uf.MARKER)[-1].split(uf.END)[0], fid + ": reference block stale; run render")
            except KeyError:
                errors.append(fid + ": incomplete reference metadata")
    if catalog["version"] == 2:
        from specs_lib import gaps
        try:
            gaps.report({"entities": [], "coverage_groups": []}, catalog)
        except ValueError as error:
            errors.append(str(error))
    for path in files:
        if path.startswith(uf.DIRECTORY) and path.endswith(".md") and path != uf.DIRECTORY + "README.md":
            need(path in specs, "Unregistered flow document: " + path)
        if not uf.excluded(path, catalog) and path not in covered and path not in specs and not path.startswith(uf.RECORDS) and path != uf.CATALOG:
            errors.append("Unmapped file (map it or justify an exclusion): " + path)
    if errors:
        raise uf.FlowError("\n".join(errors))
    return catalog
