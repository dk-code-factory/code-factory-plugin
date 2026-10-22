"""Json schema for local specification tooling."""
import json


def decode_json(data):
    """Decode JSON without silently choosing between duplicate object members."""
    def unique_members(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("Duplicate JSON member: " + key)
            result[key] = value
        return result
    return json.loads(data, object_pairs_hook=unique_members)


def load(files, path):
    import user_flows as uf
    try:
        return uf.decode_json(files[path])
    except (KeyError, ValueError, UnicodeDecodeError) as error:
        raise uf.FlowError("Missing or invalid JSON: " + path + " (" + str(error) + ")") from error


def require_object(value, context):
    import user_flows as uf
    if not isinstance(value, dict):
        raise uf.FlowError(context + " must be an object")


def array_field(obj, field, context, item_type=str, *, optional=False):
    """Check containers before consumers can promote keys/characters to entries."""
    import user_flows as uf
    if optional and field not in obj:
        return []
    values = obj.get(field)
    if not isinstance(values, list):
        raise uf.FlowError(context + ": " + field + " must be an array")
    if any(not isinstance(value, item_type) for value in values):
        kind = {str: "text", dict: "object", list: "array"}[item_type]
        raise uf.FlowError(context + ": " + field + " require " + kind + " entries")
    return values


def catalog_collection_shapes(catalog):
    """Shared structural preflight for legacy and layered catalog consumers."""
    import user_flows as uf
    uf.require_object(catalog, "Catalog")
    uf.array_field(catalog, "exclusions", "Catalog", dict, optional=True)
    suites = catalog.get("suites")
    uf.require_object(suites, "Catalog suites")
    for name, suite in suites.items():
        uf.require_object(suite, name + ": verification suite")
        for command in uf.array_field(suite, "commands", name, list):
            if any(not isinstance(arg, str) for arg in command):
                raise uf.FlowError(name + ": command must be an argv array of text")
        inputs = suite.get("inputs", [])
        if not isinstance(inputs, list) or any(not isinstance(path, str) for path in inputs):
            raise uf.FlowError(name + ": suite inputs must be an array of text")
    if isinstance(catalog.get("adoption"), dict):
        uf.array_field(catalog["adoption"], "flows", "Catalog adoption", optional=True)
    for flow in uf.array_field(catalog, "flows", "Catalog", dict):
        context = str(flow.get("id", "Flow"))
        for field in ("scenarios", "suites"):
            uf.array_field(flow, field, context)
        for field in ("terms", "ui"):
            uf.array_field(flow, field, context, optional=True)
        for field in ("implementation", "tests", "gaps"):
            refs = uf.array_field(flow, field, context, dict, optional=field == "gaps")
            if field == "tests":
                for ref in refs:
                    uf.array_field(ref, "scenarios", context + ": test reference")


def safe_path(path):
    return (isinstance(path, str) and path and not path.startswith("/")
            and "\\" not in path and all(p not in ("", ".", "..") for p in path.split("/")))


def is_test(path):
    return (path.endswith("_test.go") or path.startswith("web/tests/")
            or path.startswith("scripts/test_") or "/tests/" in path)
