package lexer

import (
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

// TriviaKind identifies non-token source segments attached as leading trivia.
type TriviaKind uint8

// TriviaKind values describe trivia categories.
const (
	TriviaWhitespace TriviaKind = iota
	TriviaNewline
	TriviaLineComment
	TriviaHashComment
	TriviaBlockComment
	TriviaDocComment
	// TriviaBOM is a UTF-8 byte order mark at offset 0. A BOM anywhere else is an error token.
	TriviaBOM
)

func (k TriviaKind) String() string {
	switch k {
	case TriviaWhitespace:
		return "Whitespace"
	case TriviaNewline:
		return "Newline"
	case TriviaLineComment:
		return "LineComment"
	case TriviaHashComment:
		return "HashComment"
	case TriviaBlockComment:
		return "BlockComment"
	case TriviaDocComment:
		return "DocComment"
	case TriviaBOM:
		return "BOM"
	default:
		return fmt.Sprintf("TriviaKind(%d)", k)
	}
}

// Trivia represents a non-token source span (whitespace/comments/newlines).
type Trivia struct {
	Kind TriviaKind
	Span text.Span
}

// Bytes returns the trivia bytes referenced by Span or nil if Span is invalid for src.
func (t Trivia) Bytes(src []byte) []byte {
	return bytesForSpan(src, t.Span)
}
