"""Flow rendering for local specification tooling."""
from specs_lib.gap_inventory import render_gap
import re


def references(flow):
    import user_flows as uf
    lines = [uf.MARKER]
    if "graph" in flow:
        # Imported lazily: the legacy checker remains usable without registries.
        import specs
        lines.extend(["## Terminology and UI references", "",
                      "Glossary: " + ", ".join(f'[{t}](../glossary/README.md#{t})' for t in flow["terms"]) + ".",
                      "UI entities: " + (", ".join(f'[{u}](../ui/README.md#{u})' for u in flow["ui"]) or "none (operational contract)") + ".",
                      "", "## Flow diagram", "", specs.flow_diagram(flow["graph"]), ""])
    lines.extend(["## Implementation references", ""])
    for ref in flow["implementation"]:
        lines.append(f'- [{ref["path"]}](../../{ref["path"]}) — {ref["purpose"]}'
                     + (f' (`{ref["anchor"]}`)' if ref.get("anchor") else ""))
    lines.extend(["", "## Test references", ""])
    for ref in flow["tests"]:
        lines.append(f'- [{ref["path"]}](../../{ref["path"]}) — `{ref["anchor"]}`; '
                     + ", ".join(ref["scenarios"]) + ". " + ref["purpose"])
    lines.extend(["", "## Verification and gaps", "",
                  "Local suites: " + ", ".join(f'`{s}`' for s in flow["suites"]) + ".",
                  "Fixture success does not establish real-host acceptance."])
    for gap in flow["gaps"]:
        lines.append(f'- **{gap["scenario"]}** — {render_gap(gap)}')
    lines.extend(["", uf.END, ""])
    return "\n".join(lines)


def render_flow(text, flow):
    import user_flows as uf
    if text.count(uf.MARKER) != 1 or text.count(uf.END) != 1 or text.index(uf.MARKER) > text.index(uf.END):
        raise uf.FlowError("Flow requires one ordered reference block: " + flow["id"])
    prefix, rest = text.split(uf.MARKER)
    _, suffix = rest.split(uf.END)
    return prefix + uf.references(flow).rstrip() + suffix


def prose(files, flow):
    import user_flows as uf
    if not flow:
        return None
    text = files.get(flow["spec"], b"")
    return re.sub(re.escape(uf.MARKER.encode()) + b".*?" + re.escape(uf.END.encode()), b"", text, flags=re.S).strip()
