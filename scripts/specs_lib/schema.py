"""Schema for local specification tooling."""
import re
import unicodedata
from collections import defaultdict, deque


def graph_errors(graph):
    errors = []
    if not isinstance(graph, dict) or set(graph) != {"start", "nodes", "edges"}:
        return ["graph must be an object with start, nodes and edges"]
    nodes = graph.get("nodes", [])
    edges = graph.get("edges", [])
    if not isinstance(nodes, list) or not isinstance(edges, list):
        return ["graph nodes and edges must be arrays"]
    if any(not isinstance(n, dict) or set(n) - {"id", "label", "scenario", "terminal"} for n in nodes):
        return ["graph nodes must be objects with supported fields"]
    if any(not isinstance(e, dict) or set(e) - {"from", "to", "label"} for e in edges):
        return ["graph edges must be objects with supported fields"]
    if any(not isinstance(n.get("id"), str) for n in nodes) or not isinstance(graph.get("start"), str):
        return ["graph node IDs and start must be text"]
    if any("terminal" in n and type(n["terminal"]) is not bool for n in nodes):
        errors.append("graph terminal must be a boolean")
    if any("scenario" in n and (not isinstance(n["scenario"], str) or not n["scenario"].strip()) for n in nodes):
        errors.append("graph scenario must be nonempty text")
    if any(not isinstance(e.get("from"), str) or not isinstance(e.get("to"), str) for e in edges):
        return errors + ["graph edge endpoints must be text"]
    ids = [n.get("id", "") for n in nodes]
    if not ids or len(ids) != len(set(ids)) or any(not re.fullmatch(r"[A-Za-z][A-Za-z0-9_]*", i) for i in ids):
        errors.append("graph requires unique safe node IDs")
    if any(not isinstance(n.get("label"), str) or not n["label"].strip() for n in nodes):
        errors.append("graph node labels required")
    start = graph.get("start")
    terminal = {n["id"] for n in nodes if n.get("terminal") is True}
    if start not in ids or not terminal:
        errors.append("graph requires valid start and terminal outcomes")
    forward, reverse = defaultdict(set), defaultdict(set)
    for edge in edges:
        a, b = edge.get("from"), edge.get("to")
        if a not in ids or b not in ids:
            errors.append("graph edge has invalid endpoints")
        else:
            forward[a].add(b)
            reverse[b].add(a)
        if not isinstance(edge.get("label", ""), str):
            errors.append("graph edge label must be text")
    def reachable(seeds, edges):
        seen, queue = set(seeds), deque(seeds)
        while queue:
            for other in edges[queue.popleft()] - seen:
                seen.add(other)
                queue.append(other)
        return seen
    if set(ids) - reachable([start], forward):
        errors.append("graph has unreachable steps")
    if set(ids) - reachable(terminal, reverse):
        errors.append("graph has steps without a path to a terminal outcome")
    if any(forward[n] for n in terminal) or any(not forward[n] for n in set(ids) - terminal):
        errors.append("only terminal outcomes may have no outgoing edges")
    return errors


def registry_collection_shapes(terms, ui, catalog):
    """Reject non-array registries before rendering, relationships or coverage."""
    import user_flows as uf
    uf.require_object(terms, "Glossary registry")
    uf.require_object(ui, "UI registry")
    uf.catalog_collection_shapes(catalog)
    for term in uf.array_field(terms, "terms", "Glossary registry", dict):
        uf.array_field(term, "aliases", str(term.get("id", "Glossary term")))
    for field in ("entities", "instances", "assertions", "source_map", "coverage_groups", "documents"):
        uf.array_field(ui, field, "UI registry", dict)
    for entity in ui["entities"]:
        context = str(entity.get("id", "UI entity"))
        for field in ("terms", "contains", "states", "assertions", "suites"):
            uf.array_field(entity, field, context)
        uf.array_field(entity, "gaps", context, dict if ui.get("version") == 2 else str)
        for field in ("elements", "implementation"):
            uf.array_field(entity, field, context, dict)
    for ref in ui["source_map"]:
        for field in ("anchors", "location_labels"):
            uf.array_field(ref, field, "UI source inventory", optional=True)
    for group in ui["coverage_groups"]:
        for field in ("entities", "assertions"):
            uf.array_field(group, field, "UI coverage inventory")
    for doc in ui["documents"]:
        for block in uf.array_field(doc, "blocks", "UI document recipe", dict):
            for field in ("ids", "fields", "headers"):
                uf.array_field(block, field, "UI document recipe", optional=True)
    for flow in catalog["flows"]:
        for field in ("terms", "ui"):
            uf.array_field(flow, field, str(flow.get("id", "Flow")))


def validate(files, *, rendered=True, previous_catalog=None):
    import specs as api
    import user_flows as uf
    errors = []
    def need(ok, message):
        if not ok:
            errors.append(message)
    terms, ui, catalog = (uf.load(files, p) for p in (api.TERMS, api.UI, uf.CATALOG))
    uf.require_object(catalog, "Catalog")
    suites = catalog.get("suites")
    if not isinstance(suites, dict) or not isinstance(suites.get("tooling"), dict):
        raise uf.FlowError("Required tooling verification suite is missing or invalid")
    api.registry_collection_shapes(terms, ui, catalog)
    recipe_errors = api.render_recipe_errors(files, ui, catalog)
    errors.extend(recipe_errors)
    # Render repairs obsolete owned bodies. Check their ownership/markers before
    # excluding them from preflight content checks; registries and all authored
    # content remain inputs. The complete generated candidate is checked again
    # with rendered=True before any output is written.
    content_files = files
    if not rendered and not recipe_errors:
        content_files = {path: api.authored(files, path) if path.startswith(("docs/glossary/", "docs/ui/")) and path.endswith(".md") else data
                         for path, data in files.items()}
    glossary = content_files.get("docs/glossary/README.md", b"").decode()
    for name, suite in suites.items():
        if not isinstance(suite, dict):
            raise uf.FlowError(name + ": verification suite must be an object")
        commands = suite.get("commands")
        if not isinstance(commands, list) or not commands or any(not isinstance(c, list) or not c or any(not isinstance(a, str) or not a for a in c) for c in commands):
            raise uf.FlowError(name + ": commands must be nonempty argv arrays")
        explicit = suite.get("inputs", [])
        if not isinstance(explicit, list) or any(not isinstance(p, str) or not uf.safe_path(p) or not api.suite_path_files(files, p) for p in explicit):
            raise uf.FlowError(name + ": suite inputs must be safe existing repository paths")
    if "adoption" in catalog:
        adoption = catalog["adoption"]
        need(isinstance(adoption, dict) and set(adoption) == {"base", "flows"}
             and adoption.get("base") == api.BASELINE and isinstance(adoption.get("flows"), list)
             and all(isinstance(fid, str) for fid in adoption["flows"])
             and len(adoption["flows"]) == len(api.ADOPTION_FLOWS) and set(adoption["flows"]) == api.ADOPTION_FLOWS,
             "Initial adoption must retain the pinned base and fourteen product flows")
    need(type(terms.get("version")) is int and terms["version"] == 1
         and type(ui.get("version")) is int and ui["version"] in (1, 2), "Unsupported registry version")
    names, term_ids = {}, set()
    for t in terms["terms"]:
        tid = t["id"]
        need(bool(re.fullmatch(r"[a-z][a-z0-9-]*", tid)) and tid not in term_ids, "Invalid/duplicate term ID: " + tid)
        term_ids.add(tid)
        need(set(t) <= {"id", "name", "aliases", "category", "definition", "scope"}, tid + ": unknown glossary fields")
        need(all(isinstance(t.get(k), str) and t[k].strip() for k in ("name", "category", "definition")), tid + ": missing glossary definition/name/category")
        need(isinstance(t["aliases"], list) and all(isinstance(a, str) and a.strip() for a in t["aliases"]), tid + ": aliases must be names")
        need("scope" not in t or isinstance(t["scope"], str) and bool(re.fullmatch(r"[a-z][a-z0-9-]*", t["scope"])), tid + ": naming scope must be a nonempty term ID")
        scope = t.get("scope") if isinstance(t.get("scope"), str) else None
        for name in [t["name"]] + t["aliases"]:
            key = (scope, unicodedata.normalize("NFC", " ".join(name.casefold().split())))
            need(key not in names, tid + ": duplicate/ambiguous scoped name " + name)
            names[key] = tid
        definition = t["definition"]
        need(not api.definition_has_reference(definition, glossary), tid + ": definition contains a link, path, implementation reference or procedure")
    for t in terms["terms"]:
        need("scope" not in t or isinstance(t["scope"], str) and t["scope"] in term_ids, t["id"] + ": unknown naming scope")
    entities = {e["id"]: e for e in ui["entities"]}
    need(len(entities) == len(ui["entities"]), "Duplicate UI entity ID")
    assertions = {a["id"]: a for a in ui["assertions"]}
    need(len(assertions) == len(ui["assertions"]), "Duplicate UI assertion ID")
    orphan_assertions = set(assertions) - {aid for e in ui["entities"] for aid in e["assertions"]}
    need(not orphan_assertions, "UI assertions need entity owners: " + ", ".join(sorted(orphan_assertions)))
    valid_kinds = {"widget", "pane", "page", "dialog", "shell", "connection"}
    common_fields = {"id", "kind", "terms", "contains", "elements", "states", "implementation",
                     "assertions", "gaps", "suites", "interactions", "composition", "context",
                     "purpose", "element_description", "state_description"}
    connection_fields = {"from", "to", "connection_type", "external_boundary", "endpoint_description", "trigger"}
    for e in ui["entities"]:
        eid = e["id"]
        need(set(e) <= common_fields | connection_fields, eid + ": unknown UI entity fields")
        need(e["kind"] == "connection" or not (set(e) & connection_fields), eid + ": connection fields require connection kind")
        need(bool(re.fullmatch(r"(?:W|P|PAGE|D|S|C)-[A-Z][A-Z0-9-]*", eid)), "Invalid UI ID: " + eid)
        need(e["kind"] in valid_kinds, eid + ": invalid entity kind")
        prefix = {"widget": "W-", "pane": "P-", "page": "PAGE-", "dialog": "D-", "shell": "S-", "connection": "C-"}.get(e["kind"], "?")
        need(eid.startswith(prefix), eid + ": ID/kind mismatch")
        need(bool(e["terms"]) and set(e["terms"]) <= term_ids, eid + ": missing glossary definition")
        need(len(e["contains"]) == len(set(e["contains"])), eid + ": duplicate containment")
        allowed = {"shell": {"pane", "page", "dialog"}, "page": {"pane"}, "dialog": {"pane"}}
        for child in e["contains"]:
            need(child in entities and entities[child]["kind"] in allowed.get(e["kind"], set()), eid + ": invalid containment " + child)
        elements = e["elements"]
        need(isinstance(elements, list), eid + ": elements must be an array")
        elements = elements if isinstance(elements, list) else []
        need(not elements or e["kind"] == "widget", eid + ": only widgets own elements")
        element_ids = []
        for el in elements:
            shaped = isinstance(el, dict) and set(el) == {"id", "term"} and isinstance(el.get("id"), str) and isinstance(el.get("term"), str)
            need(shaped, eid + ": elements require only text id and term fields")
            if shaped:
                element_ids.append(el["id"])
                need(bool(re.fullmatch(re.escape(eid) + r"\.[a-z][a-z0-9-]*", el["id"])) and el["term"] in term_ids,
                     eid + ": invalid element or missing glossary definition")
        need(len(set(element_ids)) == len(element_ids), eid + ": duplicate element")
        need(set(e["states"]) <= term_ids, eid + ": undefined state term")
        need(bool(e["implementation"]), eid + ": implementation references required")
        need(bool(e["assertions"] or e["gaps"]), eid + ": test assertion or explicit coverage gap required")
        need(ui["version"] == 2 or all(isinstance(gap, str) and gap.strip() for gap in e["gaps"]), eid + ": coverage gaps require substantive reasons")
        need(set(e["assertions"]) <= set(assertions), eid + ": undefined assertion")
        need(bool(e["suites"]) and set(e["suites"]) <= set(catalog["suites"]), eid + ": unknown verification suite")
        if e["kind"] == "connection":
            need(isinstance(e.get("connection_type"), str) and e["connection_type"] in term_ids, eid + ": undefined connection type")
            need("external_boundary" not in e or type(e["external_boundary"]) is bool, eid + ": external boundary must be boolean")
            for field in ("endpoint_description", "trigger"):
                need(field not in e or isinstance(e[field], str) and bool(e[field].strip()), eid + ": " + field + " must be nonempty text")
            endpoints = []
            for field in ("from", "to"):
                values = e.get(field)
                shaped = isinstance(values, list) and bool(values) and all(isinstance(v, str) and bool(v.strip()) for v in values)
                need(shaped, eid + ": connection endpoints required as nonempty text arrays")
                if shaped:
                    need(len(values) == len(set(values)), eid + ": duplicate connection endpoint")
                    endpoints.extend(values)
            for endpoint in endpoints:
                target = entities.get(endpoint.split(".")[0])
                valid_element = target is not None and target["kind"] == "widget" and isinstance(target["elements"], list) and any(
                    isinstance(el, dict) and el.get("id") == endpoint for el in target["elements"])
                need(target is not None and target["kind"] in valid_kinds - {"connection"} and (
                    endpoint == target["id"] or valid_element and bool(re.fullmatch(re.escape(target["id"]) + r"\.[a-z][a-z0-9-]*", endpoint))),
                    eid + ": invalid connection endpoint " + endpoint)
        for ref in e["implementation"]:
            path, anchor = ref["path"], ref.get("anchor")
            need(uf.safe_path(path) and path in files and not uf.is_test(path), eid + ": missing/invalid implementation " + path)
            need(bool(ref.get("purpose")), eid + ": implementation purpose required")
            if anchor:
                need(files.get(path, b"").count(anchor.encode()) == 1, eid + ": missing/ambiguous source anchor " + anchor)
    visiting, visited = set(), set()
    def visit(eid):
        if eid in visiting:
            errors.append("Containment cycle: " + eid)
            return
        if eid in visited or eid not in entities:
            return
        visiting.add(eid)
        for child in entities[eid]["contains"]:
            visit(child)
        visiting.remove(eid)
        visited.add(eid)
    for eid in entities:
        visit(eid)
    instance_ids, instanced, instance_panes = set(), set(), set()
    for i in ui["instances"]:
        fields = {"id", "type", "pane", "repeat"}
        shaped = set(i) == fields and all(isinstance(i[field], str) and i[field].strip() for field in fields)
        need(shaped, "Displayed instances require nonempty text id, type, pane and repeat fields only")
        if not shaped:
            continue
        need(i["id"] not in instance_ids, "Duplicate displayed instance: " + i["id"])
        instance_ids.add(i["id"])
        need(i["type"] in entities and entities[i["type"]]["kind"] == "widget" and i["pane"] in entities and entities[i["pane"]]["kind"] == "pane", "Invalid widget instance: " + i["id"])
        instanced.add(i["type"])
        instance_panes.add(i["pane"])
    need({e["id"] for e in entities.values() if e["kind"] == "widget"} <= instanced, "Widget types need displayed instances")
    children = {c for e in entities.values() for c in e["contains"]}
    need({e["id"] for e in entities.values() if e["kind"] == "pane"} <= children, "Panes need containing surfaces")
    need({e["id"] for e in entities.values() if e["kind"] == "pane"} <= instance_panes, "Panes need widget instances")
    for a in ui["assertions"]:
        need(uf.safe_path(a["path"]) and uf.is_test(a["path"]) and a["path"] in files, a["id"] + ": missing test reference")
        need(bool(a.get("anchor")) and files.get(a["path"], b"").count(a["anchor"].encode()) == 1, a["id"] + ": missing/ambiguous assertion anchor")
        need(bool(a.get("assertion", "").strip()), a["id"] + ": assertion meaning required")
    for ref in ui["source_map"]:
        for anchor in ref.get("anchors", [ref["anchor"]]):
            need(files.get(ref["path"], b"").count(anchor.encode()) == 1, "UI source inventory: missing/ambiguous anchor " + anchor)
        need(uf.safe_path(ref["path"]) and ref["path"] in files and bool(ref.get("anchor")) and files.get(ref["path"], b"").count(ref["anchor"].encode()) == 1, "UI source inventory: invalid source anchor")
    need({r["path"] for r in ui["source_map"]} <= {r["path"] for e in ui["entities"] for r in e["implementation"]}, "UI source inventory paths need entity ownership mappings")
    for group in ui["coverage_groups"]:
        need(set(group["entities"]) <= set(entities) and set(group["assertions"]) <= set(assertions) and bool(group["gap"]), "UI coverage inventory: invalid references or missing coverage limits")
        need(ui["version"] == 2 or isinstance(group["gap"], str) and bool(group["gap"].strip()), "Legacy UI coverage gaps require nonempty text")
    doc_paths, owned = set(), set()
    for doc in ui["documents"]:
        need(doc["path"].startswith("docs/ui/") and uf.safe_path(doc["path"]) and doc["path"] in files and doc["path"] not in doc_paths, "Invalid/duplicate UI document recipe")
        doc_paths.add(doc["path"])
        block_names = set()
        for block in doc["blocks"]:
            need(block["name"] not in block_names, "Duplicate document block")
            block_names.add(block["name"])
            if block["kind"] == "entities":
                need(set(block["ids"]) <= set(entities), "Undefined entity in render recipe")
                owned.update(block["ids"])
                need(all(field in entities[eid] for eid in block["ids"] if eid in entities for field in block["fields"]), "Undefined field in render recipe")
    need(set(entities) - {"S-FACTORY"} <= owned or not ui["source_map"], "UI entities need generated inventory rows")
    for f in catalog["flows"]:
        need(bool(f["terms"]) and set(f["terms"]) <= term_ids, f["id"] + ": undefined glossary references")
        need(set(f["ui"]) <= set(entities), f["id"] + ": undefined UI references")
        graph_issues = api.graph_errors(f.get("graph"))
        errors.extend(f["id"] + ": " + err for err in graph_issues)
        if not graph_issues:
            need({n["scenario"] for n in f["graph"]["nodes"] if "scenario" in n} == set(f["scenarios"]), f["id"] + ": graph scenario coverage mismatch")
    from specs_lib.gap_inventory import inventory
    try:
        inventory(ui, catalog)
    except ValueError as error:
        errors.append(str(error))
    errors.extend(api.dependency_errors(content_files, catalog, previous_catalog))
    errors.extend(api.validate_agents(files))
    try:
        uf.validate(files, api.ui_paths(ui), rendered=rendered)
    except uf.FlowError as error:
        errors.append(str(error))
    if not errors and rendered:
        for path, text in api.generated(files).items():
            need(files.get(path) == text.encode(), path + ": generated output stale; run render")
    if errors:
        raise uf.FlowError("\n".join(errors))
    return terms, ui, catalog
