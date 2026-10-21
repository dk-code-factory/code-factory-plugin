"""References for local specification tooling."""
import html
import posixpath
import re
import string
from urllib.parse import unquote, urlsplit


def decoded_reference(text):
    """Decode entity/URL spellings before comparing link destinations."""
    while True:
        decoded = html.unescape(unquote(text)).replace("\\/", "/")
        if decoded == text:
            return decoded
        text = decoded


def dependency_errors(files, catalog, previous_catalog=None):
    import specs as api
    import user_flows as uf
    errors = []
    fids = {f["id"] for c in (catalog, previous_catalog or {"flows": []}) for f in c["flows"]}
    for path, data in files.items():
        if not path.startswith(("docs/glossary/", "docs/ui/")) or not path.endswith((".json", ".md")):
            continue
        text = data.decode(errors="replace")
        if path.endswith(".json"):
            # Scan decoded strings, not JSON's extra backslashes around them.
            strings, pending = [], [uf.load(files, path)]
            while pending:
                value = pending.pop()
                if isinstance(value, str):
                    strings.append(value)
                elif isinstance(value, dict):
                    pending.extend(value.keys())
                    pending.extend(value.values())
                elif isinstance(value, list):
                    pending.extend(value)
            text = "\n".join(strings)
        # Markdown removes backslashes before ASCII punctuation in a link
        # destination. Do that before treating remaining path separators alike.
        text = re.sub(r"\\([" + re.escape(string.punctuation) + r"])", r"\1", api.decoded_reference(text))
        # Resolve raw, Markdown and HTML destinations identically. Scanning path
        # tokens also covers reference definitions without needing their labels.
        destinations = []
        for token in re.findall(r"[\w.~:/\\?#%+@-]+", text):
            token = token.replace("\\", "/").split("?", 1)[0].split("#", 1)[0]
            if "://" in token:
                token = urlsplit(token).path
            destinations.extend((posixpath.normpath(token).lstrip("/"),
                                 posixpath.normpath(posixpath.join(posixpath.dirname(path), token))))
        flow_path = next((p for p in destinations if p == "docs/user-flows" or p.startswith("docs/user-flows/")), None)
        ui_path = next((p for p in destinations if p == "docs/ui" or p.startswith("docs/ui/")), None)
        ids = "|".join(map(re.escape, sorted(fids)))
        compound = "|".join(re.escape(fid) for fid in sorted(fids) if "-" in fid)
        # Explicit flow references remain references after rename or deletion.
        pattern = r"user-flows[/\\#]|\bflow:[a-z][a-z0-9-]*\b"
        if ids:
            pattern += r"|(?:flow-|#)(?:" + ids + r")\b|\b(?:" + ids + r")\.md"
        if compound:
            pattern += r"|\b(?:" + compound + r")\b"
        flow_ref = re.search(pattern, text)
        if path.startswith("docs/glossary/") and re.search(r"(?im)^\s*#{1,6}\s+(?:used by|usage|consumers|backlinks)\b", text):
            errors.append(path + ": forbidden glossary usage/backlink section")
        ui_ref = re.search(r"(?<![A-Za-z0-9-])(?:W|P|PAGE|D|S|C)-[A-Z][A-Z0-9-]*(?![A-Za-z0-9-])", text)
        reference = flow_path or (flow_ref.group() if flow_ref else None)
        if path.startswith("docs/glossary/"):
            reference = reference or ui_path or (ui_ref.group() if ui_ref else None)
        if reference:
            errors.append(path + ": forbidden dependency reference " + reference)
    return errors


def reference_definition_context(glossary):
    """Remove quote/list containers while retaining relative code indentation."""
    import specs as api
    containers, lines = [], []
    quote = re.compile(r"^ {0,3}> ?")
    item = re.compile(r"^( {0,3})([-+*]|[0-9]{1,9}[.)])( +|$)")
    for line in api.decoded_reference(glossary).expandtabs(4).split("\n"):
        if not line.strip():
            lines.append("")
            continue
        remaining = line
        for index, (kind, padding) in enumerate(containers):
            if not remaining.strip():
                break
            match = quote.match(remaining) if kind == "quote" else None
            if match:
                remaining = remaining[match.end():]
            elif kind == "list" and remaining.startswith(" " * padding):
                remaining = remaining[padding:]
            else:
                containers = containers[:index]
                break
        while remaining.strip():
            match = quote.match(remaining)
            if match:
                containers.append(("quote", 0))
                remaining = remaining[match.end():]
                continue
            match = item.match(remaining)
            if not match:
                break
            # A list continuation removes its marker width and one to four
            # padding spaces. Extra padding retains code-block indentation.
            spaces = len(match[3])
            padding = len(match[1]) + len(match[2]) + (spaces if 1 <= spaces <= 4 else 1)
            containers.append(("list", padding))
            remaining = remaining[min(padding, len(remaining)):]
        lines.append(remaining)
    return "\n".join(lines)


def definition_has_reference(definition, glossary):
    import specs as api
    text = api.decoded_reference(definition)
    # Full/collapsed reference syntax is explicit. A shortcut is a link only
    # when its label resolves in the rendered glossary, so ordinary brackets
    # and punctuation remain available to definitions.
    label = lambda s: " ".join(s.casefold().split())
    label_chars = r"(?:\\.|[^\[\]\\])"
    # Reference labels also resolve from nested list continuation paragraphs.
    # Preserve escaped opening brackets as ordinary definition punctuation.
    context = api.reference_definition_context(glossary)
    labels = {label(s) for s in re.findall(r"(?m)^ {0,3}\[(" + label_chars + r"+)\]:\s*\S+", context)}
    if re.search(r"(?<!\\)\[" + label_chars + r"+\]\s*\[" + label_chars + r"*\]", text):
        return True
    if any(label(s) in labels for s in re.findall(r"(?<!\\)\[(" + label_chars + r"+)\]", text)):
        return True
    return bool(re.search(r"(?:[a-z][a-z0-9+.-]*://|\b(?:mailto|tel|data):|\bwww\.|[\w.+-]+@[\w.-]+\.[a-z]{2,}\b)|\[[^\]]*\]\s*\(|</?[a-z][^>]*>|<!--|<[^>]*[:@][^>]*>|`|\b[\w.-]+/\w|\b[\w-]+\.(?:py|go|tsx?|json|md)\b|\b(?:click|run the|execute|first,|then,)\b", text, re.I))
