"""Core for local specification tooling."""
import json


class FlowError(Exception):
    pass


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def module_paths(files):
    """Declared Python implementation inputs of both specification runners."""
    return {path for path in files if path.startswith("scripts/specs_lib/") and path.endswith(".py")}
