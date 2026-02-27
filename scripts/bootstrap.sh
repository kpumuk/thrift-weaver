#!/usr/bin/env bash
set -euo pipefail

# bootstrap.sh initializes local developer prerequisites for thrift-weaver.
#
# Inputs:
# - optional env var CODEX_NO_HOOKS=1 to skip lefthook installation
#
# Outputs:
# - pinned tools installed via mise
# - git hooks installed (unless CODEX_NO_HOOKS=1)
# - tree-sitter parser generated

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

if command -v mise >/dev/null 2>&1; then
  mise trust >/dev/null 2>&1 || true
  mise install
else
  echo "bootstrap: mise is required but not found in PATH" >&2
  exit 1
fi

if [[ "${CODEX_NO_HOOKS:-0}" != "1" ]]; then
  mise exec lefthook -- lefthook install
fi

"$ROOT_DIR/scripts/generate-tree-sitter.sh"

echo "bootstrap: done"

