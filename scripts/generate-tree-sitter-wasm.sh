#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GRAMMAR_DIR="$ROOT_DIR/grammar/tree-sitter-thrift"
GRAMMAR_SRC_DIR="$GRAMMAR_DIR/src"
ARTIFACT_DIR="$ROOT_DIR/internal/grammars/thrift"
WASM_PATH="$ARTIFACT_DIR/thrift.wasm"
CHECKSUM_PATH="$ARTIFACT_DIR/thrift.wasm.sha256"
RUNTIME_WRAPPER="$ROOT_DIR/internal/grammars/thrift/runtime/thrift_runtime.c"
TREE_SITTER_CORE_DIR="$(mise exec go -- go list -m -f '{{.Dir}}' github.com/tree-sitter/go-tree-sitter)"
# We compile directly with the wasi-sdk clang that tree-sitter CLI bootstraps.
# `tree-sitter build --wasm` only produces a grammar module (`tree_sitter_thrift`)
# and cannot add our runtime wrapper exports (`tw_parser_*`, `tw_node_*`, ...),
# which are required for in-process wazero parsing.
WASI_CLANG="${WASI_CLANG:-$HOME/.cache/tree-sitter/wasi-sdk/bin/clang}"

mkdir -p "$ARTIFACT_DIR"

if [[ ! -x "$WASI_CLANG" ]]; then
  echo "bootstrapping wasi clang via tree-sitter CLI..." >&2
  BOOTSTRAP_WASM="$(mktemp "${TMPDIR:-/tmp}/tree-sitter-bootstrap-XXXXXX.wasm")"
  mise exec tree-sitter -- tree-sitter build --wasm --output "$BOOTSTRAP_WASM" >/dev/null
  rm -f "$BOOTSTRAP_WASM"
fi

if [[ ! -x "$WASI_CLANG" ]]; then
  echo "missing wasi clang after bootstrap: $WASI_CLANG" >&2
  exit 1
fi

# Build a single wasm module containing:
# 1) tree-sitter core runtime C sources,
# 2) thrift grammar parser.c,
# 3) our wrapper ABI used by internal/syntax/treesitter/parser.go.
"$WASI_CLANG" --target=wasm32-wasi -D__EMSCRIPTEN__ -mexec-model=reactor \
  -I "$TREE_SITTER_CORE_DIR/include" \
  -I "$TREE_SITTER_CORE_DIR/src" \
  -I "$GRAMMAR_SRC_DIR" \
  "$TREE_SITTER_CORE_DIR/src/lib.c" \
  "$GRAMMAR_SRC_DIR/parser.c" \
  "$RUNTIME_WRAPPER" \
  -o "$WASM_PATH" \
  -O2 \
  -Wl,--no-entry \
  -Wl,-z,stack-size=65536 \
  -Wl,--export=malloc \
  -Wl,--export=free \
  -Wl,--export=strlen \
  -Wl,--export=tree_sitter_thrift \
  -Wl,--export=tw_parser_new \
  -Wl,--export=tw_parser_delete \
  -Wl,--export=tw_parser_set_language \
  -Wl,--export=tw_parser_parse_string \
  -Wl,--export=tw_tree_delete \
  -Wl,--export=tw_tree_root_node \
  -Wl,--export=tw_node_child_count \
  -Wl,--export=tw_node_child \
  -Wl,--export=tw_node_named_child_count \
  -Wl,--export=tw_node_named_child \
  -Wl,--export=tw_node_type \
  -Wl,--export=tw_node_symbol \
  -Wl,--export=tw_node_start_byte \
  -Wl,--export=tw_node_end_byte \
  -Wl,--export=tw_node_is_error \
  -Wl,--export=tw_node_is_missing \
  -Wl,--export=tw_node_is_named \
  -Wl,--export=tw_node_is_extra \
  -Wl,--export=tw_node_has_error

checksum="$(shasum -a 256 "$WASM_PATH" | awk '{print $1}')"
printf '%s\n' "$checksum" > "$CHECKSUM_PATH"

echo "generated wasm grammar: $WASM_PATH"
echo "updated checksum: $CHECKSUM_PATH"
