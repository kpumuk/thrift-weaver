#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GRAMMAR_DIR="$ROOT_DIR/grammar/tree-sitter-thrift"

cd "$GRAMMAR_DIR"

mise exec tree-sitter -- tree-sitter generate
