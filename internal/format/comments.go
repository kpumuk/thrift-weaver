package format

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
)

// CommentEmitter emits token-leading trivia comments with normalized spacing/newlines.
type CommentEmitter struct {
	Indent        string
	Newline       string
	MaxBlankLines int
}

// EmitLeading renders comment/newline trivia preceding a token.
// Whitespace trivia is normalized (not preserved byte-for-byte).
func (e CommentEmitter) EmitLeading(src []byte, trivia []lexer.Trivia, indentLevel int) ([]byte, error) {
	norm, err := e.normalize()
	if err != nil {
		return nil, err
	}
	if len(trivia) == 0 {
		return nil, nil
	}

	var out bytes.Buffer
	pendingBreaks := 0
	pendingSpace := false
	atLineStart := true

	for _, tr := range trivia {
		switch tr.Kind {
		case lexer.TriviaWhitespace:
			if !atLineStart && pendingBreaks == 0 {
				pendingSpace = true
			}
		case lexer.TriviaNewline:
			pendingBreaks++
			pendingSpace = false
			atLineStart = true
		case lexer.TriviaLineComment, lexer.TriviaHashComment, lexer.TriviaBlockComment, lexer.TriviaDocComment:
			writePendingBreaks(&out, norm.Newline, pendingBreaks, norm.MaxBlankLines)
			if pendingBreaks > 0 {
				atLineStart = true
			}
			pendingBreaks = 0
			if !atLineStart && pendingSpace {
				out.WriteByte(' ')
			}
			if atLineStart {
				out.WriteString(strings.Repeat(norm.Indent, indentLevel))
			}

			raw := tr.Bytes(src)
			if raw == nil {
				return nil, fmt.Errorf("invalid trivia span %s", tr.Span)
			}
			out.Write(raw)
			atLineStart = endsWithLineBreak(raw)
			pendingSpace = false
		default:
			// Unknown trivia kind should not break formatting; ignore it conservatively.
		}
	}

	if pendingBreaks > 0 {
		writePendingBreaks(&out, norm.Newline, pendingBreaks, norm.MaxBlankLines)
		out.WriteString(strings.Repeat(norm.Indent, indentLevel))
	}

	return out.Bytes(), nil
}

func (e CommentEmitter) normalize() (CommentEmitter, error) {
	if e.Indent == "" {
		e.Indent = defaultIndent
	}
	if e.Newline == "" {
		e.Newline = "\n"
	}
	if e.Newline != "\n" && e.Newline != "\r\n" {
		return CommentEmitter{}, fmt.Errorf("invalid newline %q", e.Newline)
	}
	if e.MaxBlankLines < 0 {
		return CommentEmitter{}, fmt.Errorf("invalid MaxBlankLines %d", e.MaxBlankLines)
	}
	return e, nil
}

func writePendingBreaks(out *bytes.Buffer, newline string, breaks, maxBlankLines int) {
	if breaks <= 0 {
		return
	}
	limit := maxBlankLines + 1
	limit = max(limit, 1)
	if breaks > limit {
		breaks = limit
	}
	out.WriteString(strings.Repeat(newline, breaks))
}

func endsWithLineBreak(b []byte) bool {
	return bytes.HasSuffix(b, []byte("\n")) || bytes.HasSuffix(b, []byte("\r\n"))
}
