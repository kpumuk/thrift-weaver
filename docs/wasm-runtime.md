# WASM Runtime and Troubleshooting

This document describes the implemented parser runtime behavior for `thriftfmt`, `thriftlint`, and `thriftls`.

Normative policy lives in:

- [RFC 0001](/Users/dmytro/work/github/thrift-weaver/docs/rfcs/0001-thrift-tooling-platform.md)
- [RFC 0002](/Users/dmytro/work/github/thrift-weaver/docs/rfcs/0002-rfc-0001-amendment-wasm-incremental-lint.md)

Artifact generation and drift checks live in:

- [WASM Grammar Build Pipeline](/Users/dmytro/work/github/thrift-weaver/docs/wasm-grammar-build.md)

## Runtime Model

- The supported parser runtime is a single in-process wasm backend.
- `thrift.wasm` and `thrift.wasm.sha256` are embedded into the binaries with `go:embed`.
- Runtime execution uses `wazero`; the server does not shell out to `tree-sitter` and does not load a grammar from the filesystem at runtime.
- There is no backend toggle in user-facing configuration.

## Startup Integrity Checks

Parser initialization validates two things before the runtime is used:

- artifact checksum matches the committed `thrift.wasm.sha256`
- required wasm exports are present

Relevant failure classes:

- `wasm checksum mismatch`
- `wasm abi mismatch`

At the syntax layer these surface as `INTERNAL_PARSE` diagnostics for the current document version instead of reusing stale parser output.

## LSP Parse and Lint Lifecycle

`thriftls` currently behaves as follows:

- `didOpen`: full parse, then full-file lint
- `didChange`: apply incremental edits when eligible, publish syntax diagnostics immediately, then run debounced full-file lint
- `didSave`: run full-file lint immediately
- `didClose`: clear diagnostics

Lint-on-change is currently:

- full-file only
- debounced by `150ms`
- version- and generation-gated before publish

Changed-range lint is:

- experimental
- non-normative
- not enabled in the shipped diagnostic path

## Incremental Reparse Safeguards

Incremental reparsing is attempted only when the incoming change batch stays within the current guardrails:

- at most `1024` ranged edits in a single `didChange`
- at most `256 KiB` of combined removed and inserted bytes across that batch

If either limit is exceeded, `thriftls` falls back to a full reparse for that version.

Incremental correctness checks currently include:

- applying `InputEdit` metadata to the old tree before parse reuse
- reparsing with the old tree
- extracting changed ranges
- validating changed-range structure
- periodic full-parse equivalence verification every `256` incremental reparses

If verification or changed-range extraction fails, the server disables reuse for that step and falls back to a full reparse so diagnostics remain correct.

## Breaker Semantics

The syntax layer has a process-wide backend breaker protecting the wasm parser runtime.

States:

- `closed`
- `open`
- `half_open`

Current thresholds:

- open after `5` hard backend failures within `60s`
- first probe delay: `30s`
- probe delay doubles up to `10m`
- close after `3` successful probe attempts

Hard failures mean parser/backend failures other than:

- `context.Canceled`
- `context.DeadlineExceeded`

When the breaker is open:

- parser backend calls are short-circuited
- the current document version gets degraded parser diagnostics (`INTERNAL_PARSE`)
- stale async diagnostics are suppressed by `(uri, version, generation)` checks

## Current Limits and Non-Limits

Implemented limits:

- incremental edit-count limit: `1024`
- incremental edited-byte limit: `256 KiB`
- lint debounce: `150ms`

Current non-limits:

- there is no committed document-size cap in the shipped server
- there is no separate user-configurable parser timeout knob
- there are no user-configurable lint rule toggles yet

Cancellation/time bounds currently come from the calling request context.

## Failure Semantics by Surface

Editor lifecycle:

- fail-open
- the document snapshot still advances for the current version
- parser/internal diagnostics are published for that version
- older diagnostics are not reused as current results

Formatter:

- fail-closed
- unsafe parse state returns an error instead of edits

Linter:

- current lint publishing is tied to the current-version snapshot
- parser/internal diagnostics remain authoritative when the parse backend is degraded

## Troubleshooting

### `INTERNAL_PARSE` on open or change

Check:

- the current release/build includes the embedded grammar artifact
- the wasm artifact and checksum still match: `mise run grammars-drift`
- no recent grammar/runtime ABI drift was introduced

Relevant tests:

- `go test ./internal/syntax/...`
- `go test ./internal/lsp/...`

### `wasm checksum mismatch`

This means the embedded wasm bytes do not match `internal/grammars/thrift/thrift.wasm.sha256`.

Use:

```bash
mise run grammars-drift
```

If this fails in CI only, regenerate inside the pinned toolchain and compare the resulting `thrift.wasm` and checksum against the committed files.

### `wasm abi mismatch`

This means the runtime export/import surface expected by `internal/syntax/treesitter/parser.go` does not match the built wasm module.

Typical causes:

- grammar wasm regenerated with an incompatible wrapper/export surface
- runtime wrapper code changed without regenerating the grammar artifact

Check:

- `docs/wasm-grammar-build.md`
- `internal/syntax/treesitter/parser.go`
- `go test ./internal/syntax/treesitter`

### Diagnostics look stale after rapid edits

Expected behavior:

- syntax diagnostics may publish first on `didChange`
- lint diagnostics follow after the debounce window
- stale lint output from older generations is intentionally dropped

If you need to validate this behavior, run:

```bash
go test ./internal/lsp -run 'TestDidChangePublishesDebouncedLintDiagnostics|TestDidChangeSuppressesStaleDebouncedLintDiagnostics|TestDidChangeReplacesStaleLintWithCurrentVersionParserFailure'
```

## References

- [WASM Grammar Build Pipeline](/Users/dmytro/work/github/thrift-weaver/docs/wasm-grammar-build.md)
- [Incremental Parsing Proof](/Users/dmytro/work/github/thrift-weaver/docs/incremental-parsing-proof.md)
- [Performance Benchmarks](/Users/dmytro/work/github/thrift-weaver/docs/performance.md)
