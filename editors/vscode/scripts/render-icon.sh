#!/usr/bin/env sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ext_dir="$(CDPATH= cd -- "$script_dir/.." && pwd)"
src="$ext_dir/media/icon.svg"
dst="$ext_dir/media/icon.png"

if ! command -v sips >/dev/null 2>&1; then
  echo "render-icon: sips is required (macOS). Install/choose another renderer and regenerate $dst from $src." >&2
  exit 1
fi

tmp="$ext_dir/media/.icon.tmp.png"
trap 'rm -f "$tmp"' EXIT INT TERM

# Render SVG to PNG and normalize to a marketplace-friendly 256x256 icon.
sips -s format png "$src" --out "$tmp" >/dev/null
sips -z 256 256 "$tmp" --out "$dst" >/dev/null

echo "rendered $dst from $src"
