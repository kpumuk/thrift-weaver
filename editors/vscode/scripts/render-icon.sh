#!/usr/bin/env sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ext_dir="$(CDPATH= cd -- "$script_dir/.." && pwd)"
src="$ext_dir/media/icon.svg"
dst="$ext_dir/media/icon.png"

if ! command -v svg2png >/dev/null 2>&1; then
  echo "render-icon: svg2png is required. Install it and regenerate $dst from $src." >&2
  exit 1
fi

# Render directly to a marketplace-friendly 256x256 PNG.
svg2png -w 256 -h 256 "$src" "$dst" >/dev/null

echo "rendered $dst from $src"
