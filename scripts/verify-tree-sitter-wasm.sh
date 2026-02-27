#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
WASM_PATH="$ROOT_DIR/internal/grammars/thrift/thrift.wasm"
CHECKSUM_PATH="$ROOT_DIR/internal/grammars/thrift/thrift.wasm.sha256"

if [[ ! -f "$WASM_PATH" ]]; then
  echo "missing wasm artifact: $WASM_PATH" >&2
  exit 1
fi

if [[ ! -f "$CHECKSUM_PATH" ]]; then
  echo "missing checksum file: $CHECKSUM_PATH" >&2
  exit 1
fi

expected_checksum="$(tr -d '[:space:]' < "$CHECKSUM_PATH")"
actual_checksum="$(shasum -a 256 "$WASM_PATH" | awk '{print $1}')"

if [[ "$actual_checksum" != "$expected_checksum" ]]; then
  echo "wasm checksum mismatch" >&2
  echo "expected: $expected_checksum" >&2
  echo "actual:   $actual_checksum" >&2
  exit 1
fi

echo "wasm checksum OK: $actual_checksum"
