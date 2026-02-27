#!/usr/bin/env bash
set -euo pipefail

# sync-thrift-corpus.sh updates local corpus fixtures from an external Thrift checkout.
#
# Inputs:
# - first arg: path to a Thrift repository checkout (required)
# - optional env var DRY_RUN=1 to print actions without copying
#
# Outputs:
# - writes/updates files under testdata/corpus/{valid,invalid,editor}
# - prints a sync summary
#
# Note:
# - this is a conservative M0 scaffold script; it copies a small representative subset.

if [[ $# -lt 1 ]]; then
  echo "usage: $0 /path/to/thrift" >&2
  exit 1
fi

SRC_ROOT="$(cd -- "$1" && pwd)"
ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

VALID_DIR="$ROOT_DIR/testdata/corpus/valid"
INVALID_DIR="$ROOT_DIR/testdata/corpus/invalid"
EDITOR_DIR="$ROOT_DIR/testdata/corpus/editor"

mkdir -p "$VALID_DIR" "$INVALID_DIR" "$EDITOR_DIR"

copy_file() {
  local src="$1" dst="$2"
  if [[ "${DRY_RUN:-0}" == "1" ]]; then
    echo "dry-run: copy $src -> $dst"
    return 0
  fi
  cp "$src" "$dst"
}

find_first_thrift() {
  local dir="$1"
  find "$dir" -type f -name '*.thrift' | sort | head -n1
}

valid_sample="$(find_first_thrift "$SRC_ROOT")"
if [[ -n "$valid_sample" ]]; then
  copy_file "$valid_sample" "$VALID_DIR/000_external_valid.thrift"
fi

for f in "$SRC_ROOT"/test/audit/break*.thrift; do
  [[ -f "$f" ]] || continue
  base="$(basename "$f")"
  copy_file "$f" "$INVALID_DIR/$base"
done

if [[ -f "$SRC_ROOT/test/ThriftTest.thrift" ]]; then
  copy_file "$SRC_ROOT/test/ThriftTest.thrift" "$EDITOR_DIR/001_editor_sample.thrift"
fi

echo "sync-thrift-corpus: complete"

