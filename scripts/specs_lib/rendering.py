"""Rendering for local specification tooling."""
from specs_lib.gap_inventory import render_gap
import re


def esc(value):
    # Only quoted labels are emitted. Encode punctuation as Mermaid decimal
    # entities; neither markup, edge syntax nor directives can escape a label.
    return "".join(c if c.isalnum() or c in " -_.,:/" else f"#{ord(c)};" for c in value)


def flow_diagram(graph):
    import specs as api
    import user_flows as uf
    errors = api.graph_errors(graph)
    if errors:
        raise uf.FlowError("; ".join(errors))
    lines = ["```mermaid", "flowchart TD"]
    # Registry IDs are semantic keys, never Mermaid syntax (e.g. reserved `end`).
    aliases = {node["id"]: f"N{i}" for i, node in enumerate(graph["nodes"])}
    for node in graph["nodes"]:
        label = api.esc(node["label"] + (" (" + node["scenario"] + ")" if node.get("scenario") else ""))
        shape = f'(["{label}"])' if node.get("terminal") is True else f'["{label}"]'
        lines.append("    " + aliases[node["id"]] + shape)
    for edge in graph["edges"]:
        label = '|"' + api.esc(edge["label"]) + '"|' if edge.get("label") else ""
        lines.append(f'    {aliases[edge["from"]]} -->{label} {aliases[edge["to"]]}')
    return "\n".join(lines + ["```"])


def marker(name):
    return f"<!-- specs:{name} -->", f"<!-- /specs:{name} -->"


def replace_block(text, name, body):
    import specs as api
    import user_flows as uf
    start, end = api.marker(name)
    if text.count(start) != 1 or text.count(end) != 1 or text.index(start) > text.index(end):
        raise uf.FlowError("Missing, duplicate or reversed generated markers: " + name)
    prefix, rest = text.split(start)
    _, suffix = rest.split(end)
    return prefix + start + "\n" + body.rstrip() + "\n" + end + suffix


def table(headers, rows):
    import user_flows as uf
    if not isinstance(headers, list) or not headers or any(not isinstance(h, str) or not h.strip() for h in headers):
        raise uf.FlowError("Table headers must be nonempty strings")
    if any(not isinstance(row, (list, tuple)) or len(row) != len(headers) for row in rows):
        raise uf.FlowError("Table row width must match headers")
    def cell(s):
        return str(s).replace("\r\n", "\n").replace("\r", "\n").replace("|", "&#124;").replace("\n", "<br>")
    return "\n".join(["| " + " | ".join(cell(h) for h in headers) + " |", "| " + " | ".join("---" for _ in headers) + " |"]
                     + ["| " + " | ".join(cell(c) for c in row) + " |" for row in rows])


def generated(files):
    import specs as api
    import user_flows as uf
    terms, ui, catalog = (uf.load(files, p) for p in (api.TERMS, api.UI, uf.CATALOG))
    issues = api.render_recipe_errors(files, ui, catalog)
    if issues:
        raise uf.FlowError("\n".join(issues))
    entities = {e["id"]: e for e in ui["entities"]}
    result = {}
    lines = []
    for term in terms["terms"]:
        lines.extend([f'<a id="{term["id"]}"></a>', f'## {term["name"]}', "",
                      f'`{term["id"]}` · {term["category"]}' + (f' · scope: {term["scope"]}' if term.get("scope") else ""), "",
                      term["definition"], ""])
        if term["aliases"]:
            lines.extend(["Aliases: " + ", ".join(term["aliases"]) + ".", ""])
    path = "docs/glossary/README.md"
    result[path] = api.replace_block(files[path].decode(), "terms", "\n".join(lines))
    for doc in ui["documents"]:
        path = doc["path"]
        text = files[path].decode()
        for block in doc["blocks"]:
            rows = []
            kind = block["kind"]
            if kind == "entities":
                for eid in block["ids"]:
                    e = entities[eid]
                    rows.append([f'`{eid}`'] + ["<br>".join(render_gap(gap) for gap in e[field]) if field == "gaps" and any(isinstance(gap, dict) for gap in e[field]) else e[field] for field in block["fields"]])
            elif kind == "terminology":
                names = {t["id"]: t["name"] for t in terms["terms"]}
                rows = [[f'<a id="{e["id"]}"></a>`{e["id"]}`', e["kind"], ", ".join(f'[{names[t]}](../glossary/README.md#{t})' for t in e["terms"])] for e in entities.values()]
            elif kind == "sources":
                rows = [[f'[{r["path"]}](../../{r["path"]}) — ' + ", ".join(f'`{a}`' for a in r.get("location_labels", [r["anchor"]])), r["entities"], r["detail"]] for r in ui["source_map"]]
            elif kind == "assertions":
                rows = [[f'`{r["id"]}`', f'[{r["path"]}](../../{r["path"]}) — `{r["anchor"]}`', r["assertion"]] for r in ui["assertions"]]
            elif kind == "coverage":
                rows = [[g["name"], ", ".join(f'`{i}`' for i in g["assertions"]) or "None", render_gap(g["gap"])] for g in ui["coverage_groups"]]
            elif kind == "instances":
                rows = [[f'`{i["id"]}`', f'`{i["type"]}`', f'`{i["pane"]}`', i["repeat"]] for i in ui["instances"]]
            elif kind == "containment":
                ids = {e: "N" + str(i) for i, e in enumerate(entities)}
                lines = ["```mermaid", "flowchart TD"]
                for eid, e in entities.items():
                    if e["kind"] != "connection":
                        lines.append(f'    {ids[eid]}["{api.esc(eid)}"]')
                for e in entities.values():
                    for child in e["contains"]:
                        lines.append(f'    {ids[e["id"]]} --> {ids[child]}')
                # Instances make reuse explicit; types have no shared draft state.
                for i, instance in enumerate(ui["instances"]):
                    lines.append(f'    I{i}["{api.esc(instance["id"])}"]')
                    lines.append(f'    {ids[instance["pane"]]} --> I{i}')
                    lines.append(f'    I{i} -.->|"type"| {ids[instance["type"]]}')
                text = api.replace_block(text, block["name"], "\n".join(lines + ["``` "]))
                continue
            elif kind == "connections":
                ids = {e: "N" + str(i) for i, e in enumerate(entities)}
                lines = ["```mermaid", "flowchart LR"]
                used = set()
                for e in entities.values():
                    if e["kind"] != "connection":
                        continue
                    for endpoint in e.get("from", []) + e.get("to", []):
                        base = endpoint.split(".")[0]
                        if base not in used:
                            lines.append(f'    {ids[base]}["{api.esc(base)}"]')
                            used.add(base)
                for e in entities.values():
                    if e["kind"] != "connection":
                        continue
                    for a in e.get("from", []):
                        for b in e.get("to", []):
                            lines.append(f'    {ids[a.split(".")[0]]} -->|"{api.esc(e["id"])}"| {ids[b.split(".")[0]]}')
                text = api.replace_block(text, block["name"], "\n".join(lines + ["``` "]))
                continue
            else:
                raise uf.FlowError("Unknown render block kind: " + kind)
            text = api.replace_block(text, block["name"], api.table(block["headers"], rows))
        result[path] = text
    for flow in catalog["flows"]:
        result[flow["spec"]] = uf.render_flow(files[flow["spec"]].decode(), flow)
    return result


def render_recipe_errors(files, ui, catalog):
    """Check ownership and table shape without requiring fresh generated bodies."""
    import specs as api
    import user_flows as uf
    errors, paths = [], set()
    entities = {e["id"]: e for e in ui["entities"]}
    widths = {"terminology": 3, "sources": 3, "assertions": 3, "coverage": 3, "instances": 4}

    def blocks_at(path, blocks):
        text = files.get(path, b"").decode()
        spans = []
        for start, end in blocks:
            if text.count(start) != 1 or text.count(end) != 1 or text.index(start) >= text.index(end):
                errors.append(path + ": missing, duplicate or reversed generated markers")
            else:
                spans.append((text.index(start), text.index(end) + len(end)))
        spans.sort()
        if any(a[1] > b[0] for a, b in zip(spans, spans[1:])):
            errors.append(path + ": overlapping generated blocks")

    blocks_at("docs/glossary/README.md", [api.marker("terms")])
    for doc in ui["documents"]:
        path = doc["path"]
        if not (uf.safe_path(path) and path.startswith("docs/ui/") and path.endswith(".md") and path in files and path not in paths):
            errors.append("Invalid/duplicate UI document recipe")
        paths.add(path)
        names, markers = set(), []
        for block in doc["blocks"]:
            name, kind = block["name"], block["kind"]
            if not isinstance(name, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", name) or name in names:
                errors.append("Invalid/duplicate document block name")
            names.add(name)
            markers.append(api.marker(name))
            if kind in ("containment", "connections"):
                if "headers" in block:
                    errors.append(name + ": diagram recipes cannot declare table headers")
                continue
            width = widths.get(kind)
            if kind == "entities":
                fields = block.get("fields")
                ids = block.get("ids")
                if not isinstance(fields, list) or any(not isinstance(f, str) or not f for f in fields):
                    errors.append(name + ": entity fields must be text names")
                    continue
                if not isinstance(ids, list) or any(not isinstance(eid, str) or eid not in entities for eid in ids):
                    errors.append(name + ": undefined entity in render recipe")
                    continue
                if any(field not in entities[eid] for eid in ids for field in fields):
                    errors.append(name + ": undefined field in render recipe")
                width = len(fields) + 1
            if width is None:
                errors.append("Unknown render block kind: " + str(kind))
                continue
            headers = block.get("headers")
            if not isinstance(headers, list) or len(headers) != width or any(not isinstance(h, str) or not h.strip() for h in headers):
                errors.append(name + ": headers must be nonempty strings matching table row width " + str(width))
        if uf.safe_path(path) and path in files:
            blocks_at(path, markers)
    flow_paths = set()
    for flow in catalog["flows"]:
        fid, path = flow.get("id"), flow.get("spec")
        if not isinstance(fid, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", fid) or path != uf.DIRECTORY + fid + ".md" or path not in files or path in flow_paths:
            errors.append("Invalid/duplicate flow document recipe")
            continue
        flow_paths.add(path)
        blocks_at(path, [(uf.MARKER, uf.END)])
    return errors


def authored(files, path):
    """Exclude only blocks owned by this snapshot's registry at this path."""
    import specs as api
    import user_flows as uf
    data = files.get(path, b"")
    names = []
    if path == "docs/glossary/README.md" and api.TERMS in files:
        names.append("terms")
    if api.UI in files:
        names.extend(block["name"] for doc in uf.load(files, api.UI)["documents"] if doc["path"] == path for block in doc["blocks"])
    for name in names:
        start, end = (s.encode() for s in api.marker(name))
        if data.count(start) == data.count(end) == 1 and data.index(start) < data.index(end):
            prefix, rest = data.split(start)
            _, suffix = rest.split(end)
            data = prefix + suffix
    return data
