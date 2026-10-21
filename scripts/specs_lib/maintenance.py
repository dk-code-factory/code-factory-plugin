"""Maintenance for local specification tooling."""
import json
import re
import tomllib


def skill_description_is_string(value):
    """Validate the required skills' single-line YAML string subset.

    Accept plain strings, YAML single-quoted strings and JSON double-quoted
    strings, with optional trailing comments. Tags, anchors, collections, block
    scalars and implicit non-string scalar spellings are outside this grammar.
    This deliberately does not claim to parse general YAML.
    """
    if any(c in "\r\x85\u2028\u2029" or not (c == "\t" or " " <= c <= "~" or
           "\u00a0" <= c <= "\ud7ff" or "\ue000" <= c <= "\ufffd" or ord(c) >= 0x10000)
           for c in value):
        return False
    def permitted_content(scalar):
        scalar = scalar.strip()
        return (bool(scalar) and len(scalar) <= 1024 and not scalar.startswith("[TODO:")
                and not any(c in "<>" or 0xD800 <= ord(c) <= 0xDFFF for c in scalar))
    value = value.strip(" ")
    if not value:
        return False
    if value.startswith('"'):
        try:
            scalar, end = json.JSONDecoder().raw_decode(value)
        except json.JSONDecodeError:
            return False
        if not re.fullmatch(r"(?: +#.*)?", value[end:]):
            return False
        return isinstance(scalar, str) and permitted_content(scalar)
    if value.startswith("'"):
        match = re.fullmatch(r"'((?:[^']|'')*)'(?: +#.*)?", value)
        return match is not None and permitted_content(match[1].replace("''", "'"))
    value = re.split(r" +#", value, maxsplit=1)[0].rstrip(" ")
    if "\t" in value or value[0] in "[],{}#&*!|>'\"%@`" or re.match(r"[-?:](?:\s|$)", value) or re.search(r":(?:\s|$)", value):
        return False
    nonstring = (r"(?:null|~|true|false|yes|no|on|off|y|n|<<|=|"
                 r"[-+]?(?:[0-9][0-9_:]*(?:\.[0-9_]*)?|\.[0-9_]+)(?:e[-+]?[0-9]+)?|"
                 r"[-+]?0(?:x[0-9a-f_]+|b[01_]+|o[0-7_]+)|[-+]?\.(?:inf|nan))")
    return permitted_content(value) and not (re.fullmatch(nonstring, value, re.I) or
                re.fullmatch(r"[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}(?:[Tt \t].*)?", value))


def validate_agents(files):
    import specs as api
    errors = []
    paths = {p for p in files if p.startswith(".codex/agents/") and p.endswith(".toml")}
    expected = {f".codex/agents/{name}.toml" for name in api.AGENTS}
    if expected - paths:
        errors.append("Declare the five required project specification agents")
    # Unrelated agent definitions have their own settings and ownership policy.
    for path in sorted(paths & expected):
        try:
            agent = tomllib.loads(files[path].decode())
            if set(agent) != {"name", "description", "developer_instructions"} or any(not isinstance(v, str) or not v.strip() for v in agent.values()) or agent["name"] not in api.AGENTS or path != f'.codex/agents/{agent["name"]}.toml':
                errors.append(path + ": standalone agent requires name, description and developer_instructions only; inherit settings")
                continue
            for ref in re.findall(r'`([^`]+)`', agent.get("developer_instructions", "")):
                if "/" in ref and not ref.startswith("python3 ") and ref not in files and not any(p.startswith(ref.rstrip("/") + "/") for p in files):
                    errors.append(path + ": unknown ownership/input path " + ref)
        except (tomllib.TOMLDecodeError, UnicodeDecodeError):
            errors.append(path + ": invalid TOML")
    for name in api.SKILLS:
        path = f".agents/skills/{name}/SKILL.md"
        text = files.get(path, b"").decode()
        metadata = re.match(r"---\nname: " + re.escape(name) + r"\ndescription: ([^\n]*)\n---\n", text)
        if metadata is None or not api.skill_description_is_string(metadata[1]):
            errors.append(path + ": invalid skill metadata")
        for section in ("Ownership", "Inputs", "Permitted edits", "Verification", "Handoff"):
            if "## " + section not in text:
                errors.append(path + ": missing " + section)
        if "scripts/specs.py" not in text:
            errors.append(path + ": unified command reference required")
        for ref in re.findall(r"scripts/[a-zA-Z0-9_./-]+\.(?:py|sh)", text):
            if ref not in files:
                errors.append(path + ": unknown command " + ref)
    return errors
