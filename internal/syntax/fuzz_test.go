package syntax

import (
	"context"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func FuzzParse(f *testing.F) {
	addSyntaxSeeds(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		t.Helper()
		if len(src) > 512*1024 {
			t.Skip()
		}

		tree, err := Parse(context.Background(), src, ParseOptions{URI: "fuzz.thrift"})
		if err != nil {
			t.Fatalf("Parse error: %v", err)
		}
		if tree == nil {
			t.Fatal("nil tree")
		}
		if tree.LineIndex == nil {
			t.Fatal("nil line index")
		}
		if len(tree.Tokens) == 0 {
			t.Fatal("no tokens")
		}
		if tree.Tokens[len(tree.Tokens)-1].Kind != lexer.TokenEOF {
			t.Fatalf("last token kind = %v, want EOF", tree.Tokens[len(tree.Tokens)-1].Kind)
		}
		if tree.Root == NoNode {
			t.Fatal("missing root node")
		}
		tokenCount := len(tree.Tokens)

		for i := 1; i < len(tree.Nodes); i++ { // node[0] is sentinel
			n := tree.Nodes[i]
			if err := n.Span.Validate(); err != nil {
				t.Fatalf("node[%d] invalid span %s: %v", i, n.Span, err)
			}
			if int(n.Span.End) > len(src) {
				t.Fatalf("node[%d] span %s out of bounds (len=%d)", i, n.Span, len(src))
			}
			if int(n.FirstToken) >= tokenCount {
				t.Fatalf("node[%d] first token %d out of range (len=%d)", i, n.FirstToken, tokenCount)
			}
			if int(n.LastToken) >= tokenCount {
				t.Fatalf("node[%d] last token %d out of range (len=%d)", i, n.LastToken, tokenCount)
			}
		}
	})
}

func addSyntaxSeeds(f *testing.F) {
	f.Helper()

	for _, s := range [][]byte{
		nil,
		[]byte(""),
		[]byte("struct S {\n  1: string a\n}\n"),
		[]byte("service Demo { void ping(1: i32 id) throws (1: string msg) }\n"),
		[]byte("const string X = 'unterminated\n"),
		[]byte("/* unterminated block comment"),
		[]byte("# hash comment\nstruct A {}\n"),
		{0xff, 0xfe, 0xfd}, // invalid UTF-8 bytes
	} {
		f.Add(s)
	}

	if cases, err := testutil.FormatGoldenCases(); err == nil {
		for _, c := range cases {
			f.Add(testutil.ReadFile(f, c.InputPath))
		}
	}
}
