# RFC 0001: Thrift Tooling Platform (`thriftfmt`, `thriftls`, VS Code Extension)

- Status: Accepted
- Authors: Dmytro Shteflyuk
- Created: 2026-02-23
- Target release: Beta (date TBD)

## Summary

This RFC proposes a new standalone tooling project for Thrift IDL editing and formatting, consisting of:

- `thriftfmt`: a stable, lossless-aware formatter for `.thrift` files
- `thriftls`: an LSP server for editor integrations
- a VS Code extension with syntax highlighting and LSP integration

The project will be implemented primarily in Go and designed around a reusable syntax/formatting engine. Parsing will use a `tree-sitter` grammar (for incremental, error-tolerant parsing suitable for LSP) plus a custom lossless lexer/token-trivia layer (for formatter fidelity).

## Motivation

The current Apache Thrift C++ compiler frontend is optimized for semantic compilation and code generation, not source-preserving formatting:

- whitespace and regular comments are discarded early
- some syntax is normalized into semantic representations
- top-level declarations are stored in typed collections rather than source order

These are good compiler design choices but they are poor foundations for a modern formatter/LSP stack.

Building a dedicated tooling project allows:

- lossless parsing/trivia preservation for formatting
- error-tolerant incremental parsing for editors
- a cleaner Go-based developer experience
- independent release cadence from the Thrift compiler

## Goals

- Provide a deterministic, idempotent Thrift formatter (`thriftfmt`)
- Provide a production-quality LSP server (`thriftls`) for editors
- Provide a VS Code extension with syntax highlighting and LSP client integration
- Preserve comments and syntax fidelity where formatter policy permits
- Support invalid/incomplete code in editor workflows
- Validate formatted output compatibility against the official Thrift compiler in CI

## Non-Goals (Initial Scope)

- Replacing the official Thrift compiler
- Semantic type checking beyond syntax diagnostics in v1
- Cross-file indexing, go-to-definition, rename in v1
- A perfect source-preserving rewriter (formatter may normalize whitespace and selected style choices)
- Embedding formatter/LSP into the existing `thrift` binary

## High-Level Architecture

The platform is a shared engine with two frontends (CLI + LSP), plus a VS Code client.

```text
                    +----------------------+
                    |    VS Code Plugin    |
                    | TextMate + LSP client|
                    +----------+-----------+
                               |
                               | JSON-RPC (LSP)
                               v
                    +----------------------+
                    |       thriftls       |
                    |  LSP transport/API   |
                    +----------+-----------+
                               |
                    +----------v-----------+
                    |  Shared Go Engine    |
                    | lexer + tokens       |
                    | tree-sitter parser   |
                    | CST wrappers         |
                    | diagnostics          |
                    | formatter            |
                    +----------+-----------+
                               ^
                               |
                    +----------+-----------+
                    |       thriftfmt      |
                    |  CLI (check/write)   |
                    +----------------------+
```

## Core Technical Decisions

### 1. Language and Runtime

- Implementation language: Go
- Go `tree-sitter` binding/runtime: `github.com/tree-sitter/go-tree-sitter` (version pinned in M0)
- Rationale:
  - rapid iteration and testing
  - straightforward CLI/LSP packaging
  - strong ecosystem for tooling and CI

### 2. Parsing Strategy

- Use `tree-sitter` for syntax parsing and incremental updates
- Add a custom lossless lexer for trivia and exact token lexemes

Rationale:

- `tree-sitter` gives incremental/error-tolerant parsing and node spans
- custom lexer gives formatter-grade trivia preservation and lexeme fidelity
- hybrid approach reduces risk versus hand-rolling a fully incremental parser

Normative v1 decision:

- `tree-sitter` is the structural parser only; the custom lexer is the token/trivia source of truth for formatting.
- All formatter output decisions must be derived from:
  - CST structure (node kinds + spans)
  - lossless token/trivia spans
  - formatter policy
- No formatter logic may depend on `tree-sitter` tokenization internals.

### 3. Syntax Representation

- Internal primary representation for formatting/LSP: CST-oriented syntax tree + lossless token stream
- No semantic AST required for v1 formatter/LSP

### 4. Formatter Strategy

- Deterministic pretty-printer with doc-algebra style layout
- Preserve comments and token lexemes where policy allows
- Regenerate whitespace/indentation
- Support full-document and range formatting

### 5. LSP Strategy

- Snapshot-based document model keyed by URI+version
- Full reparse on change in v1 (designed to allow incremental optimization later)
- Error-tolerant parsing and partial diagnostics for malformed code

Normative v1 decisions:

- LSP text sync mode: `Incremental` (`textDocument/didChange` with ranged edits)
- Internal parse mode: full reparse from reconstructed document text after each accepted change
- Formatting on invalid syntax:
  - `textDocument/formatting` and `rangeFormatting` may return an LSP error (`RequestFailed`) when formatting is unsafe
  - diagnostics continue to be published asynchronously via `publishDiagnostics`

## Repository Layout (Monorepo)

Proposed repository root (new project, separate from Apache Thrift repo):

```text
thrift-weaver/
  README.md
  LICENSE
  go.mod
  go.sum
  .github/
    workflows/
      ci.yml
      release.yml
  docs/
    architecture.md
    formatting-style.md
    release.md
    rfcs/
      0001-thrift-tooling-platform.md
  cmd/
    thriftfmt/
      main.go
    thriftls/
      main.go
  internal/
    text/
      line_index.go
      positions.go
      edits.go
    lexer/
      token.go
      trivia.go
      lexer.go
      lexer_test.go
    syntax/
      kinds.go
      parse.go
      diagnostics.go
      cst.go
      query.go
      treesitter/
        parser.go
        language.go
        node.go
    format/
      doc.go
      printer.go
      comments.go
      policy.go
      format.go
      range_format.go
      format_test.go
    lsp/
      server.go
      handlers.go
      transport_stdio.go
      snapshots.go
      workspace.go
      capabilities.go
      diagnostics.go
      formatting.go
      symbols.go
      folding.go
      semantic_tokens.go
    testutil/
      corpus.go
      goldens.go
      thrift_oracle.go
  grammar/
    tree-sitter-thrift/
      grammar.js
      src/
      queries/
        highlights.scm
        folds.scm
        symbols.scm
  editors/
    vscode/
      package.json
      src/
        extension.ts
        client.ts
        config.ts
      syntaxes/
        thrift.tmLanguage.json
      language-configuration.json
      scripts/
        package-binaries.ts
      README.md
      CHANGELOG.md
  testdata/
    corpus/
      valid/
      invalid/
      editor/
    format/
      input/
      expected/
    lsp/
      scenarios/
  scripts/
    bootstrap.sh
    generate-tree-sitter.sh
    sync-thrift-corpus.sh
```

## Module Boundaries and Responsibilities

### `internal/text`

Purpose:

- Line index and offset math
- Byte offset <-> UTF-8 line/column
- Byte offset <-> LSP UTF-16 positions
- Text edit utilities and diff helpers

Constraints:

- This package is the only place that understands LSP UTF-16 conversions.
- Parser/formatter APIs should use byte offsets internally.

### `internal/lexer`

Purpose:

- Produce a lossless token stream with trivia and raw spans
- Provide stable token kinds independent of `tree-sitter` internals

Key responsibilities:

- Exact lexeme slicing from source
- Comment classification (`//`, `#`, `/* */`, `/** */`)
- Whitespace/newline trivia capture
- Robust handling of malformed strings/comments (emit error tokens + diagnostics)

v1 decision:

- Use a leading-trivia-only storage model unless a concrete formatter bug requires trailing trivia.
- `Trailing` fields in example APIs below are illustrative and may be omitted from implementation.

### `internal/syntax`

Purpose:

- Wrap `tree-sitter` parse tree with project-specific CST API
- Merge tree nodes with token stream
- Produce diagnostics and syntax queries for editor features

Key responsibilities:

- Parse source into `Tree` (CST root + token stream + diagnostics)
- Provide node iteration/query helpers
- Support parse recovery and error nodes
- Track stable spans for range formatting and editor features

Critical invariant:

- Every non-synthetic CST node span must map to a contiguous source byte range.
- `FirstToken` / `LastToken` must reference tokens whose spans are within the node span.
- Error/recovery nodes must still preserve source order in `Children`.

### `internal/format`

Purpose:

- Format source using CST + token/trivia model
- Return full-file output and precise edits

Key responsibilities:

- Doc-algebra builder/printer
- Comment placement and preservation rules
- Full and range formatting
- Idempotence guarantees

Critical invariant:

- Formatter must never emit text outside the input document's declared encoding assumptions (UTF-8 bytes in, UTF-8 bytes out).

### `internal/lsp`

Purpose:

- LSP server implementation over shared engine
- Snapshot lifecycle and request routing

Key responsibilities:

- document lifecycle (`didOpen`, `didChange`, `didClose`)
- diagnostics publishing
- formatting handlers
- symbols/folds/selection ranges
- semantic tokens (phase 2)
- cancellation and version consistency
- structured logging/trace hooks for debugging and support

v1 concurrency model:

- requests may be handled concurrently across different documents
- operations for the same document must resolve against a single immutable snapshot version
- stale formatting requests (older version than current snapshot) may return `ContentModified`

### `editors/vscode`

Purpose:

- VS Code client and packaging
- Syntax highlighting (TextMate in v1; semantic tokens later)
- Launch managed-install or user-provided `thriftls`

Key responsibilities:

- register language and grammar
- spawn server
- configure transport and settings
- surface logs/errors to users

## Data Structures (Go API-Level)

This section defines the core data model for the engine.

### Source and Positioning

```go
package text

type ByteOffset int

type Span struct {
    Start ByteOffset // inclusive
    End   ByteOffset // exclusive
}

type Point struct {
    Line   int // 0-based
    Column int // byte column
}

type Range struct {
    Start Point
    End   Point
}

// LSP-facing UTF-16 position/range, kept at edges only.
type UTF16Position struct {
    Line      int
    Character int
}

type UTF16Range struct {
    Start UTF16Position
    End   UTF16Position
}
```

### Token and Trivia Model

```go
package lexer

type TokenKind uint16
type TriviaKind uint8

const (
    TriviaWhitespace TriviaKind = iota
    TriviaNewline
    TriviaLineComment
    TriviaHashComment
    TriviaBlockComment
    TriviaDocComment
)

type Trivia struct {
    Kind TriviaKind
    Span text.Span
}

type Token struct {
    Kind    TokenKind
    Span    text.Span
    Leading []Trivia
    Flags    TokenFlags // e.g. malformed, synthesized, recovered
}

type TokenFlags uint8
```

Notes:

- Token text is recovered via `source[token.Span.Start:token.Span.End]`.
- Trivia also points into source via spans; no duplicated strings by default.
- A leading-trivia-only model is acceptable in v1 if comment placement remains stable.

### Syntax Tree (CST Wrapper)

```go
package syntax

type NodeKind uint16
type NodeID uint32

const NoNode NodeID = 0
// Real node IDs are 1-based. NodeID is not required to equal the slice index.

type ChildRef struct {
    IsToken bool
    Index   uint32 // token index or node index
}

type Node struct {
    ID         NodeID
    Kind       NodeKind
    Span       text.Span
    FirstToken uint32 // inclusive token index
    LastToken  uint32 // inclusive token index
    Parent     NodeID // NoNode for root
    Children   []ChildRef // original source order
    Flags      NodeFlags  // error/recovered/synthetic
}

type NodeFlags uint8

type Tree struct {
    URI         string
    Version     int32
    Source      []byte
    Tokens      []lexer.Token
    Nodes       []Node
    Root        NodeID
    Diagnostics []Diagnostic
    LineIndex   *text.LineIndex
}
```

Design notes:

- Tree is immutable after parse.
- Nodes are stored in slices for cache locality and stable indexing.
- Parent pointers enable quick ancestor widening for range formatting.
- `Children` preserve exact syntax order, even for malformed or recovered regions.

### Diagnostics

```go
package syntax

type Severity uint8

const (
    SeverityError Severity = iota + 1
    SeverityWarning
    SeverityInfo
)

type DiagnosticCode string

type Diagnostic struct {
    Code       DiagnosticCode
    Message    string
    Severity   Severity
    Span       text.Span
    Related    []RelatedDiagnostic
    Source     string // "lexer", "parser", "formatter"
    Recoverable bool
}

type RelatedDiagnostic struct {
    Message string
    Span    text.Span
}
```

### Formatter Result Types

```go
package format

type Options struct {
    LineWidth           int
    Indent              string // default: "  "
    MaxBlankLines       int
    PreserveCommentCols bool // v2, experimental
}

type Result struct {
    Output      []byte
    Changed     bool
    Diagnostics []syntax.Diagnostic
}

type RangeResult struct {
    Edits       []text.ByteEdit
    Diagnostics []syntax.Diagnostic
}
```

`text.ByteEdit` (referenced above) is defined as:

```go
package text

type ByteEdit struct {
    Span    Span
    NewText []byte
}
```

### LSP Snapshot Model

```go
package lsp

type Snapshot struct {
    URI       string
    Version   int32
    Tree      *syntax.Tree
    UpdatedAt time.Time
}

type DocumentStore interface {
    Get(uri string) (*Snapshot, bool)
    Put(snapshot *Snapshot)
    Delete(uri string)
}
```

## Engine APIs (Go, Internal-First)

The examples below define the intended engine/package contracts for implementation.
v1 does not commit to a public/stable Go library API; packages remain internal until post-beta.

### Parsing APIs

```go
package syntax

type ParseOptions struct {
    URI            string
    Version        int32
    IncludeQueries bool // parse tree-sitter query metadata if needed
}

func Parse(ctx context.Context, src []byte, opts ParseOptions) (*Tree, error)

// Future incremental API; v1 may parse from scratch.
func Reparse(ctx context.Context, old *Tree, src []byte, opts ParseOptions) (*Tree, error)
```

Behavior:

- Returns a `Tree` even if syntax errors exist (best-effort), unless parsing infrastructure fails catastrophically.
- Parser errors appear in `Tree.Diagnostics`.
- `error` is reserved for internal failures (cancellation, parser initialization, invariant violations).
- `Reparse` is an optimization API. It must remain behaviorally equivalent to `Parse` for the same input bytes and options.

### Formatting APIs

```go
package format

func Document(ctx context.Context, tree *syntax.Tree, opts Options) (Result, error)

func Range(ctx context.Context, tree *syntax.Tree, r text.Span, opts Options) (RangeResult, error)

// Convenience wrapper for CLI paths.
func Source(ctx context.Context, src []byte, uri string, opts Options) (Result, error)
```

Behavior:

- `Document` may refuse to format if parse errors exceed a safety threshold (configurable policy).
- `Range` widens to the nearest format-safe ancestor (declaration/block/list node).
- Both functions are deterministic and idempotent given the same tree/options.

Formatting refusal contract:

- Refusal due to unsafe syntax is not a process error.
- Engine API will return a typed error (e.g., `ErrUnsafeToFormat`) for unsafe formatting requests.
- LSP/CLI layers map `ErrUnsafeToFormat` to protocol/UX behavior (LSP `RequestFailed`, CLI exit code `2`) while continuing to surface diagnostics from parsing.

### LSP Server APIs (Internal)

```go
package lsp

type ServerOptions struct {
    Logf          func(string, ...any)
    FormatOptions format.Options
}

type Server struct {
    // internal state
}

func NewServer(opts ServerOptions) *Server
func (s *Server) RunStdio(ctx context.Context) error
```

Request handlers (internal signatures):

```go
func (s *Server) DidOpen(ctx context.Context, p DidOpenParams) error
func (s *Server) DidChange(ctx context.Context, p DidChangeParams) error
func (s *Server) DidClose(ctx context.Context, p DidCloseParams) error
func (s *Server) Formatting(ctx context.Context, p DocumentFormattingParams) ([]TextEdit, error)
func (s *Server) RangeFormatting(ctx context.Context, p DocumentRangeFormattingParams) ([]TextEdit, error)
func (s *Server) DocumentSymbol(ctx context.Context, p DocumentSymbolParams) ([]DocumentSymbol, error)
func (s *Server) FoldingRange(ctx context.Context, p FoldingRangeParams) ([]FoldingRange, error)
func (s *Server) SelectionRange(ctx context.Context, p SelectionRangeParams) ([]SelectionRange, error)
func (s *Server) SemanticTokensFull(ctx context.Context, p SemanticTokensParams) (*SemanticTokens, error) // phase 2
```

LSP protocol contract (normative v1):

- `initialize` advertises incremental sync, document/range formatting, document symbols, folding ranges, and selection ranges.
- `initialize` must not advertise unsupported methods behind placeholders.
- `shutdown` is graceful and idempotent; `exit` terminates process.
- `textDocument/formatting` and `textDocument/rangeFormatting`:
  - return `RequestFailed` when formatting is unsafe (`ErrUnsafeToFormat`)
  - return `ContentModified` when request version is stale relative to current snapshot
- Unknown methods return standard JSON-RPC method-not-found behavior.
- Server must remain responsive under cancellation and treat cancellation as non-fatal.

## Formatter Design

### Formatting Policy (v1)

The formatter will:

- normalize indentation
- normalize horizontal spacing
- normalize blank line counts
- preserve comments
- preserve declaration and member order
- preserve token lexemes where possible:
  - string quote style and escapes
  - hex/decimal literal spelling
  - deprecated spellings (`async`, `byte`) unless an explicit normalize option is added

The formatter will not (v1):

- reorder imports/includes/namespaces
- rewrite deprecated syntax
- enforce semantic style (e.g. field ids ordering)

### Default Style Profile (v1)

These defaults are normative for the first implementation and for golden tests unless changed by a future RFC:

- `LineWidth = 100`
- `Indent = "  "` (two spaces)
- `MaxBlankLines = 2`
- top-level declarations separated by one blank line
- members (fields/functions/enum values) formatted one per line
- preserve existing separator lexeme when syntactically equivalent (`,` vs `;`) in v1
- preserve literal spellings and comment text
- invalid-code formatting in LSP defaults to fail-closed (`RequestFailed`) unless formatting is provably safe

If a syntax construct cannot be formatted without choosing a canonical separator, choose semicolon for declarations and comma for list/map/annotation items, and document the exception in tests.

### Doc-Algebra Model

Internal printer primitives:

- `Text`
- `Line` (hard break)
- `SoftLine` (space or line)
- `Indent`
- `Group`
- `Concat`
- `IfBreak` (optional in v2)

This enables:

- stable wrapping at configurable width
- consistent nested formatting (types, annotations, const literals)
- reuse across full/range formatting

### Comment Handling

Comment fidelity is a formatter-critical requirement.

Policy:

- comments are lexed as trivia with spans
- formatter emits comments at token boundaries based on trivia ownership
- blank-line preservation is conservative (cap at `MaxBlankLines`)
- no comment text rewriting in v1

Edge cases to support:

- comments between type and identifier
- trailing comments on fields and enum values
- doc comments preceding declarations and members
- comments inside const maps/lists

### Source Text and Newline Policy (v1)

Normative rules:

- Input bytes are treated as UTF-8 for parsing/formatting.
- UTF-8 BOM at file start is preserved if present.
- Invalid UTF-8 bytes:
  - parser/lexer may emit diagnostics
  - formatter must refuse (`ErrUnsafeToFormat`) rather than rewrite bytes
- Newline style:
  - preserve dominant file newline style (`LF` or `CRLF`) for formatter-emitted line breaks
  - mixed newline input may be normalized to the dominant style and should emit a diagnostic (non-fatal if formatting is otherwise safe)
- Formatter must not introduce NUL bytes.

## Parsing and Tree-Sitter Integration

### Grammar Scope

The `tree-sitter` grammar must support:

- current Thrift syntax
- common deprecated syntax forms tolerated in practice (as parseable nodes/tokens)
- error recovery around top-level declarations and container/literal boundaries

### Query Files

`grammar/tree-sitter-thrift/queries/` will include:

- `highlights.scm` for syntax highlighting (future semantic overlay optional)
- `folds.scm` for folding ranges
- `symbols.scm` for declarations (services, structs, enums, typedefs, consts)

### cgo / Build Strategy

`tree-sitter` integration introduces C code.

Plan:

- vendor/generated parser C sources in repo
- build Go bindings with `cgo` using `github.com/tree-sitter/go-tree-sitter`
- produce statically linked or self-contained binaries where feasible
- test builds on macOS/Linux/Windows in CI before extension packaging work starts

Risk mitigation:

- lock `tree-sitter` runtime/parser versions
- add a dedicated parser build smoke test in CI

Windows ARM64 note:

- Building `windows/arm64` is straightforward for pure Go, but not automatically trivial here because `tree-sitter` integration uses `cgo` and requires a working C toolchain for cross/native builds.
- v1 plan: include `windows/arm64` as a target if CI/toolchain setup is proven in M0/M1; otherwise defer artifact publication while keeping code portable.

### Parser/Lexer Alignment Invariants (Must-Have)

Because parsing is hybrid (`tree-sitter` + custom lexer), alignment rules must be explicit:

- all CST node spans are in byte offsets over the same source buffer used by the lexer
- lexer token spans must form a monotonically increasing sequence ending at EOF
- formatter lookup from CST node -> covering token range must be deterministic
- any span mismatch between lexer and parser is a parser bug and should surface as an internal diagnostic/test failure

Implementation note:

- create a small conformance test suite that asserts CST node spans align with expected token boundaries for representative grammar forms (declarations, nested containers, comments, malformed inputs)

## LSP Feature Set and Phasing

### v1 (MVP)

- `initialize`
- `shutdown`, `exit`
- `textDocument/didOpen`
- `textDocument/didChange`
- `textDocument/didClose`
- `textDocument/publishDiagnostics`
- `textDocument/formatting`
- `textDocument/rangeFormatting`
- `textDocument/documentSymbol`
- `textDocument/foldingRange`
- `textDocument/selectionRange`
- `workspace/didChangeConfiguration` (configuration reload only; no complex workspace features)

### v2

- `textDocument/semanticTokens/full`
- `textDocument/onTypeFormatting` (optional)
- richer diagnostics and quick fixes (e.g., deprecated syntax hints)

### Deferred (post-v2)

- go-to-definition
- references
- rename
- code actions requiring cross-file indexing

## VS Code Extension Design

### v1 Responsibilities

- Register `thrift` language
- Provide TextMate syntax highlighting (`syntaxes/thrift.tmLanguage.json`)
- Start `thriftls` via `vscode-languageclient`
- Manage `thriftls` installation/version selection (gopls-style tool management) or use user-provided path
- Route formatting requests to LSP
- Expose settings:
  - `thrift.server.path`
  - `thrift.server.args`
  - `thrift.format.lineWidth`
  - `thrift.trace.server`

Non-goal in v1:

- Implementing language semantics in the extension. All parsing/formatting/diagnostics logic lives in `thriftls`.

### Binary Packaging Strategy

v1 decision (gopls-style):

- Do not bundle `thriftls` binaries inside the `.vsix` by default.
- Publish per-platform `thriftls` binaries as release artifacts.
- VS Code extension downloads/installs the matching `thriftls` binary on demand (or via explicit command), similar to `gopls` tool installation flows.
- Store managed binaries in extension-managed storage/cache.
- Allow override via user-specified external path (`thrift.server.path`).
- Optional in v1 if CI/toolchain is ready: Windows `arm64` artifact publication.

Managed install contract (normative v1):

- Extension downloads `thriftls` only from a trusted release manifest URL or user-configured override endpoint.
- Manifest must include:
  - manifest schema version
  - tool version
  - platform/arch tuple
  - download URL
  - SHA-256 checksum
  - file size (bytes)
- Default managed manifest/download endpoints must use HTTPS; non-HTTPS endpoints are allowed only via explicit user override for development or air-gapped mirrors.
- Extension verifies checksum before install and rejects mismatches.
- Install/update is atomic:
  - download to temp file
  - verify checksum
  - replace managed binary via atomic rename where supported
  - preserve last-known-good binary for rollback on failed update
- Archive extraction (if used) must reject path traversal entries and unexpected file layouts.
- Extension must clearly surface offline/download/verification errors and allow manual `thrift.server.path` fallback.
- Artifact signing/provenance verification (e.g., signatures/attestations) is recommended and may be added before beta if release automation is ready; v1 minimum requirement is checksum verification.

Tradeoffs:

- Managed install keeps `.vsix` small and aligns with established Go tooling UX
- Requires robust download/version/checksum handling in extension
- External path still provides enterprise/offline escape hatch

### Semantic Highlighting Strategy

- v1: TextMate only (reliable and simple)
- v2: semantic tokens via LSP (overlay/fallback)

## CLI Design (`thriftfmt`)

### Commands and Flags

Primary usage:

- `thriftfmt path/to/file.thrift`
- `thriftfmt --write path/to/file.thrift`
- `thriftfmt --check path/to/file.thrift`
- `thriftfmt --stdin --assume-filename foo.thrift`
- `thriftfmt --line-width 100`

Flags:

- `--write`, `-w`: write result in-place
- `--check`: non-zero exit if changes would be made
- `--stdin`: read source from stdin
- `--stdout`: explicit stdout (default if no `-w`)
- `--assume-filename`: URI/name for diagnostics and parser context
- `--line-width`: max width
- `--range start:end` (optional in v1 CLI; required by API, not required by CLI)
  - v1 syntax (if implemented): byte offsets, half-open `[start,end)`, zero-based (e.g. `--range 120:240`)
  - future line/column syntax, if added, must use a distinct flag to avoid ambiguity
- `--debug-tokens`
- `--debug-cst`

### Exit Codes

- `0`: success; no changes (or write success)
- `1`: formatting changes required in `--check`
- `2`: syntax errors prevented formatting
- `3`: internal error

Input/output conflict rules (normative):

- `--write` and `--stdin` may not be used together
- `--check` and `--write` may not be used together
- formatting multiple files in one invocation is deferred unless explicitly added later

## Error Handling and Recovery Policy

### CLI

- By default, refuse formatting if syntax tree is too broken to ensure safe output
- Emit syntax diagnostics to stderr
- Return exit code `2`

### LSP

- Always attempt parse and publish diagnostics
- Formatting handlers may:
  - return no edits when already formatted
  - return LSP `RequestFailed` / `ContentModified` when unsafe or stale
- Never crash on malformed input

### Safety Threshold for Formatting

Formatter may refuse when:

- unterminated block/string causes tokenization desync
- root tree is mostly recovery/error nodes
- selected range cannot be widened to a format-safe ancestor

Exact thresholds should be documented in `docs/formatting-style.md` and covered by tests.

Minimum v1 threshold policy (to avoid implementation ambiguity):

- full-document formatting is allowed if lexer reaches EOF and root parse tree exists, even with recoverable parse diagnostics, unless unterminated string/block comment prevents reliable tokenization
- range formatting requires a format-safe ancestor with fully bounded token coverage
- if refusal occurs, diagnostics must indicate the blocking region when possible

## Performance Targets (Beta)

Targets are for local editor interaction and CI formatting runs.

- Parse + diagnostics for typical files (<2k LOC): p95 <50 ms on reference hardware (warm)
- Full document format for typical files: p95 <100 ms on reference hardware (warm)
- `didChange` handling and diagnostic refresh: perceived responsive under normal typing (debounce allowed); target p95 <75 ms parse+diagnostics on typical files after debounce
- No unbounded memory growth across repeated open/change/close cycles in LSP session

These are non-binding v1 targets but required for beta sign-off.

Measurement rules (required for beta sign-off):

- Publish benchmark corpus definitions (at least: small, typical, large Thrift files; malformed-file set).
- Record hardware/OS baseline for reported numbers in CI or release notes.
- Report p50/p95 latency for parse and format benchmarks.
- Track steady-state RSS (or equivalent process memory metric) during repeated LSP open/change/close test loops.

## Testing Strategy

### 1. Unit Tests

- lexer tokenization and trivia capture
- UTF-16 position mapping
- parser node wrappers and queries
- formatter doc-printer behavior
- LSP handler utilities

### 2. Golden Tests

- `input.thrift` -> `expected.thrift`
- idempotence: `fmt(fmt(x)) == fmt(x)`
- comment preservation fixtures
- malformed syntax recovery fixtures
- range-format widening fixtures

### 3. Corpus Tests

- Parse large sets of real-world `.thrift` files
- Include compiler fixtures and custom edge-case corpus

### 4. Compatibility Oracle Tests

- Validate formatted output parses with official Thrift compiler (`thrift`)
- CI job should fail if formatter emits syntax not accepted by official compiler

Version pinning requirement:

- CI must pin the oracle compiler version (container image or released binary) to avoid silent behavior drift.
- A separate scheduled job may run against latest upstream for early-warning compatibility signals.

### 5. LSP Integration Tests

- `didOpen` diagnostics
- versioned `didChange` ordering
- formatting and range formatting responses
- cancellation handling
- UTF-16 edit correctness
- `initialize` capability advertisement matches implemented handlers
- formatting request failure semantics (`RequestFailed`, `ContentModified`) are covered by integration tests

### 6. VS Code Smoke Tests

- extension activation
- server launch
- diagnostics visible
- formatting command works
- syntax highlighting grammar loads
- managed `thriftls` install/update flow works against test manifest
- checksum verification failure is surfaced and blocks activation of managed binary

### 7. Fuzz / Robustness

- fuzz lexer and parser for panics/crashes
- fuzz formatting on arbitrary token streams/trees (best effort)

## CI / Release Plan

### CI Required Jobs

- `go test ./...`
- `golangci-lint` (or equivalent)
- parser generation drift check (`tree-sitter` generated files committed and up to date)
- corpus parse tests
- golden formatter tests
- compatibility oracle tests (with `thrift` compiler installed in job)
- VS Code extension build smoke test
- cross-platform binary build smoke (at least compile)
- release manifest/checksum generation and verification smoke (for managed `thriftls` install flow)

Recommended additions (required before beta):

- race detector run for LSP/document-store packages (`go test -race` on supported CI runners)
- VS Code extension integration smoke against a packaged `.vsix`

### Release Artifacts

- `thriftfmt` binaries (macOS/Linux/Windows)
- `thriftls` binaries (macOS/Linux/Windows)
- `thriftls` release manifest (machine-readable platform matrix + checksums)
- checksums file (SHA-256) for published binaries/artifacts
- VS Code extension package (`.vsix`)

### Versioning

- SemVer across CLI and LSP server
- VS Code extension version may track repo release; extension-managed `thriftls` versions are selected/pinned via manifest policy and extension compatibility rules

## Milestones and Acceptance Criteria

### M0: Foundation

Scope:

- repo scaffold
- CI skeleton
- test harness
- RFC + architecture docs

Acceptance criteria:

- repository structure exists and builds
- CI runs lint + unit test placeholders
- golden test harness can execute sample fixtures
- `tree-sitter` parser generation script stubbed and documented
- chosen Go `tree-sitter` binding and version are pinned in repo docs/build files

### M1: Parsing MVP

Scope:

- lossless lexer
- `tree-sitter` grammar v1
- CST wrapper
- syntax diagnostics

Acceptance criteria:

- parser returns `Tree` with tokens and nodes for valid fixtures
- parser returns recoverable diagnostics for invalid fixtures
- no panics on corpus parse test
- node spans map correctly to source bytes and LSP positions (tests)
- at least top-level declarations and members are represented in CST query APIs
- parser/lexer alignment invariants are enforced by dedicated tests

### M2: Formatter MVP

Scope:

- full-document formatter
- comment preservation
- CLI `thriftfmt`

Acceptance criteria:

- supports includes/namespaces/typedefs/enums/consts/structs/exceptions/services
- formatter is idempotent on formatter corpus
- comments are preserved in output (golden tests)
- `--check` exit codes behave as specified
- formatted output parses with official `thrift` compiler across corpus subset
- formatter refusal behavior (`ErrUnsafeToFormat` or equivalent) is finalized and tested

### M3: LSP MVP

Scope:

- diagnostics + formatting + range formatting
- document symbols/folding/selection ranges

Acceptance criteria:

- `thriftls` handles LSP open/change/close lifecycle without crashes
- diagnostics update on edits for valid and invalid files
- `textDocument/formatting` and `rangeFormatting` return valid edits
- range formatting widens to safe ancestors and is covered by tests
- document symbols and folding ranges are returned for core declarations
- formatting request failure semantics (`RequestFailed`, `ContentModified`) are covered by integration tests
- `initialize` advertises only implemented v1 capabilities

### M4: VS Code Extension MVP

Scope:

- syntax highlighting
- LSP client integration
- server binary management/install flow (gopls-style)

Acceptance criteria:

- opening `.thrift` file activates extension
- syntax highlighting works with TextMate grammar
- diagnostics and formatting work via extension-managed `thriftls` install (or configured external path)
- managed install validates manifest/checksum and preserves last-known-good binary on failed update
- offline/download/verification failures produce actionable user-facing errors and do not corrupt existing managed binary
- extension works on macOS/Linux/Windows in smoke tests
- user can override server path via settings

### M5: Hardening and Beta

Scope:

- performance tuning
- semantic tokens (optional)
- crash hardening and release automation

Acceptance criteria:

- beta performance targets met on representative corpora
- no known crashers from fuzz/corpus suites
- release pipeline produces signed/publishable artifacts (or documented unsigned process)
- user documentation covers install, format, and VS Code setup

## Decision Log and Remaining Questions

1. Project hosting and governance:
   - Resolved for v1: start in `github.com/kpumuk/thrift-weaver` and evaluate upstreaming later.
2. `tree-sitter` distribution policy:
   - Resolved for v1: `cgo`-based binaries are acceptable; no non-`cgo` fallback is required from day one.
   - Follow-up: confirm Windows `arm64` artifact support based on toolchain/CI readiness.
3. Formatter v1 style strictness:
   - Resolved for v1: preserve separator lexemes and deprecated spellings by default; canonicalize whitespace/indentation only.
4. Library API stability:
   - Resolved for v1: keep implementation packages internal until post-beta; no public/stable Go library API commitment in v1.
5. Invalid-code formatting policy in editors:
   - Resolved for v1: fail closed (no edits + explicit error) unless formatting is provably safe.

Remaining non-blocking question (can be decided in M3/M4):

6. Linux managed binary compatibility policy for VS Code extension:
   - Resolved direction: follow `gopls`-style managed install/distribution patterns rather than bundling.
   - Remaining detail (M3/M4): define Linux binary baseline(s) and fallback guidance (`glibc` floor and/or alternate artifacts).

No M0-blocking open questions remain.

## Immediate Implementation Decisions (M0, Resolved)

These are narrower than the open questions above and directly block scaffolding work:

1. Resolved: repository home/module path starts at `github.com/kpumuk/thrift-weaver`
2. Resolved: use `github.com/tree-sitter/go-tree-sitter`; pin exact versions in M0
3. Resolved: use RFC v1 default style profile and preserve separators/deprecated spellings
4. Resolved: LSP invalid-format behavior defaults to fail-closed
5. Resolved: Windows `arm64` artifact publication is best-effort in v1 and non-blocking for beta (depends on `cgo` toolchain/CI readiness)

## Alternatives Considered

### A. Extend Existing C++ Compiler Frontend

Rejected for this project scope because:

- frontend discards trivia and normalizes syntax too early for formatter needs
- significant refactor would be required
- editor/LSP incremental parsing remains unsolved

### B. Handwritten Go Parser (No `tree-sitter`)

Deferred (possible future alternative) because:

- simpler pure-Go distribution
- but higher risk/time for error recovery + incremental/LSP-friendly behavior

### C. Formatter Only (No LSP)

Rejected because editor integration is a primary requirement and affects parser architecture choices from day one.

## Rollout Plan

1. Implement engine and CLI first (`thriftfmt`) to stabilize formatting semantics.
2. Add `thriftls` on top of same engine.
3. Ship VS Code extension with gopls-style managed `thriftls` install (plus external-path fallback).
4. Iterate on editor features (semantic tokens, code actions, navigation).

## Appendix: Initial Implementation Order (Detailed)

1. `internal/text` (line index, UTF-16 conversions, byte edits)
2. `internal/lexer` (lossless tokens/trivia + tests)
3. `grammar/tree-sitter-thrift` skeleton + parser generation pipeline
4. `internal/syntax` parse wrapper + diagnostics + CST queries
5. `internal/format` doc printer + declaration formatting
6. `cmd/thriftfmt` + golden tests + compiler compatibility CI
7. `internal/lsp` core server + formatting/diagnostics handlers
8. `editors/vscode` extension with TextMate + managed-install `thriftls`
9. Hardening, performance, release automation
