#!/bin/sh
set -eu
export CODE_FACTORY_REAL_CODEX=0
cd "$(dirname "$0")/.."
exec make CODE_FACTORY_REAL_CODEX=0 verify "$@"
