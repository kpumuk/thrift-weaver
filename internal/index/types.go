// Package index provides workspace indexing for cross-file Thrift analysis.
package index

import (
	"errors"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// DocumentKey uniquely identifies a document within the workspace index.
type DocumentKey string

// SymbolID uniquely identifies a declaration symbol.
type SymbolID string

// ReferenceSiteID uniquely identifies a reference site.
type ReferenceSiteID string

// SymbolKind identifies a top-level declaration kind indexed by RFC 0003.
type SymbolKind string

// SymbolKind values cover the top-level declaration kinds indexed in RFC 0003.
const (
	SymbolKindTypedef   SymbolKind = "typedef"
	SymbolKindConst     SymbolKind = "const"
	SymbolKindEnum      SymbolKind = "enum"
	SymbolKindSenum     SymbolKind = "senum"
	SymbolKindStruct    SymbolKind = "struct"
	SymbolKindUnion     SymbolKind = "union"
	SymbolKindException SymbolKind = "exception"
	SymbolKindService   SymbolKind = "service"
)

// ReferenceKind identifies the semantic context of a reference site.
type ReferenceKind string

// ReferenceKind values describe the semantic binding context for a reference site.
const (
	ReferenceKindType           ReferenceKind = "type"
	ReferenceKindServiceExtends ReferenceKind = "service_extends"
	ReferenceKindThrowsType     ReferenceKind = "throws_type"
	ReferenceKindOpaque         ReferenceKind = "opaque"
)

// IncludeStatus describes how an include edge resolved.
type IncludeStatus string

// IncludeStatus values describe the include resolution outcome recorded in a snapshot.
const (
	IncludeStatusUnknown       IncludeStatus = "unknown"
	IncludeStatusResolved      IncludeStatus = "resolved"
	IncludeStatusMissing       IncludeStatus = "missing"
	IncludeStatusAliasConflict IncludeStatus = "alias_conflict"
)

// BindingStatus describes how a reference site resolved.
type BindingStatus string

// BindingStatus values describe the reference binding outcome recorded in a snapshot.
const (
	BindingStatusUnknown     BindingStatus = "unknown"
	BindingStatusBound       BindingStatus = "bound"
	BindingStatusUnresolved  BindingStatus = "unresolved"
	BindingStatusAmbiguous   BindingStatus = "ambiguous"
	BindingStatusTainted     BindingStatus = "tainted"
	BindingStatusUnsupported BindingStatus = "unsupported"
)

// Options configure a workspace manager.
type Options struct {
	WorkspaceRoots []string
	IncludeDirs    []string
	MaxFiles       int
	MaxFileBytes   int64
}

// DocumentInput is a parsed-document input used by the workspace manager.
type DocumentInput struct {
	URI        string
	Version    int32
	Generation uint64
	Source     []byte
}

// NamespaceDecl captures a namespace declaration.
type NamespaceDecl struct {
	Scope  string
	Target string
	Span   text.Span
}

// QualifiedName identifies a top-level declaration by defining document and name.
type QualifiedName struct {
	DeclaringURI string
	Name         string
}

// Symbol is an indexed top-level declaration.
type Symbol struct {
	ID       SymbolID
	Key      DocumentKey
	URI      string
	Kind     SymbolKind
	Name     string
	QName    QualifiedName
	NameSpan text.Span
	FullSpan text.Span
}

// IncludeEdge captures a single include declaration.
type IncludeEdge struct {
	RawPath     string
	Alias       string
	Span        text.Span
	ResolvedURI string
	ResolvedKey DocumentKey
	Status      IncludeStatus
}

// BindingResult captures how a reference site resolved.
type BindingResult struct {
	Status BindingStatus
	Target SymbolID
	Reason string
}

// ReferenceSite captures a semantic reference candidate.
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

// IndexDiagnostic is an index-layer diagnostic.
//
//nolint:revive // RFC 0003 names this shared type IndexDiagnostic.
type IndexDiagnostic struct {
	URI      string
	Code     string
	Message  string
	Severity syntax.Severity
	Span     text.Span
}

// DocumentSummary is the immutable extracted summary for one document.
type DocumentSummary struct {
	Key          DocumentKey
	URI          string
	Version      int32
	Generation   uint64
	ContentHash  [32]byte
	ParseTainted bool
	Includes     []IncludeEdge
	Namespaces   []NamespaceDecl
	Declarations []Symbol
	References   []ReferenceSite
	Diagnostics  []IndexDiagnostic
}

// IncludeGraph records resolved include edges and components.
type IncludeGraph struct {
	Forward    map[DocumentKey][]DocumentKey
	Reverse    map[DocumentKey][]DocumentKey
	Components [][]DocumentKey
}

// WorkspaceSnapshot is the published immutable workspace index view.
type WorkspaceSnapshot struct {
	Generation     uint64
	Documents      map[DocumentKey]*DocumentSummary
	SymbolsByID    map[SymbolID]Symbol
	SymbolsByQName map[QualifiedName][]SymbolID
	RefsByTarget   map[SymbolID][]ReferenceSiteID
	IncludeGraph   IncludeGraph
	ReverseDeps    map[DocumentKey][]DocumentKey
	SnapshotIssues []IndexDiagnostic
}

// DocumentView binds one document summary to its containing snapshot.
type DocumentView struct {
	Document *DocumentSummary
	Snapshot *WorkspaceSnapshot
}

// QueryDocument identifies the caller document for workspace queries.
type QueryDocument struct {
	URI        string
	Version    int32
	Generation uint64
}

// Location is an index-layer location result.
type Location struct {
	URI  string
	Span text.Span
}

// WorkspaceSymbol is a workspace symbol query result.
type WorkspaceSymbol struct {
	Name          string
	Kind          SymbolKind
	URI           string
	Span          text.Span
	ContainerName string
}

// QueryMeta reports the snapshot that answered a query.
type QueryMeta struct {
	WorkspaceGeneration uint64
	DocumentURI         string
	DocumentVersion     int32
	DocumentGeneration  uint64
}

// PrepareRenameResult describes a prepare-rename result or blockers.
type PrepareRenameResult struct {
	Placeholder string
	Span        text.Span
	Blockers    []IndexDiagnostic
}

// VersionedDocumentEdits are rename edits for a single document.
type VersionedDocumentEdits struct {
	URI         string
	Version     *int32
	ContentHash [32]byte
	Edits       []text.ByteEdit
}

// RenameResult is the result of a rename plan.
type RenameResult struct {
	Placeholder string
	Documents   []VersionedDocumentEdits
	Blockers    []IndexDiagnostic
}

var (
	// ErrContentModified reports that the caller document does not match a compatible snapshot.
	ErrContentModified = errors.New("content modified")
	// ErrRenameBlocked reports that rename is blocked by semantic constraints.
	ErrRenameBlocked = errors.New("rename blocked")
	// ErrWorkspaceClosed reports that no workspace snapshot is currently available.
	ErrWorkspaceClosed = errors.New("workspace snapshot unavailable")
)
