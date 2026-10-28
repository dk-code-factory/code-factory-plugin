#!/bin/sh
set -eu
export CODE_FACTORY_REAL_CODEX=0
cd "$(dirname "$0")/.."
make CODE_FACTORY_REAL_CODEX=0 specs-check
make CODE_FACTORY_REAL_CODEX=0 build
make CODE_FACTORY_REAL_CODEX=0 check
make CODE_FACTORY_REAL_CODEX=0 test
make CODE_FACTORY_REAL_CODEX=0 test-performance
python3 scripts/package.py
python3 scripts/test_installer.py
python3 scripts/smoke.py
