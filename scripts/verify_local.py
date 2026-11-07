#!/usr/bin/env python3
"""Reconcile and verify once in one owned local invocation."""
import os
import sys

if __name__ == '__main__':
    # Bootstrap admission must precede selectable application imports. Keep this
    # independent of the library's validation for callers of its already-loaded API.
    for name in ('PYTHONPATH', 'PYTHONHOME', 'PYTHONUSERBASE'):
        selected = os.environ.get(name, '')
        values = [selected] if name == 'PYTHONUSERBASE' else selected.split(os.pathsep)
        if selected and any(not value or not os.path.isabs(value) for value in values):
            sys.stderr.write('Relative or empty ' + name + ' entries are unsupported by local verification\n')
            raise SystemExit(1)
    from specs_lib.local_verification import main
    raise SystemExit(main())
