#!/usr/bin/env python3
"""Execute one fixture verification command with checkout and process ownership."""
from pathlib import Path
import sys

from specs_lib.isolation import isolated
from specs_lib.processes import run_verification


def main():
    command = sys.argv[1:]
    if command == ["--validate-owner"]:
        import os
        root = Path(__file__).resolve().parents[1]
        if (os.environ.get("CODE_FACTORY_VERIFY_ROOT") != str(root)
                or not os.environ.get("CODE_FACTORY_VERIFY_RUN")):
            return 1
        with isolated(root):
            print("owned")
        return 0
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        raise ValueError("A verification command is required")
    root = Path(__file__).resolve().parents[1]
    with isolated(root):
        # A Make jobserver may use pipe descriptors. Preserve only those explicitly
        # declared by Make, never arbitrary owner or credential descriptors.
        from verification_timing import jobserver_fds
        descriptors = jobserver_fds(command)
        # Nested timing and browser runners finalize evidence and reap their
        # own groups after TERM. Keep this outer deadline beyond their grace.
        return run_verification(command, cwd=root, timeout=7200, pass_fds=descriptors,
                                termination_grace=10).returncode


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as error:
        sys.exit(str(error))
