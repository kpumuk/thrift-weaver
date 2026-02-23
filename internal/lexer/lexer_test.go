package lexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

func TestTokenAndTriviaBytesUseRawSpans(t *testing.T) {
	t.Parallel()

	src := []byte("  abc")
	tr := Trivia{Kind: TriviaWhitespace, Span: text.Span{Start: 0, End: 2}}
	tok := Token{Kind: TokenIdentifier, Span: text.Span{Start: 2, End: 5}}

	if got := string(tr.Bytes(src)); got != "  " {
		t.Fatalf("Trivia.Bytes() = %q, want %q", got, "  ")
	}
	if got := string(tok.Bytes(src)); got != "abc" {
		t.Fatalf("Token.Bytes() = %q, want %q", got, "abc")
	}
}

func TestLexGoldenRepresentativeValidInput(t *testing.T) {
	t.Parallel()

	src := []byte(`/** doc */
namespace cpp foo.bar // ns
struct User {
  1: required i64 id = 0x2A;
  2: optional string name = "A\nB";
  3: double score = .5e+1 # trailing hash comment
}
`)

	res := Lex(src)
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}

	got := renderTokens(src, res.Tokens)
	want := strings.TrimSpace(`
KwNamespace("namespace") lead=[DocComment("/** doc */"),Newline("\n")]
Identifier("cpp") lead=[Whitespace(" ")]
Identifier("foo") lead=[Whitespace(" ")]
Dot(".") lead=[]
Identifier("bar") lead=[]
KwStruct("struct") lead=[Whitespace(" "),LineComment("// ns"),Newline("\n")]
Identifier("User") lead=[Whitespace(" ")]
LBrace("{") lead=[Whitespace(" ")]
IntLiteral("1") lead=[Newline("\n"),Whitespace("  ")]
Colon(":") lead=[]
KwRequired("required") lead=[Whitespace(" ")]
Kwi64("i64") lead=[Whitespace(" ")]
Identifier("id") lead=[Whitespace(" ")]
Equal("=") lead=[Whitespace(" ")]
IntLiteral("0x2A") lead=[Whitespace(" ")]
Semi(";") lead=[]
IntLiteral("2") lead=[Newline("\n"),Whitespace("  ")]
Colon(":") lead=[]
KwOptional("optional") lead=[Whitespace(" ")]
KwString("string") lead=[Whitespace(" ")]
Identifier("name") lead=[Whitespace(" ")]
Equal("=") lead=[Whitespace(" ")]
StringLiteral("\"A\\nB\"") lead=[Whitespace(" ")]
Semi(";") lead=[]
IntLiteral("3") lead=[Newline("\n"),Whitespace("  ")]
Colon(":") lead=[]
KwDouble("double") lead=[Whitespace(" ")]
Identifier("score") lead=[Whitespace(" ")]
Equal("=") lead=[Whitespace(" ")]
FloatLiteral(".5e+1") lead=[Whitespace(" ")]
RBrace("}") lead=[Whitespace(" "),HashComment("# trailing hash comment"),Newline("\n")]
EOF("") lead=[Newline("\n")]
`)
	if got != want {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestLexMalformedInputsEmitErrorTokensAndDiagnostics(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src          []byte
		wantDiagCode DiagnosticCode
	}{
		"unterminated string": {
			src:          []byte(`"abc`),
			wantDiagCode: DiagnosticUnterminatedString,
		},
		"unterminated block comment": {
			src:          []byte("/* abc"),
			wantDiagCode: DiagnosticUnterminatedBlockComment,
		},
		"invalid byte": {
			src:          []byte{0xff},
			wantDiagCode: DiagnosticInvalidByte,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := Lex(tc.src)
			if len(res.Diagnostics) == 0 {
				t.Fatalf("expected diagnostics for %q", tc.src)
			}
			if res.Diagnostics[0].Code != tc.wantDiagCode {
				t.Fatalf("diagnostic code = %s, want %s", res.Diagnostics[0].Code, tc.wantDiagCode)
			}
			if len(res.Tokens) == 0 || res.Tokens[0].Kind != TokenError {
				t.Fatalf("expected first token to be TokenError, got %+v", res.Tokens)
			}
			if !res.Tokens[0].Flags.Has(TokenFlagMalformed) {
				t.Fatalf("expected malformed flag on error token, got %v", res.Tokens[0].Flags)
			}
			if got := res.Tokens[len(res.Tokens)-1].Kind; got != TokenEOF {
				t.Fatalf("expected EOF token at end, got %s", got)
			}
		})
	}
}

func TestLexTriviaAndLiteralFidelity(t *testing.T) {
	t.Parallel()

	src := []byte("  // c1\r\n# c2\r\nconst i32 x = 0XBeEf;\n\"a\\\"b\" 'bee'")
	res := Lex(src)

	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}

	var gotComments []string
	var gotLiterals []string
	for _, tok := range res.Tokens {
		for _, tr := range tok.Leading {
			if tr.Kind == TriviaLineComment || tr.Kind == TriviaHashComment {
				gotComments = append(gotComments, string(tr.Bytes(src)))
			}
		}
		if tok.Kind == TokenIntLiteral || tok.Kind == TokenStringLiteral {
			gotLiterals = append(gotLiterals, string(tok.Bytes(src)))
		}
	}

	wantComments := []string{"// c1", "# c2"}
	if fmt.Sprint(gotComments) != fmt.Sprint(wantComments) {
		t.Fatalf("comments = %v, want %v", gotComments, wantComments)
	}

	// Literal spellings must be preserved exactly.
	wantLiterals := []string{"0XBeEf", "\"a\\\"b\"", "'bee'"}
	if fmt.Sprint(gotLiterals) != fmt.Sprint(wantLiterals) {
		t.Fatalf("literals = %v, want %v", gotLiterals, wantLiterals)
	}
}

func TestLexNoPanicsOnMalformedCorpusSamples(t *testing.T) {
	t.Parallel()

	inputs := [][]byte{
		[]byte(`"`),
		[]byte(`/*`),
		[]byte(`0x`),
		{0xff, '{', 0xfe},
		[]byte("struct X {\n 1: string name = \"a\n}\n"),
	}

	for _, src := range inputs {
		t.Run(fmt.Sprintf("%q", src), func(t *testing.T) {
			t.Parallel()
			_ = Lex(src)
		})
	}
}

func renderTokens(src []byte, tokens []Token) string {
	lines := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		lines = append(lines, fmt.Sprintf("%s(%q) lead=%s", tok.Kind, tok.Bytes(src), renderLeading(src, tok.Leading)))
	}
	return strings.Join(lines, "\n")
}

func renderLeading(src []byte, trivia []Trivia) string {
	if len(trivia) == 0 {
		return "[]"
	}

	parts := make([]string, 0, len(trivia))
	for _, tr := range trivia {
		parts = append(parts, fmt.Sprintf("%s(%q)", tr.Kind, tr.Bytes(src)))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
