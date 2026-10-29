"""Authoritative unified specification CLI argument definitions."""
import argparse
from pathlib import Path


def argument_parser(root, **options):
    """Build the public parser with an explicit default repository root."""
    parser = argparse.ArgumentParser(**options)
    parser.add_argument("command", choices=("validate", "render", "impact", "check", "reconcile", "report", "gaps"))
    parser.add_argument("--root", type=Path, default=Path(root))
    parser.add_argument("--base", default="HEAD")
    parser.add_argument("--head")
    parser.add_argument("--merge-base", action="store_true")
    parser.add_argument("--staged", action="store_true")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--review", type=Path)
    return parser
