#!/usr/bin/env python3
"""Validate flow contracts and reconcile their implementation and tests."""
import argparse
import hashlib
import io
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
import time
from fnmatch import fnmatchcase

if __name__ == "__main__":
    sys.modules.setdefault("user_flows", sys.modules[__name__])

CATALOG = "docs/user-flows/catalog.json"
DIRECTORY = "docs/user-flows/"
RECORDS = DIRECTORY + "reconciliations/"
MARKER = "<!-- flow-references -->"
END = "<!-- /flow-references -->"
FIXTURE_ENV = {"CODE_FACTORY_REAL_CODEX": "0"}
SECTIONS = ("Goal", "Preconditions", "Main flow", "Alternatives and recovery", "Scenarios")

from specs_lib.core import FlowError, canonical, module_paths
from specs_lib.git_snapshots import git, revision, Snapshot, content_id, snapshot
from specs_lib.json_schema import decode_json, load, require_object, array_field, catalog_collection_shapes, safe_path, is_test
from specs_lib.flow_rendering import references, render_flow, prose
from specs_lib.flow_schema import excluded, validate
from specs_lib.flow_impact import paths, policy, analyze
from specs_lib.flow_receipts import contract_errors, receipt_errors
from specs_lib.processes import run_verification, reconcile as _reconcile


def reconcile(root, files, affected, base, review_path, *, records=RECORDS, analyzer=analyze, constraints=contract_errors, log_stream=None):
    """Keep the legacy call signature and bound default callbacks."""
    return _reconcile(root, files, affected, base, review_path, records=records, analyzer=analyzer, constraints=constraints, log_stream=log_stream)


def unified_registry(files):
    # Select the implementation from the same snapshot as the command. Any
    # remaining foundation/metadata or structural flow field keeps incomplete
    # unified checkouts from silently falling back to the flow-only validator.
    if any(path in files for path in ("docs/glossary/terms.json", "docs/ui/entities.json",
                                      "docs/specs/README.md")) or any(
            path.startswith("docs/specs/reconciliations/") for path in files):
        return True
    catalog = load(files, CATALOG) if CATALOG in files else {}
    return isinstance(catalog, dict) and any(
        isinstance(flow, dict) and bool({"terms", "ui", "graph"} & flow.keys())
        for flow in catalog.get("flows", [])
    )

def main(argv=None):
    arguments = list(sys.argv[1:] if argv is None else argv)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("validate", "render", "impact", "check", "reconcile"))
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path(__file__).resolve().parents[1])
    parser.add_argument("--base", default="HEAD", help="Exact comparison base; use --merge-base for a branch/PR")
    parser.add_argument("--head", help="Check committed head instead of working tree (CI)")
    parser.add_argument("--merge-base", action="store_true", help="Compare from merge-base(base, head or HEAD)")
    parser.add_argument("--staged", action="store_true", help="Check the index, excluding unstaged changes")
    parser.add_argument("--review", type=pathlib.Path, help="Reviewed notes JSON for reconcile")
    parser.add_argument("--json", action="store_true", help="Machine-readable result (verification logs remain on stdout for reconcile)")
    args = parser.parse_args(arguments)
    try:
        root = args.root.resolve()
        if args.staged and (args.head or args.command in ("reconcile", "render")):
            raise FlowError("--staged cannot be combined with --head, render or reconcile")
        if args.command in ("reconcile", "render") and args.head:
            raise FlowError("Writing requires the working tree; omit --head")
        if args.staged:
            tree = git(root, "write-tree").decode().strip()
            files = snapshot(root, tree)
        elif args.head:
            files = snapshot(root, revision(root, args.head))
        else:
            files = snapshot(root)
        before = None
        if args.command in ("impact", "check", "reconcile"):
            base = revision(root, args.base)
            if args.merge_base:
                base = git(root, "merge-base", base, revision(root, args.head or "HEAD")).decode().strip()
            before = snapshot(root, base)
        # Comparison policy belongs to both selected snapshots. Removing every
        # current registry marker must not discard the baseline's foundational
        # retirement and mandatory tooling obligations.
        if unified_registry(files) or (before is not None and unified_registry(before)):
            import specs
            return specs.main(arguments)
        if args.command == "render":
            catalog = validate(files, rendered=False)
            output = {flow["spec"]: render_flow(files[flow["spec"]].decode(), flow) for flow in catalog["flows"]}
            candidate = dict(files)
            candidate.update({path: text.encode() for path, text in output.items()})
            validate(candidate)
            for path, text in output.items():
                if files.get(path) != text.encode():
                    (root / path).write_text(text)
            result = {"rendered": len(catalog["flows"])}
        elif args.command == "validate":
            catalog = validate(files)
            result = {"valid": True, "flows": len(catalog["flows"])}
        else:
            _, affected = analyze(before, files, base)
            if args.command == "reconcile":
                if args.review is None:
                    raise FlowError("reconcile requires --review FILE; inspect impact first")
                result = reconcile(root, files, affected, base, args.review)
            else:
                errors = [error for fid, item in affected.items() for error in (receipt_errors(files, fid, item, base) if args.command == "check" else contract_errors(fid, item))]
                result = {"base": base, "affected": affected, "errors": errors}
                if errors:
                    if args.json:
                        print(json.dumps(result, indent=2))
                    else:
                        print("\n".join(errors), file=sys.stderr)
                    return 1
        if args.json:
            print(json.dumps(result, indent=2))
        elif "affected" in result:
            print("Affected flows: " + (", ".join(result["affected"]) or "none") + "; check passed")
        else:
            print(json.dumps(result))
        return 0
    except (FlowError, KeyError, TypeError, ValueError, AttributeError) as error:
        if args.json:
            print(json.dumps({"errors": [str(error)]}))
        else:
            print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
