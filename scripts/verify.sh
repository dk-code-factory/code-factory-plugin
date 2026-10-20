#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
make build
make check
make test
python3 scripts/package.py
python3 scripts/test_installer.py
python3 scripts/smoke.py
