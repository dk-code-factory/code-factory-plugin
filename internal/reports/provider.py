#!/usr/bin/env python3
"""Restore the retained skill lifecycle at the native provider boundary."""
import json
import os
from pathlib import Path
import sys

workspace = Path(__file__).resolve().parent
config = json.loads((workspace / 'provider-config.json').read_text())
receipt = json.loads((workspace / 'active-lifecycle.json').read_text())
if not receipt.get('token') or not receipt.get('runtime_root'):
    raise SystemExit('Retained lifecycle receipt unavailable')
os.environ['DK_HOST'] = 'codex'
os.environ['DK_UPDATE_PARENT'] = receipt['token']
os.environ['DK_UPDATE_MANAGED_RECEIPT'] = json.dumps(receipt)
for name in ('OPENAI_API_KEY', 'CODEX_API_KEY', 'OPENAI_BASE_URL'):
    os.environ.pop(name, None)
os.execvpe(config['codex'], [config['codex'], *sys.argv[1:]], os.environ)
