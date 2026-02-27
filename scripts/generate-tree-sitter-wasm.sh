#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GRAMMAR_DIR="$ROOT_DIR/grammar/tree-sitter-thrift"
ARTIFACT_DIR="$ROOT_DIR/internal/grammars/thrift"
WASM_PATH="$ARTIFACT_DIR/thrift.wasm"
CHECKSUM_PATH="$ARTIFACT_DIR/thrift.wasm.sha256"

mkdir -p "$ARTIFACT_DIR"

cd "$GRAMMAR_DIR"

mise exec tree-sitter -- tree-sitter build --wasm --output "$WASM_PATH"

checksum="$(shasum -a 256 "$WASM_PATH" | awk '{print $1}')"
printf '%s\n' "$checksum" > "$CHECKSUM_PATH"

echo "generated wasm grammar: $WASM_PATH"
echo "updated checksum: $CHECKSUM_PATH"
