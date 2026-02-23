package treesitterthrift

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestLanguageParsesRepresentativeTopLevelAndMembers(t *testing.T) {
	src := []byte(`
include "shared.thrift"
cpp_include "legacy.h"
namespace go demo.example

typedef list<i64> cpp_type "std::vector<int64_t>" IDs
const map<string, i32> DEFAULTS = {"a": 1, "b": 2}
const uuid GEN_UUID = '00000000-4444-CCCC-ffff-0123456789ab'
const uuid GEN_GUID = '{00112233-4455-6677-8899-aaBBccDDeeFF}'

enum Color {
  RED = 1,
  BLUE = 2,
}

struct User {
  1: required string name,
  2: optional IDs ids,
  3: list<string> tags (foo = "bar"),
}

service UserService extends BaseService {
  oneway void ping(),
  async i32 lookup(1: string key) throws (1: string message),
}
`)

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}

	root := tree.RootNode()
	if got := root.Kind(); got != "source_file" {
		t.Fatalf("root kind = %q, want source_file", got)
	}
	if root.HasError() {
		t.Fatalf("expected no parse errors, tree: %s", root.ToSexp())
	}
	if root.NamedChildCount() < 8 {
		t.Fatalf("expected multiple declarations, got %d named children", root.NamedChildCount())
	}
}

func TestLanguageRecoversOnInvalidInput(t *testing.T) {
	src := []byte(`
struct Broken {
  1: string name
  2: list<i32 values
}
service Svc {
  void ok(1: i32 id)
`)

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}

	root := tree.RootNode()
	if got := root.Kind(); got != "source_file" && got != "ERROR" {
		t.Fatalf("root kind = %q, want source_file or ERROR (recoverable)", got)
	}
	if !root.HasError() {
		t.Fatal("expected recoverable parse errors on invalid fixture")
	}
	if root.ChildCount() == 0 {
		t.Fatal("expected recovered tree to retain named children")
	}
}

func TestLanguageParsesDeprecatedPragmaticSyntax(t *testing.T) {
	src := []byte(`
senum LegacyNames {
  "FOO",
  "BAR";
}

union LegacyUnion {
  1: byte code xsd_all,
}

service LegacyService {
  async byte lookup(1: byte key);
}
`)

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}

	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("expected deprecated/pragmatic syntax fixture to parse, tree: %s", root.ToSexp())
	}

	assertContainsNamedKind(t, root, "senum_definition")
	assertContainsNamedKind(t, root, "union_definition")
	assertContainsNamedKind(t, root, "service_definition")
}

func TestQueryFilesLoadAndMatchExpectedNodes(t *testing.T) {
	src := []byte(`
// docs
struct User {
  1: string name,
}
service UserService {
  oneway void ping(),
}
`)

	parser := sitter.NewParser()
	defer parser.Close()
	lang := Language()
	if err := parser.SetLanguage(lang); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()
	if tree.RootNode().HasError() {
		t.Fatalf("unexpected parse error: %s", tree.RootNode().ToSexp())
	}

	for _, name := range QueryNames() {
		querySource, err := QuerySource(name)
		if err != nil {
			t.Fatalf("QuerySource(%q): %v", name, err)
		}
		query, qErr := sitter.NewQuery(lang, querySource)
		if qErr != nil {
			t.Fatalf("NewQuery(%q): %v", name, qErr)
		}

		qc := sitter.NewQueryCursor()
		captures := qc.Captures(query, tree.RootNode(), src)
		captureCount := 0
		for match, index := captures.Next(); match != nil; match, index = captures.Next() {
			_ = match.Captures[index].Node
			captureCount++
		}
		qc.Close()
		query.Close()

		if captureCount == 0 {
			t.Fatalf("query %q produced no captures on representative fixture", name)
		}
	}
}

func assertContainsNamedKind(t *testing.T, n *sitter.Node, want string) {
	t.Helper()
	if n.Kind() == want && n.IsNamed() {
		return
	}

	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.Children(cursor) {
		if assertContainsNamedKindRec(&child, want) {
			return
		}
	}

	t.Fatalf("did not find named node kind %q in tree", want)
}

func assertContainsNamedKindRec(n *sitter.Node, want string) bool {
	if n.Kind() == want && n.IsNamed() {
		return true
	}
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.Children(cursor) {
		if assertContainsNamedKindRec(&child, want) {
			return true
		}
	}
	return false
}
