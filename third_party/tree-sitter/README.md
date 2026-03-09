# Vendored Tree-sitter Core

This directory vendors the tree-sitter C runtime sources used to build the embedded Thrift wasm parser.

Source snapshot:
- upstream packaging source: `github.com/tree-sitter/go-tree-sitter` `v0.25.0`
- files copied from that module:
  - `include/tree_sitter/*`
  - `src/*`

Why this exists:
- the shipped binaries are wasm-only and do not use the Go cgo binding at runtime
- the wasm build still needs the tree-sitter core C sources
- vendoring those sources removes the last build-time dependency on the Go cgo binding module

Update policy:
- when upgrading tree-sitter runtime/ABI, refresh this directory together with the grammar wasm artifact and drift checks
