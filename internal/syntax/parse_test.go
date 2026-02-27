package syntax

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestParseValidBuildsTreeAndQueries(t *testing.T) {
	t.Parallel()

	src := []byte(`
include "shared.thrift"
namespace go demo
const uuid GEN_UUID = '00000000-4444-CCCC-ffff-0123456789ab'

enum Color {
  RED = 1,
  BLUE = 2,
}

struct User {
  1: required string name,
  2: optional i64 id,
}

service UserService {
  oneway void ping(),
}
`)

	tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///demo.thrift", Version: 7})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if tree.URI != "file:///demo.thrift" || tree.Version != 7 {
		t.Fatalf("tree identity mismatch: uri=%q version=%d", tree.URI, tree.Version)
	}
	if tree.LineIndex == nil {
		t.Fatal("expected LineIndex to be populated")
	}
	if tree.Root == NoNode {
		t.Fatal("expected root node")
	}
	if len(tree.Nodes) <= 1 {
		t.Fatalf("expected CST nodes, got %d", len(tree.Nodes))
	}
	if len(tree.Tokens) == 0 || tree.Tokens[len(tree.Tokens)-1].Kind != lexer.TokenEOF {
		t.Fatal("expected EOF token")
	}
	if hasDiagnosticCode(tree.Diagnostics, DiagnosticParserErrorNode) || hasDiagnosticCode(tree.Diagnostics, DiagnosticParserMissingNode) {
		t.Fatalf("unexpected parser diagnostics: %+v", tree.Diagnostics)
	}
	if hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalAlignment) {
		t.Fatalf("unexpected internal alignment diagnostics: %+v", tree.Diagnostics)
	}

	top := tree.TopLevelDeclarationIDs()
	if len(top) < 5 {
		t.Fatalf("expected top-level declarations, got %d", len(top))
	}

	var sawEnum, sawStruct, sawService bool
	for _, id := range top {
		n := tree.NodeByID(id)
		if n == nil {
			continue
		}
		switch KindName(n.Kind) {
		case "enum_definition":
			sawEnum = true
			members := tree.MemberNodeIDs(id)
			if len(members) != 2 {
				t.Fatalf("enum members = %d, want 2", len(members))
			}
		case "struct_definition":
			sawStruct = true
			members := tree.MemberNodeIDs(id)
			if len(members) != 2 {
				t.Fatalf("struct members = %d, want 2", len(members))
			}
		case "service_definition":
			sawService = true
			members := tree.MemberNodeIDs(id)
			if len(members) != 1 {
				t.Fatalf("service members = %d, want 1", len(members))
			}
		}
	}
	if !sawEnum || !sawStruct || !sawService {
		t.Fatalf("missing expected top-level kinds (enum=%v struct=%v service=%v)", sawEnum, sawStruct, sawService)
	}
}

func TestParseInvalidReturnsRecoverableDiagnostics(t *testing.T) {
	t.Parallel()

	src := []byte("struct Broken {\n  1: string name = \"unterminated\n}\n")
	tree, err := Parse(context.Background(), src, ParseOptions{})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if tree == nil || tree.Root == NoNode {
		t.Fatal("expected tree on malformed input")
	}
	if len(tree.Diagnostics) == 0 {
		t.Fatal("expected diagnostics on malformed input")
	}
	if !hasDiagnosticSource(tree.Diagnostics, "lexer") {
		t.Fatalf("expected lexer diagnostics, got %+v", tree.Diagnostics)
	}
	if !hasDiagnosticSource(tree.Diagnostics, "parser") {
		t.Fatalf("expected parser diagnostics, got %+v", tree.Diagnostics)
	}
	for _, d := range tree.Diagnostics {
		if d.Source == "parser" && (d.Code == DiagnosticParserErrorNode || d.Code == DiagnosticParserMissingNode) && !d.Recoverable {
			t.Fatalf("parser syntax diagnostic should be recoverable: %+v", d)
		}
	}
}

func TestParseAlignmentInvariantsOnValidAndMalformed(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"valid": []byte("struct User { 1: string name, 2: i64 id, }\n"),
		"bad":   []byte("service S { void f(1: i32 x) throws (1: string msg\n"),
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tree, err := Parse(context.Background(), src, ParseOptions{})
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if err := assertTreeAlignment(tree); err != nil {
				t.Fatal(err)
			}
			if hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalAlignment) {
				t.Fatalf("unexpected internal alignment diagnostics: %+v", tree.Diagnostics)
			}
		})
	}
}

func TestParseIgnoresExtraCommentNodesForAlignment(t *testing.T) {
	t.Parallel()

	src := []byte(`struct Tenant {
  1: optional string id
    # Optional, as secret can only be read once on creation (or reset), and then hashed
  2: optional string secret
}
`)

	tree, err := Parse(context.Background(), src, ParseOptions{})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalAlignment) {
		t.Fatalf("unexpected internal alignment diagnostics with # comment: %+v", tree.Diagnostics)
	}
	if err := assertTreeAlignment(tree); err != nil {
		t.Fatal(err)
	}
}

func TestReparseEquivalentToParse(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"valid":     []byte("enum E { A = 1, B = 2, }\n"),
		"malformed": []byte("service Broken { void f(1: string name }\n"),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := ParseOptions{
				URI:     "file:///" + name + ".thrift",
				Version: 3,
			}

			first, err := Parse(context.Background(), src, opts)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			second, err := Reparse(context.Background(), first, src, opts)
			if err != nil {
				t.Fatalf("Reparse() error = %v", err)
			}

			if !reflect.DeepEqual(treeSnapshot(first), treeSnapshot(second)) {
				t.Fatalf("Parse/Reparse mismatch\nfirst=%#v\nsecond=%#v", treeSnapshot(first), treeSnapshot(second))
			}
		})
	}
}

func TestParseCorpusFixtures(t *testing.T) {
	t.Parallel()

	for _, setName := range []string{"valid", "invalid", "editor"} {
		t.Run(setName, func(t *testing.T) {
			t.Parallel()

			files, err := testutil.CorpusFiles(setName)
			if err != nil {
				t.Fatalf("CorpusFiles(%q): %v", setName, err)
			}
			for _, file := range files {
				t.Run(filepath.Base(file), func(t *testing.T) {
					assertParseFile(t, file)
				})
			}
		})
	}
}

func assertParseFile(t *testing.T, file string) {
	t.Helper()

	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", file, err)
	}
	tree, err := Parse(context.Background(), src, ParseOptions{
		URI:     "file://" + file,
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Parse(%q): %v", file, err)
	}
	if tree == nil || tree.Root == NoNode {
		t.Fatalf("Parse(%q): missing root", file)
	}
}

type snapshot struct {
	URI         string
	Version     int32
	Source      string
	Tokens      []tokenSnap
	Nodes       []nodeSnap
	Root        NodeID
	Diagnostics []diagSnap
}

type tokenSnap struct {
	Kind lexer.TokenKind
	Span string
}

type nodeSnap struct {
	ID         NodeID
	Kind       string
	Span       string
	FirstToken uint32
	LastToken  uint32
	Parent     NodeID
	Flags      NodeFlags
	Children   []ChildRef
}

type diagSnap struct {
	Code        DiagnosticCode
	Message     string
	Span        string
	Source      string
	Recoverable bool
}

func treeSnapshot(t *Tree) snapshot {
	ts := snapshot{
		URI:     t.URI,
		Version: t.Version,
		Source:  string(t.Source),
		Root:    t.Root,
	}
	for _, tok := range t.Tokens {
		ts.Tokens = append(ts.Tokens, tokenSnap{Kind: tok.Kind, Span: tok.Span.String()})
	}
	for _, n := range t.Nodes {
		ts.Nodes = append(ts.Nodes, nodeSnap{
			ID:         n.ID,
			Kind:       KindName(n.Kind),
			Span:       n.Span.String(),
			FirstToken: n.FirstToken,
			LastToken:  n.LastToken,
			Parent:     n.Parent,
			Flags:      n.Flags,
			Children:   append([]ChildRef(nil), n.Children...),
		})
	}
	for _, d := range t.Diagnostics {
		ts.Diagnostics = append(ts.Diagnostics, diagSnap{Code: d.Code, Message: d.Message, Span: d.Span.String(), Source: d.Source, Recoverable: d.Recoverable})
	}
	return ts
}

func hasDiagnosticCode(diags []Diagnostic, code DiagnosticCode) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func hasDiagnosticSource(diags []Diagnostic, source string) bool {
	for _, d := range diags {
		if d.Source == source {
			return true
		}
	}
	return false
}

func assertTreeAlignment(tree *Tree) error {
	if tree == nil {
		return errors.New("nil tree")
	}
	if len(tree.Tokens) == 0 {
		return errors.New("no tokens")
	}
	if len(tree.Nodes) == 0 {
		return errors.New("no nodes slice")
	}
	for i, tok := range tree.Tokens {
		if !tok.Span.IsValid() {
			return fmt.Errorf("invalid token span at %d: %s", i, tok.Span)
		}
		if i > 0 && tok.Span.Start < tree.Tokens[i-1].Span.Start {
			return fmt.Errorf("token start out of order at %d", i)
		}
	}
	for i, n := range tree.Nodes {
		if i == 0 {
			continue
		}
		if !n.Span.IsValid() {
			return fmt.Errorf("invalid node span for node %d: %s", n.ID, n.Span)
		}
		if int(n.FirstToken) >= len(tree.Tokens) || int(n.LastToken) >= len(tree.Tokens) {
			return fmt.Errorf("node %d token range out of bounds: %d..%d", n.ID, n.FirstToken, n.LastToken)
		}
		if n.LastToken < n.FirstToken {
			return fmt.Errorf("node %d invalid token range ordering: %d..%d", n.ID, n.FirstToken, n.LastToken)
		}
		for _, c := range n.Children {
			if c.IsToken {
				if int(c.Index) >= len(tree.Tokens) {
					return fmt.Errorf("node %d token child out of bounds: %d", n.ID, c.Index)
				}
				continue
			}
			if c.Index == 0 || int(c.Index) >= len(tree.Nodes) {
				return fmt.Errorf("node %d child node out of bounds: %d", n.ID, c.Index)
			}
			child := tree.Nodes[c.Index]
			if child.Parent != n.ID {
				return fmt.Errorf("child %d parent mismatch: got %d want %d", child.ID, child.Parent, n.ID)
			}
		}
	}
	return nil
}
