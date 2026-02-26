package lexer

import (
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func FuzzLex(f *testing.F) {
	addCommonSeeds(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		t.Helper()

		// Keep the target responsive; fuzzing should explore shape, not spend cycles on huge blobs.
		if len(src) > 512*1024 {
			t.Skip()
		}

		res := Lex(src)
		if len(res.Tokens) == 0 {
			t.Fatal("lexer returned no tokens")
		}
		last := res.Tokens[len(res.Tokens)-1]
		if last.Kind != TokenEOF {
			t.Fatalf("last token kind = %v, want EOF", last.Kind)
		}

		prevEnd := -1
		for i, tok := range res.Tokens {
			if err := tok.Span.Validate(); err != nil {
				t.Fatalf("token[%d] invalid span %s: %v", i, tok.Span, err)
			}
			if int(tok.Span.End) > len(src) {
				t.Fatalf("token[%d] span %s out of bounds (len=%d)", i, tok.Span, len(src))
			}
			if prevEnd > int(tok.Span.Start) {
				t.Fatalf("token spans out of order: prevEnd=%d curStart=%d", prevEnd, tok.Span.Start)
			}
			prevEnd = int(tok.Span.End)

			for j, tr := range tok.Leading {
				if err := tr.Span.Validate(); err != nil {
					t.Fatalf("token[%d].leading[%d] invalid span %s: %v", i, j, tr.Span, err)
				}
				if int(tr.Span.End) > len(src) {
					t.Fatalf("token[%d].leading[%d] span %s out of bounds (len=%d)", i, j, tr.Span, len(src))
				}
			}
		}
	})
}

func addCommonSeeds(f *testing.F) {
	f.Helper()

	for _, s := range [][]byte{
		nil,
		[]byte(""),
		[]byte("struct S {\n  1: string a\n}\n"),
		[]byte("service Demo { void ping(1: i32 id) }\n"),
		[]byte("const string X = 'unterminated\n"), // malformed string
		[]byte("/* unterminated block comment"),    // malformed block comment
		{0xff, 0xfe, 0xfd},                         // invalid UTF-8 bytes
		[]byte("const uuid X = '{00112233-4455-6677-8899-aabbccddeeff}'\n"),
	} {
		f.Add(s)
	}

	if cases, err := testutil.FormatGoldenCases(); err == nil {
		for _, c := range cases {
			f.Add(testutil.ReadFile(f, c.InputPath))
		}
	}
}
