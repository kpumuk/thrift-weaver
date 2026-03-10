# RFC 0003: Workspace Indexing for Cross-File Diagnostics, Navigation, and Rename

- Status: Proposed
- Authors: Dmytro Shteflyuk, Codex
- Created: 2026-03-10
- Related:
  - `docs/rfcs/0001-thrift-tooling-platform.md`
  - `docs/rfcs/0002-rfc-0001-amendment-wasm-incremental-lint.md`

## Summary

This RFC adds a shared workspace indexing subsystem that enables:

- cross-file lint diagnostics over Thrift include graphs
- go-to-definition, find references, and rename for top-level declarations
- future workspace symbol search and code actions on top of the same index

The design preserves RFC 0001 and RFC 0002 invariants:

- `syntax.Tree` remains the immutable per-document source of truth
- LSP editing remains snapshot- and generation-based
- formatting remains independent from workspace indexing
- parser/runtime failure handling remains fail-open for editor state and fail-closed for unsafe mutations

This RFC does not turn `thrift-weaver` into a full compiler frontend. The index is semantic enough to resolve include-prefixed and local declaration references, but it does not attempt whole-program type evaluation, code generation semantics, or cross-language analysis.

## Motivation

RFC 0001 intentionally deferred workspace indexing and cross-file features so the project could land formatting, diagnostics, and baseline LSP support first. The current code reflects that decision:

- `internal/lsp.SnapshotStore` tracks one immutable tree per open URI
- `internal/lint` rules only inspect a single `*syntax.Tree`
- locally resolvable type and service checks explicitly stop at document boundaries

That is correct for v1, but it leaves three important gaps:

1. `include` graphs are not resolved, so diagnostics cannot verify qualified references such as `shared.User`.
2. refactoring features like definition, references, and rename have no shared symbol/reference model to build on.
3. CLI and LSP semantics will diverge if cross-file logic is later implemented only inside `thriftls`.

The index must therefore be a shared engine package, not an LSP-only cache.

## Goals

- Introduce a shared `internal/index` package that builds immutable workspace snapshots from document summaries.
- Resolve Thrift `include` graphs using deterministic, compiler-compatible path rules.
- Support cross-file binding for top-level declarations:
  - `typedef`
  - `const`
  - `enum`
  - `senum`
  - `struct`
  - `union`
  - `exception`
  - `service`
- Support cross-file binding for semantic reference sites derived from CST context, including:
  - type positions
  - service `extends`
  - `throws` clauses
  - other `scoped_identifier` sites recorded as typed reference candidates for future features
- Add workspace-aware lint execution without regressing current single-document diagnostics.
- Add LSP support for:
  - `textDocument/definition` (go-to-definition)
  - `textDocument/references`
  - `textDocument/prepareRename`
  - `textDocument/rename`
  - `workspace/symbol`
- Preserve responsive editing by making workspace analysis asynchronous and generation-gated.
- Allow open unsaved documents to override on-disk workspace state.

## Non-Goals

- Full compiler-equivalent semantic analysis or code generation validation.
- Cross-file formatting or declaration reordering.
- Member-level cross-file rename for struct fields, service methods, enum members, or annotation keys in this RFC.
- Automatic fetching of includes from remote registries or package managers.
- Indexing files outside configured workspace roots and include directories.
- Binding `cpp_include` declarations into the semantic graph.

## Current-State Constraints

The new design must respect these existing constraints from the codebase and prior RFCs:

- `syntax.Tree` is immutable and already carries source, CST nodes, diagnostics, line index, and snapshot version.
- `internal/lsp.SnapshotStore` already uses URI/version/generation gating to suppress stale asynchronous output.
- `internal/lint.Runner` is document-oriented and must remain usable without a workspace index.
- Parsing is hybrid and lossless, so the index should reuse CST spans instead of inventing a second parser or AST.
- RFC 0002 requires verified incremental reparsing and fail-open editor lifecycle behavior.

These constraints lead to one core rule:

> Workspace indexing consumes immutable document snapshots and published on-disk file states. It never becomes the source of truth for document text.

## High-Level Architecture

```text
               +--------------------+
               | open document text |
               |  (LSP snapshots)   |
               +---------+----------+
                         |
                         v
               +--------------------+
               | document summaries |
               | includes, decls,   |
               | refs, taint flags  |
               +---------+----------+
                         |
         +---------------+----------------+
         |                                |
         v                                v
 +---------------+               +-----------------+
 | file scanner  |               | open-doc shadow |
 | on-disk .thrift|              | unsaved wins    |
 +-------+-------+               +--------+--------+
         \                                /
          \                              /
           v                            v
            +--------------------------+
            | immutable workspace      |
            | snapshot / generation    |
            | include graph + symbols  |
            | bindings + reverse refs  |
            +------------+-------------+
                         |
          +--------------+---------------+
          |                              |
          v                              v
 +--------------------+         +--------------------+
 | workspace lint     |         | LSP query engine   |
 | cross-file diags   |         | def/ref/rename     |
 +--------------------+         +--------------------+
```

## Core Design

### 1. Shared Package Layout

Add a new package family:

```text
internal/index/
  types.go
  summary.go
  resolver.go
  graph.go
  manager.go
  query.go
  scanner.go
  diagnostics.go
```

Responsibilities:

- `summary.go`: extract per-document facts from `*syntax.Tree`
- `resolver.go`: resolve include targets and bind references
- `graph.go`: maintain include graph and reverse dependency graph
- `manager.go`: own mutable queues/caches and publish immutable workspace snapshots
- `query.go`: serve definition/reference/rename/workspace-symbol queries
- `diagnostics.go`: expose workspace-aware diagnostics views for lint/LSP
- `scanner.go`: discover on-disk `.thrift` files within configured roots

The package must depend on `internal/syntax`, `internal/text`, and standard library packages only. `internal/lsp` and `cmd/thriftlint` consume it; they do not own its semantics.

### 2. Canonical Identity and URI Rules

Every indexed document has:

- a canonical display URI used in diagnostics and outward-facing APIs
- an internal `DocumentKey` used for identity and deduplication

Identity is based on `DocumentKey`, not raw URI byte equality.

Canonicalization algorithm:

1. convert the candidate path to an absolute path
2. clean path segments (`.` / `..`)
3. resolve symlinks when the target exists
4. convert to a file URI with forward slashes and standard percent-encoding
5. on Windows:
   - normalize drive letters to uppercase
   - use `/` separators in the URI form
6. preserve the filesystem path casing returned by the canonicalized path for the display URI

Identity-key derivation:

- on case-sensitive filesystems, `DocumentKey` is the canonical display URI
- on case-insensitive filesystems, `DocumentKey` is derived from the canonical display URI with the path portion case-folded to lowercase for identity comparison only
- `DocumentSummary.URI` and user-facing diagnostics keep the display URI; they do not expose the folded key

Rules:

- `Documents map[DocumentKey]*DocumentSummary` keys are identity keys only
- `QualifiedName.DeclaringURI` is always canonical
- include resolution compares candidates by `DocumentKey`, not raw path string
- if canonicalization fails for an unresolved include target, the raw include stays diagnostic-only and never becomes a document identity
- if multiple URI spellings map to the same `DocumentKey`, they collapse to one document and later spellings produce a non-fatal duplicate-path-spelling diagnostic
- `thriftls`, `SnapshotStore`, and `index.Manager` must all use the same canonicalization helper before storing or querying document state

Shared helper contract:

```go
func CanonicalizeDocumentURI(raw string) (displayURI string, key DocumentKey, err error)
```

Normative uses:

- `DidOpen` / `DidChange` / `DidClose` canonicalize incoming LSP URIs before snapshot lookup or mutation
- `SnapshotStore` stores canonical display URIs and keys only
- `UpsertOpenDocument`, `CloseOpenDocument`, include resolution, and query entrypoints canonicalize URIs through the same helper
- `QueryDocument.URI` may be non-canonical on input, but it is canonicalized before freshness comparison

### 3. Document Summary Model

Each parsed document produces a compact summary:

```go
type DocumentInput struct {
    URI        string
    Version    int32  // -1 for on-disk files
    Generation uint64 // 0 for on-disk files
    Source     []byte
}

type DocumentSummary struct {
    URI              string
    Version          int32
    Generation       uint64
    ContentHash      [32]byte
    ParseTainted     bool
    Includes         []IncludeEdge
    Namespaces       []NamespaceDecl
    Declarations     []Symbol
    References       []ReferenceSite
    Diagnostics      []IndexDiagnostic
}
```

Core supporting types:

```go
type DocumentKey string
type SymbolID string
type ReferenceSiteID string

type Symbol struct {
    ID       SymbolID
    URI      string
    Kind     SymbolKind
    Name     string
    QName    QualifiedName
    NameSpan text.Span
    FullSpan text.Span
}

type QualifiedName struct {
    DeclaringURI string
    Name         string
}

type IncludeEdge struct {
    RawPath     string
    Alias       string
    Span        text.Span
    ResolvedURI string
    Status      IncludeStatus
}

type ReferenceSite struct {
    ID            ReferenceSiteID
    URI           string
    Context       ReferenceKind
    RawText       string
    Qualifier     string
    Name          string
    Span          text.Span
    ExpectedKinds []SymbolKind
    Tainted       bool
    Binding       BindingResult
}

type BindingResult struct {
    Status BindingStatus
    Target SymbolID
    Reason string
}

type IndexDiagnostic struct {
    URI      string
    Code     string
    Message  string
    Severity syntax.Severity
    Span     text.Span
}

type DocumentView struct {
    Document *DocumentSummary
    Snapshot *WorkspaceSnapshot
}

type PrepareRenameResult struct {
    Placeholder string
    Span        text.Span
    Blockers    []IndexDiagnostic
}
```

Summary extraction rules:

- extract declarations only from non-error top-level nodes with stable names
- record include declarations from `include_declaration` only
- ignore `cpp_include_declaration` for semantic binding
- record semantic reference candidates from `scoped_identifier` nodes plus their syntactic context
- initial bound contexts are:
  - field and typedef type positions
  - const type positions
  - function return types
  - service `extends`
  - `throws` clause parameter types
- other `scoped_identifier` sites may be recorded as opaque future reference candidates but are not bound in M7-M9
- mark a candidate as tainted when its binding context intersects parser recovery or missing nodes
- preserve source spans so diagnostics and edits map back to the existing tree/line index machinery

### 4. Symbol Scope and First-Phase Coverage

Phase 1 indexes top-level declarations only. This is deliberate.

Indexed symbol kinds:

- `typedef`
- `const`
- `enum`
- `senum`
- `struct`
- `union`
- `exception`
- `service`

Not indexed in phase 1:

- struct fields
- service methods
- enum members
- annotations
- namespace declarations as rename targets

Rationale:

- cross-file Thrift references overwhelmingly target top-level declarations
- top-level coverage is sufficient for the first meaningful navigation/refactor feature set
- member-level rename requires a richer semantic model and would expand surface area without unlocking the primary lint/navigation gaps

### 5. Include Resolution

Resolution must match Thrift include semantics closely enough that users do not see different answers from `thriftls`, `thriftlint`, and the upstream compiler.

For each `include "path/to/shared.thrift"`:

- the include alias is the included filename stem (`shared`)
- references of form `shared.TypeName` bind through that alias
- unqualified names never bind through an include edge

Resolution order:

1. Normalize the raw include path using forward-slash semantics.
2. Resolve relative to the including document's directory.
3. If not found, search configured include directories in declaration order.
4. If still not found, keep the edge as unresolved and emit a workspace diagnostic.

Path rules:

- all resolved paths are canonicalized to a file URI
- symlinks are resolved before identity comparison
- resolved files must remain under a configured workspace root or include directory
- duplicate include aliases within one document are diagnostics and block rename for affected references

Tie-breaking and deduplication rules:

- the first matching candidate in resolution order wins
- if a later candidate canonicalizes to the same URI as the winner, it is treated as the same target and does not create ambiguity
- if a later candidate resolves to a different canonical URI, it is ignored for binding because search order is authoritative
- if the same canonical target is included more than once through different raw paths, the duplicate include edges are preserved for diagnostics/span reporting but collapse to one target node in the include graph

Multi-root candidate expansion order:

- workspace roots preserve client/CLI declaration order
- include directories preserve declaration order
- for each include directory entry, candidate directories are expanded in this order:
  - if the entry is absolute, use it once
  - if the entry is relative, expand it once per workspace root in workspace-root order
- the final candidate list is:
  - including document directory first
  - then expanded include-directory candidates in the deterministic order above

Cycle handling:

- include cycles are represented as strongly connected components in the graph
- cycles are not fatal to navigation or lint by themselves
- invalidation operates on SCC boundaries so a change in one file rebinds the whole cycle deterministically

### 6. Binding Model

Binding operates on immutable workspace snapshots, not mutable queues.

For each reference candidate:

- if the reference is unqualified, bind only against declarations in the same document
- if the reference is `alias.Name`, first resolve `alias` through the document's include edges, then bind `Name` against declarations in that included document
- if the candidate's expected symbol kinds do not match the declaration kind, report a typed diagnostic instead of binding loosely
- if multiple targets remain possible, store an ambiguous binding result and surface it as a blocker for rename/code actions

The index stores both successful and failed bindings so consumers can produce precise diagnostics and user-facing error messages.

### 7. Immutable Workspace Snapshots

Published index state is immutable:

```go
type WorkspaceSnapshot struct {
    Generation       uint64
    Documents        map[DocumentKey]*DocumentSummary
    SymbolsByID      map[SymbolID]Symbol
    SymbolsByQName   map[QualifiedName][]SymbolID
    RefsByTarget     map[SymbolID][]ReferenceSiteID
    IncludeGraph     IncludeGraph
    ReverseDeps      map[string][]string
    SnapshotIssues   []IndexDiagnostic
}
```

Manager behavior:

- mutable manager state may queue scans, shadow open documents, and cache file metadata
- queries and diagnostics must run only against a published `WorkspaceSnapshot`
- publication is atomic: readers see either the old generation or the new one, never a half-built graph

### 8. Freshness and Invalidation

The manager tracks two state classes:

- open-document overrides from LSP snapshots
- on-disk documents from workspace scan

Rules:

- open documents always shadow on-disk content for the same URI
- closing a document removes the shadow and re-exposes on-disk state on the next scan/reload
- a changed document invalidates:
  - its own summary
  - its outbound bindings
  - reverse dependents reachable through include edges
- invalidation is graph-based, not whole-workspace by default
- the manager may schedule a debounced background rescan for filesystem drift

LSP queries must reject stale document versions with `ContentModified` when the caller's document version is older than the active open-document override.

Query freshness contract:

- every definition/reference/rename request captures exactly one published `WorkspaceSnapshot` before binding begins
- the request is valid only if that snapshot contains the caller URI with the exact requested version and query-document generation
- if no such snapshot exists, the request returns `ContentModified`
- once a request captures a compatible snapshot, it runs to completion against that snapshot even if newer generations publish concurrently
- referenced files reindexed after capture do not affect the in-flight request because results must never mix generations
- missing transitive documents inside the captured snapshot are treated as unresolved bindings, not staleness

Workspace root rules:

- LSP roots come from `initialize.workspaceFolders`
- if the client sends no workspace folders, `thriftls` uses the directory of the first opened document as an implicit root
- CLI roots come from `--workspace-root`; if omitted in `transitive` mode, the target file's directory becomes the implicit root
- include directories may be absolute or relative to a workspace root; relative include directories are resolved per root

Fallback consistency strategy:

- when workspace features are enabled, the manager performs a periodic metadata rescan every 30 seconds
- watcher overflow/error events trigger an immediate debounced full rescan
- opening a document or manual rescan requests refresh the containing directory before the next full interval
- periodic rescans compare path, size, and modification time before reparsing to cap steady-state cost

### 9. Failure and Partial-State Policy

Workspace indexing is best-effort for diagnostics and fail-closed for refactors.

Diagnostics behavior:

- unresolved includes, ambiguous aliases, and tainted reference sites produce diagnostics
- documents with parse recovery still contribute declarations and references that can be extracted safely
- parse-tainted sites do not silently bind

Refactor behavior:

- `prepareRename` and `rename` require an exact declaration binding
- rename must fail when any affected declaration or reference site is tainted or ambiguous
- rename must fail closed when `newName` does not lex as a standalone Thrift identifier, is a reserved keyword, contains qualification separators, or would make any rewritten binding ambiguous under the post-rename snapshot
- definition/references may return partial results only when the queried symbol binding itself is exact

This policy avoids incorrect edits while still giving useful navigation and diagnostics during incomplete editing.

## API Design

### Index Manager API

```go
package index

type Options struct {
    WorkspaceRoots []string
    IncludeDirs    []string
    MaxFiles       int   // default 10000
    MaxFileBytes   int64 // default 2 MiB per file
}

type Manager struct { /* internal state */ }

func NewManager(opts Options) *Manager
func (m *Manager) UpsertOpenDocument(ctx context.Context, in DocumentInput) error
func (m *Manager) CloseOpenDocument(ctx context.Context, uri string) error
func (m *Manager) RescanWorkspace(ctx context.Context) error
func (m *Manager) Snapshot() (*WorkspaceSnapshot, bool)
```

### Query API

```go
type QueryDocument struct {
    URI        string
    Version    int32
    Generation uint64
}

type Location struct {
    URI  string
    Span text.Span
}

type WorkspaceSymbol struct {
    Name          string
    Kind          SymbolKind
    URI           string
    Span          text.Span
    ContainerName string
}

type QueryMeta struct {
    WorkspaceGeneration uint64
    DocumentURI         string
    DocumentVersion     int32
    DocumentGeneration  uint64
}

var (
    ErrContentModified = errors.New("content modified")
    ErrRenameBlocked   = errors.New("rename blocked")
    ErrWorkspaceClosed = errors.New("workspace snapshot unavailable")
)

func (m *Manager) Definition(ctx context.Context, doc QueryDocument, pos text.UTF16Position) ([]Location, QueryMeta, error)
func (m *Manager) References(ctx context.Context, doc QueryDocument, pos text.UTF16Position, includeDecl bool) ([]Location, QueryMeta, error)
func (m *Manager) PrepareRename(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (*PrepareRenameResult, QueryMeta, error)
func (m *Manager) Rename(ctx context.Context, doc QueryDocument, pos text.UTF16Position, newName string) (*RenameResult, QueryMeta, error)
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) ([]WorkspaceSymbol, QueryMeta, error)
```

Caller contract:

- `thriftls` builds `QueryDocument` from the active `SnapshotStore` entry for the URI before calling the index manager
- `QueryDocument.Generation` comes from `lsp.Snapshot.Generation`; any implementation replacing `SnapshotStore` must expose an equivalent monotonically increasing per-document generation token to request handlers
- CLI callers use `Generation = 0` for on-disk documents
- generation is an internal freshness token, not a protocol field exposed to LSP clients
- `Definition`, `References`, and `WorkspaceSymbols` return empty results with `nil` error when no binding matches the queried position/text
- `ErrContentModified` is required when the caller document no longer matches any compatible snapshot
- `ErrWorkspaceClosed` is required when no workspace snapshot has been published yet
- `PrepareRename` and `Rename` use `ErrRenameBlocked` for semantic blockers and include machine-readable blocker diagnostics in the result payload

Prepare-rename contract:

- successful prepare-rename returns `PrepareRenameResult{Placeholder, Span, Blockers:nil}` with `nil` error
- blocked prepare-rename returns `PrepareRenameResult{Blockers:...}` with `ErrRenameBlocked`
- content drift during prepare-rename returns `nil, ErrContentModified`

Rename output:

```go
type VersionedDocumentEdits struct {
    URI         string
    Version     *int32
    ContentHash [32]byte
    Edits       []text.ByteEdit
}

type RenameResult struct {
    Placeholder string
    Documents   []VersionedDocumentEdits
    Blockers    []IndexDiagnostic
}
```

Rename edit contract:

- `Documents` is sorted by canonical URI for deterministic output
- `Edits` inside each document are strictly non-overlapping
- internal edit order is descending by `Span.Start` so edits can be applied safely against raw bytes
- the LSP adapter converts the same edits into protocol `TextEdit`s sorted in client-safe order
- `newName` validation happens before edit planning starts
- collision checks are evaluated against the post-rename binding model for all affected documents, not only the declaration file
- open documents carry `Version != nil` and must still match the captured snapshot version when the result is produced
- closed/on-disk documents carry `Version = nil`; they are validated by `ContentHash` immediately before the rename result is returned
- the LSP adapter serializes open documents as versioned `TextDocumentEdit`s and closed documents as versionless document changes
- any version/hash mismatch in any target document aborts the entire rename
- aborted renames return no edits, preserve accumulated blocker diagnostics, and surface `ErrContentModified` rather than partial workspace edits

### Lint Integration

Keep existing document rules unchanged and add workspace-aware rules:

```go
package lint

type WorkspaceRule interface {
    ID() string
    Description() string
    RunWorkspace(ctx context.Context, view *index.DocumentView) ([]syntax.Diagnostic, error)
}
```

`lint.Runner` gains a workspace-aware entry point without removing `Run(ctx, tree)`:

```go
func (r *Runner) RunWithWorkspace(ctx context.Context, view *index.DocumentView) ([]syntax.Diagnostic, error)
```

Initial workspace rules:

- unresolved include target
- unresolved qualified type/service reference
- include alias collision
- cross-file service extends / throws resolution

## LSP Protocol Changes

After this RFC, `thriftls` may advertise:

- `definitionProvider`
- `referencesProvider`
- `renameProvider`
- `workspaceSymbolProvider`
- `workspace/didChangeWatchedFiles`
- `workspace/workspaceFolders`

Protocol rules:

- syntax diagnostics remain immediate and document-scoped
- local lint remains debounced as today
- workspace diagnostics publish asynchronously when an index generation includes the current document generation
- diagnostics are merged from independent source buckets (`parser`, `local-lint`, `workspace-lint`) before publish, so a late workspace result cannot clear newer parser/local diagnostics
- navigation/refactor requests are served from the latest compatible workspace snapshot
- stale or blocked rename operations return `RequestFailed` with a concrete reason
- `workspace/didChangeWatchedFiles` events trigger targeted rescans for affected URIs and their reverse dependents

## CLI Changes

`thriftlint` remains usable as a single-file linter, but gains explicit workspace controls:

- `--workspace-root PATH` (repeatable)
- `--include-dir PATH` (repeatable)
- `--cross-file off|transitive|workspace`

Default behavior:

- path input defaults to `--cross-file transitive`
- `--stdin` defaults to `--cross-file off`
- `--stdin` may enable cross-file resolution only when `--assume-filename` resolves inside a configured workspace root or include directory

Meaning:

- `off`: current single-document behavior only
- `transitive`: index the target file plus transitive includes reachable from it
- `workspace`: scan all configured workspace roots and include directories

This keeps existing workflows fast while making cross-file analysis explicit in CLI contexts.

## Performance Requirements

Targets for a warm local session:

- summary extraction for a typical file (<2k LOC): p95 <10 ms
- single-document workspace update with <=25 impacted dependents: p95 <150 ms after debounce
- definition/references query on a warm snapshot: p95 <30 ms
- rename plan for <=200 edits: p95 <75 ms
- initial scan of 1,000 thrift files / 50 MB total source: p95 <5 s on reference hardware

The manager must avoid unbounded memory growth across repeated open/change/close cycles and repeated rescans.

## Security and Resource Constraints

- the indexer reads only configured workspace roots and include directories
- no network access is permitted for include resolution
- symlink escapes outside allowed roots are rejected
- `MaxFiles` and `MaxFileBytes` defaults are required to cap resource use
- file discovery ignores `.git`, `.hg`, `.svn`, `.idea`, and `.vscode`

## Observability

Expose structured logs and metrics hooks for:

- scan duration
- number of discovered files
- number of indexed documents
- rebuild reason (`open`, `change`, `close`, `watch`, `manual-rescan`)
- impacted document count
- workspace generation number
- query latency by method
- rename blocker counts by reason

These are required for debugging stale-index and unexpected-rename complaints.

## Testing Strategy

Add multi-file fixtures under `testdata/index/` covering:

- resolved includes
- missing includes
- duplicate include aliases
- reverse dependency invalidation
- open-document shadowing of on-disk files
- cyclic include graphs
- parse-tainted documents
- definition/references/rename goldens

Required tests:

1. Unit tests for summary extraction and include resolution.
2. Binding tests for local vs qualified references.
3. Manager tests for invalidation and atomic snapshot publication.
4. LSP integration tests for definition/references/rename freshness behavior.
5. CLI tests for `--cross-file` modes.
6. Race tests for concurrent open/change/query workloads.

## Rollout Plan

### M6: Index Foundation

- add `internal/index`
- add workspace scan and document summary extraction
- publish immutable workspace snapshots
- no user-visible navigation/refactor features yet

Acceptance:

- index snapshots build from on-disk files and open-document overrides
- unresolved includes and alias collisions are test-covered

### M7: Cross-File Diagnostics

- add workspace lint rules
- wire `thriftls` and `thriftlint` to merge workspace diagnostics with existing syntax/local lint diagnostics

Acceptance:

- qualified type/service diagnostics resolve across includes
- stale workspace diagnostics are not published over newer document generations

### M8: Go-To-Definition and Navigation

- implement definition, references, and workspace symbol queries

Acceptance:

- go-to-definition works for indexed top-level declarations
- references work for indexed top-level declarations
- open unsaved documents shadow on-disk answers

### M9: Safe Rename

- implement prepareRename and rename for indexed top-level declarations

Acceptance:

- rename produces deterministic workspace edits
- ambiguous or tainted bindings fail closed with explicit blockers

## Open Questions

1. Whether to expose include directories through VS Code settings before or together with M7.
   - Proposed answer: together with M7, because diagnostics correctness depends on them.
2. Whether constants and enum members should be promoted to first-class cross-file reference targets in the same milestone as rename.
   - Proposed answer: no; keep top-level declaration rename first, then extend with a follow-up RFC if real users need more surface area.
