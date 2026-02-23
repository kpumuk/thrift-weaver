# thrift-weaver

Thrift formatter, LSP server, and VS Code extension.

See `docs/rfcs/0001-thrift-tooling-platform.md` for the project RFC and implementation plan.

## Development Toolchain

- Go version is pinned via `mise` (see `mise.toml`).
- `golangci-lint` and `lefthook` are also pinned via `mise`.
- `github.com/tree-sitter/go-tree-sitter` is version-pinned in `go.mod` (see `tools/tools.go`).

Common commands:

```bash
mise install
mise run fmt
mise run lint
mise run test
mise run ci
```
