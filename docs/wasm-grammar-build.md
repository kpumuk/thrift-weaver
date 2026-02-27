# WASM Grammar Build Pipeline

This document defines the reproducible grammar artifact pipeline for wasm parser builds.

## Pinned Toolchain

Toolchain versions are pinned in [mise.toml](/Users/dmytro/work/github/thrift-weaver/mise.toml):

- `tree-sitter = 0.26.5`
- `golang = 1.26.0`
- `node = 22.14.0`

`tree-sitter build --wasm` downloads a pinned `wasi-sdk` toolchain on first run (currently `wasi-sdk-29` under `~/.cache/tree-sitter`).

## Generate Artifacts

Run:

```bash
mise run grammars
```

This executes:

1. `scripts/generate-tree-sitter.sh` (parser C sources)
2. `scripts/generate-tree-sitter-wasm.sh` (wasm artifact + checksum)

## Artifact Paths

- Wasm artifact: [thrift.wasm](/Users/dmytro/work/github/thrift-weaver/internal/grammars/thrift/thrift.wasm)
- Checksum: [thrift.wasm.sha256](/Users/dmytro/work/github/thrift-weaver/internal/grammars/thrift/thrift.wasm.sha256)

The wasm output filename is fixed (`thrift.wasm`) to keep byte output deterministic.

## Verify Drift and Checksums

Local drift verification:

```bash
mise run grammars-drift
```

This regenerates parser + wasm assets, verifies checksum integrity, and fails if committed artifacts drift.

## CI Gates

CI enforces both drift checks in `.github/workflows/ci.yml`:

- `parser-generation-drift` checks `grammar/tree-sitter-thrift/src`
- `wasm-grammar-drift` checks wasm artifact + checksum:
  - `internal/grammars/thrift/thrift.wasm`
  - `internal/grammars/thrift/thrift.wasm.sha256`
