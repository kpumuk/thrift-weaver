package format

import (
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
)

func TestCommentEmitterPreservesCommentBytesAndCapsBlankLines(t *testing.T) {
	t.Parallel()

	src := []byte("/*a*/   /*b*/\n\n\n//c\nfoo")
	lexed := lexer.Lex(src)
	if len(lexed.Tokens) < 2 {
		t.Fatalf("expected token stream, got %d tokens", len(lexed.Tokens))
	}

	got, err := (CommentEmitter{
		Indent:        "  ",
		Newline:       "\n",
		MaxBlankLines: 1,
	}).EmitLeading(src, lexed.Tokens[0].Leading, 1)
	if err != nil {
		t.Fatalf("EmitLeading: %v", err)
	}

	want := "  /*a*/ /*b*/\n\n  //c\n  "
	if string(got) != want {
		t.Fatalf("EmitLeading = %q, want %q", got, want)
	}
}

func TestCommentEmitterNormalizesCRLF(t *testing.T) {
	t.Parallel()

	src := []byte("# a\r\n\r\nfoo")
	lexed := lexer.Lex(src)

	got, err := (CommentEmitter{
		Indent:        "\t",
		Newline:       "\r\n",
		MaxBlankLines: 2,
	}).EmitLeading(src, lexed.Tokens[0].Leading, 1)
	if err != nil {
		t.Fatalf("EmitLeading: %v", err)
	}

	want := "\t# a\r\n\r\n\t"
	if string(got) != want {
		t.Fatalf("EmitLeading = %q, want %q", got, want)
	}
}
