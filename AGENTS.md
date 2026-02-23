# Thrift Weaver - Agents Context

## What

`thrift-weaver` is a new tooling project for Thrift IDL development:

- `thriftfmt` (formatter)
- `thriftls` (LSP server)
- VS Code extension (syntax highlighting + LSP client)

The implementation plan and architecture are defined in `docs/rfcs/0001-thrift-tooling-platform.md`.

## First Rule: Read The RFC Before Coding

Before starting any implementation task:

1. Read `docs/rfcs/0001-thrift-tooling-platform.md` end-to-end.
2. If implementation requires changing behavior/policy, update the RFC first in the same PR.

## Code Quality / Local Workflow (Use `mise`)

This repo uses `mise` to pin development tools and provide standard tasks.

First-time setup:

```bash
mise trust
mise install
mise exec lefthook -- lefthook install
```

Daily commands:

```bash
mise run ci   # formatter + linter + tests
mise run fmt  # format code
mise run lint # run golangci-lint
mise run test # run go test ./...
```

Notes:

- Prefer `mise run ...` / `mise exec ...` over globally installed tools.
- Pre-commit and pre-push hooks are managed with `lefthook` and are expected to run through `mise`.

## Development Rules (Project-Specific)

- Follow the RFC v1 decisions (hybrid `tree-sitter` + custom lossless lexer, fail-closed unsafe formatting, managed `thriftls` install in VS Code).
- Keep Go packages internal-first (`internal/*`) until post-beta.
- Add tests with functional changes whenever practical.
- If the RFC is underspecified for your task, stop and patch the RFC before continuing.

## Repository Layout (Early-Stage)

- `docs/rfcs/` - accepted/design RFCs (source of truth for architecture/policy)
- `cmd/thriftfmt/` - formatter CLI entry point
- `cmd/thriftls/` - LSP server entry point
- `internal/` - core engine packages (text, lexer, syntax, format, lsp)
- `grammar/tree-sitter-thrift/` - `tree-sitter` grammar source + generated parser assets
- `editors/vscode/` - VS Code extension
- `testdata/` - formatter/LSP/corpus fixtures
