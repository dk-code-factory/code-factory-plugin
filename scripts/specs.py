#!/usr/bin/env python3
"""Deterministic glossary, UI and behavior contracts; no model or network required."""
import argparse
import hashlib
import html
import json
import pathlib
import posixpath
import re
import string
import sys
import tomllib
import unicodedata
from collections import defaultdict, deque
from urllib.parse import unquote, urlsplit
import user_flows as uf

if __name__ == "__main__":
    sys.modules.setdefault("specs", sys.modules[__name__])

TERMS = "docs/glossary/terms.json"
UI = "docs/ui/entities.json"
RECORDS = "docs/specs/reconciliations/"
BASELINE = "53ff8be70ddf852b6b13226582ccfaa9cef1f9dc"
ADOPTION_FLOWS = frozenset({"install-connect", "configure-work", "sync-inbox", "triage-inbox",
                          "resolve-priority", "relate-items", "launch-agent", "interact-agent",
                          "answer-attention", "recover-cleanup", "view-live-work",
                          "receive-notifications", "protect-secrets", "operate-installation"})
ROLES = {"glossary": "glossary-maintainer", "ui": "ui-specification-maintainer", "flow": "user-flow-maintainer"}
KINDS = {"behavior", "refactor", "terminology", "structure", "adoption"}
AGENTS = {"specification-coordinator", "glossary-maintainer", "ui-specification-maintainer", "user-flow-maintainer", "specification-reviewer"}
SKILLS = {"reconcile-specs", "maintain-glossary", "maintain-ui-specs", "reconcile-user-flows"}

from specs_lib.schema import graph_errors, registry_collection_shapes, validate
from specs_lib.references import decoded_reference, dependency_errors, reference_definition_context, definition_has_reference
from specs_lib.maintenance import skill_description_is_string, validate_agents
from specs_lib.rendering import esc, flow_diagram, marker, replace_block, table, generated, render_recipe_errors, authored
from specs_lib.impact import digest, ui_paths, model, suite_path_files, suite_inputs, contract_suites, current_verification, analyze
from specs_lib.receipts import constraints, receipt_errors


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("validate", "render", "impact", "check", "reconcile", "report", "gaps"))
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path(__file__).resolve().parents[1])
    parser.add_argument("--base", default="HEAD")
    parser.add_argument("--head")
    parser.add_argument("--merge-base", action="store_true")
    parser.add_argument("--staged", action="store_true")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--review", type=pathlib.Path)
    args = parser.parse_args(argv)
    try:
        root = args.root.resolve()
        if args.staged and (args.head or args.command in ("render", "reconcile")) or args.head and args.command in ("render", "reconcile"):
            raise uf.FlowError("render/reconcile require the working tree; --head and --staged are exclusive")
        selected_head = uf.revision(root, args.head) if args.head else None
        ref = uf.git(root, "write-tree").decode().strip() if args.staged else selected_head
        files = uf.snapshot(root, ref)
        if args.command == "render":
            validate(files, rendered=False)
            rendered = generated(files)
            candidate = dict(files)
            candidate.update({path: text.encode() for path, text in rendered.items()})
            validate(candidate)
            for path, text in rendered.items():
                if files.get(path) != text.encode():
                    (root / path).write_text(text)
            result = {"rendered": len(rendered)}
        elif args.command == "gaps":
            from specs_lib.gap_inventory import inventory, format_inventory
            _, ui, catalog = validate(files, rendered=False)
            result = inventory(ui, catalog)
            print(json.dumps(result, ensure_ascii=False, sort_keys=True) if args.json else format_inventory(result))
            return 0
        elif args.command == "validate":
            terms, ui, catalog = validate(files)
            result = {"valid": True, "terms": len(terms["terms"]), "entities": len(ui["entities"]), "flows": len(catalog["flows"])}
        else:
            base = uf.revision(root, args.base)
            if args.merge_base:
                base = uf.git(root, "merge-base", base, uf.revision(root, args.head or "HEAD")).decode().strip()
            before = uf.snapshot(root, base)
            report, affected = analyze(before, files, base)
            if args.command == "report":
                from specs_lib.reporting import build_report, format_report, report_json
                summary = build_report(before, files, report, affected, base, head=selected_head,
                                       staged=args.staged, merge_base=args.merge_base, root=root,
                                       review_path=args.review or "/tmp/spec-review.json")
                print(report_json(summary) if args.json else format_report(summary))
                return 0
            result = {"base": base, **report, "affected": affected, "errors": []}
            if args.command == "reconcile":
                if not args.review:
                    raise uf.FlowError("reconcile requires --review FILE")
                result = uf.reconcile(root, files, affected, base, args.review, records=RECORDS, analyzer=analyze, constraints=constraints, log_stream=sys.stderr if args.json else None)
            elif args.command == "check":
                result["errors"] = [err for subject, item in affected.items() for err in receipt_errors(files, subject, item, base)]
        if args.json:
            print(json.dumps(result, indent=2))
        elif result.get("errors"):
            print("\n".join(result["errors"]), file=sys.stderr)
        elif "affected" in result:
            print("Affected: " + (", ".join(result["affected"]) or "none"))
        else:
            print(json.dumps(result))
        return int(bool(result.get("errors")))
    except (uf.FlowError, KeyError, TypeError, ValueError, AttributeError, OSError) as error:
        if args.json:
            print(json.dumps({"errors": [str(error)]}))
        else:
            print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
