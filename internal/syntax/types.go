// Package syntax builds a CST-oriented parse result by combining the lossless lexer and tree-sitter parser.
package syntax

import (
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// NodeKind identifies a CST node kind (tree-sitter kind id).
type NodeKind uint16

// NodeID identifies a node in Tree.Nodes.
type NodeID uint32

const (
	// NoNode is the sentinel value for the absence of a node.
	NoNode NodeID = 0
)

// ChildRef references either a token or a node child.
type ChildRef struct {
	IsToken bool
	Index   uint32 // token index or node ID
}

// NodeFlags carry parser recovery/error metadata.
type NodeFlags uint8

const (
	// NodeFlagError marks a tree-sitter error node.
	NodeFlagError NodeFlags = 1 << iota
	// NodeFlagMissing marks a tree-sitter missing/recovered node.
	NodeFlagMissing
	// NodeFlagRecovered marks a node subtree that contains parser recovery.
	NodeFlagRecovered
	// NodeFlagNamed marks a named tree-sitter node.
	NodeFlagNamed
)

// Has reports whether all bits in mask are set.
func (f NodeFlags) Has(mask NodeFlags) bool {
	return f&mask == mask
}

// Node is a CST node in source order with token coverage.
type Node struct {
	ID         NodeID
	Kind       NodeKind
	Span       text.Span
	FirstToken uint32 // inclusive
	LastToken  uint32 // inclusive
	Parent     NodeID
	Children   []ChildRef
	Flags      NodeFlags
}

// Severity is a diagnostic severity level.
type Severity uint8

const (
	// SeverityError indicates an error diagnostic.
	SeverityError Severity = iota + 1
	// SeverityWarning indicates a warning diagnostic.
	SeverityWarning
	// SeverityInfo indicates an informational diagnostic.
	SeverityInfo
)

// DiagnosticCode identifies a syntax-layer diagnostic kind.
type DiagnosticCode string

const (
	// DiagnosticParserErrorNode reports a parser-generated error node.
	DiagnosticParserErrorNode DiagnosticCode = "PARSE_ERROR_NODE"
	// DiagnosticParserMissingNode reports a parser-generated missing node.
	DiagnosticParserMissingNode DiagnosticCode = "PARSE_MISSING_NODE"
	// DiagnosticInternalAlignment reports parser/lexer alignment invariant failures.
	DiagnosticInternalAlignment DiagnosticCode = "INTERNAL_ALIGNMENT"
	// DiagnosticInternalParse reports parser infrastructure issues surfaced in diagnostics.
	DiagnosticInternalParse DiagnosticCode = "INTERNAL_PARSE"
)

// RelatedDiagnostic adds context to a diagnostic.
type RelatedDiagnostic struct {
	Message string
	Span    text.Span
}

// Diagnostic is a unified syntax diagnostic.
type Diagnostic struct {
	Code        DiagnosticCode
	Message     string
	Severity    Severity
	Span        text.Span
	Related     []RelatedDiagnostic
	Source      string // lexer | parser | formatter
	Recoverable bool
}

// ParseOptions control syntax parsing behavior.
type ParseOptions struct {
	URI            string
	Version        int32
	IncludeQueries bool
}

// Tree is the immutable syntax parse result.
type Tree struct {
	URI           string
	Version       int32
	Source        []byte
	Tokens        []lexer.Token
	Nodes         []Node // index 0 is unused sentinel; real NodeIDs are 1-based
	Root          NodeID
	Diagnostics   []Diagnostic
	LineIndex     *text.LineIndex
	ChangedRanges []text.Span

	runtime *parseRuntimeState
}

// NodeByID returns the node for id or nil if not present.
func (t *Tree) NodeByID(id NodeID) *Node {
	if t == nil || id == NoNode {
		return nil
	}
	idx := int(id)
	if idx < 0 || idx >= len(t.Nodes) {
		return nil
	}
	return &t.Nodes[idx]
}

// RootNode returns the root node or nil.
func (t *Tree) RootNode() *Node {
	return t.NodeByID(t.Root)
}

// KindName resolves a NodeKind to its tree-sitter grammar kind name.
func KindName(kind NodeKind) string {
	return kindName(kind)
}

func (n Node) String() string {
	return fmt.Sprintf("Node{id=%d kind=%s span=%s tokens=%d..%d}", n.ID, KindName(n.Kind), n.Span, n.FirstToken, n.LastToken)
}
