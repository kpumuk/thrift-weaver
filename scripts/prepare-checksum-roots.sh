#!/bin/sh
set -eu

out_dir=${1:-.checksum-roots}

rm -rf "$out_dir"
mkdir -p "$out_dir"
cp docs/release.md "$out_dir/UNSIGNED-BETA-RELEASE-POLICY.md"
cp .release-extra/*.vsix "$out_dir/"
