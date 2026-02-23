package lexer

import (
	"fmt"
	"unicode/utf8"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

// DiagnosticCode identifies lexer diagnostic categories.
type DiagnosticCode string

// DiagnosticCode values emitted by the lexer.
const (
	DiagnosticInvalidByte              DiagnosticCode = "LEX_INVALID_BYTE"
	DiagnosticUnknownCharacter         DiagnosticCode = "LEX_UNKNOWN_CHARACTER"
	DiagnosticUnterminatedString       DiagnosticCode = "LEX_UNTERMINATED_STRING"
	DiagnosticUnterminatedBlockComment DiagnosticCode = "LEX_UNTERMINATED_BLOCK_COMMENT"
	DiagnosticInvalidHexLiteral        DiagnosticCode = "LEX_INVALID_HEX_LITERAL"
)

// Diagnostic is a lexer-level issue with source location.
type Diagnostic struct {
	Code    DiagnosticCode
	Message string
	Span    text.Span
}

// Result is the output of lexing source bytes.
type Result struct {
	Tokens      []Token
	Diagnostics []Diagnostic
}

// Lex tokenizes src into a lossless token stream with leading trivia.
func Lex(src []byte) Result {
	l := scanner{src: src}
	l.run()
	return Result{
		Tokens:      l.tokens,
		Diagnostics: l.diagnostics,
	}
}

type scanner struct {
	src         []byte
	i           int
	tokens      []Token
	diagnostics []Diagnostic
}

func (s *scanner) run() {
	for {
		leading, errTok := s.scanLeadingTrivia()
		if errTok != nil {
			errTok.Leading = leading
			s.tokens = append(s.tokens, *errTok)
			continue
		}

		if s.eof() {
			s.tokens = append(s.tokens, Token{
				Kind:    TokenEOF,
				Span:    span(len(s.src), len(s.src)),
				Leading: leading,
			})
			return
		}

		tok := s.scanToken()
		tok.Leading = leading
		s.tokens = append(s.tokens, tok)
	}
}

func (s *scanner) scanLeadingTrivia() ([]Trivia, *Token) {
	var out []Trivia

	for !s.eof() {
		start := s.i
		switch b := s.src[s.i]; b {
		case ' ', '\t', '\v', '\f':
			for !s.eof() && isHorizontalSpace(s.src[s.i]) {
				s.i++
			}
			out = append(out, Trivia{Kind: TriviaWhitespace, Span: span(start, s.i)})
		case '\n':
			s.i++
			out = append(out, Trivia{Kind: TriviaNewline, Span: span(start, s.i)})
		case '\r':
			s.i++
			if !s.eof() && s.src[s.i] == '\n' {
				s.i++
			}
			out = append(out, Trivia{Kind: TriviaNewline, Span: span(start, s.i)})
		case '#':
			s.scanLineComment()
			out = append(out, Trivia{Kind: TriviaHashComment, Span: span(start, s.i)})
		case '/':
			if s.peekByte(1) == '/' {
				s.i += 2
				s.scanLineComment()
				out = append(out, Trivia{Kind: TriviaLineComment, Span: span(start, s.i)})
				continue
			}
			if s.peekByte(1) == '*' {
				t, errTok := s.scanBlockCommentOrError()
				if errTok != nil {
					return out, errTok
				}
				out = append(out, t)
				continue
			}
			return out, nil
		default:
			if b >= utf8.RuneSelf {
				if r, size := utf8.DecodeRune(s.src[s.i:]); r == utf8.RuneError && size == 1 {
					s.i++
					return out, s.makeErrorToken(start, s.i, DiagnosticInvalidByte, "invalid UTF-8 byte")
				}
			}
			return out, nil
		}
	}

	return out, nil
}

func (s *scanner) scanToken() Token {
	start := s.i
	b := s.src[s.i]

	switch {
	case isIdentStart(b):
		s.i++
		for !s.eof() && isIdentPart(s.src[s.i]) {
			s.i++
		}
		tok := Token{
			Kind: TokenIdentifier,
			Span: span(start, s.i),
		}
		if kind, ok := keywordKinds[string(s.src[start:s.i])]; ok {
			tok.Kind = kind
		}
		return tok
	case isDigit(b):
		return s.scanNumber()
	case b == '.' && isDigit(s.peekByte(1)):
		return s.scanLeadingDotFloat()
	case b == '"' || b == '\'':
		return s.scanString()
	case b >= utf8.RuneSelf:
		r, size := utf8.DecodeRune(s.src[s.i:])
		if r == utf8.RuneError && size == 1 {
			s.i++
			return *s.makeErrorToken(start, start+1, DiagnosticInvalidByte, "invalid UTF-8 byte")
		}
		s.i += size
		return *s.makeErrorToken(start, s.i, DiagnosticUnknownCharacter, "unsupported non-ASCII token character")
	default:
		s.i++
		switch b {
		case '{':
			return Token{Kind: TokenLBrace, Span: span(start, s.i)}
		case '}':
			return Token{Kind: TokenRBrace, Span: span(start, s.i)}
		case '(':
			return Token{Kind: TokenLParen, Span: span(start, s.i)}
		case ')':
			return Token{Kind: TokenRParen, Span: span(start, s.i)}
		case '[':
			return Token{Kind: TokenLBracket, Span: span(start, s.i)}
		case ']':
			return Token{Kind: TokenRBracket, Span: span(start, s.i)}
		case '<':
			return Token{Kind: TokenLAngle, Span: span(start, s.i)}
		case '>':
			return Token{Kind: TokenRAngle, Span: span(start, s.i)}
		case ',':
			return Token{Kind: TokenComma, Span: span(start, s.i)}
		case ';':
			return Token{Kind: TokenSemi, Span: span(start, s.i)}
		case ':':
			return Token{Kind: TokenColon, Span: span(start, s.i)}
		case '=':
			return Token{Kind: TokenEqual, Span: span(start, s.i)}
		case '.':
			return Token{Kind: TokenDot, Span: span(start, s.i)}
		case '+':
			return Token{Kind: TokenPlus, Span: span(start, s.i)}
		case '-':
			return Token{Kind: TokenMinus, Span: span(start, s.i)}
		case '*':
			return Token{Kind: TokenStar, Span: span(start, s.i)}
		case '/':
			return Token{Kind: TokenSlash, Span: span(start, s.i)}
		default:
			return *s.makeErrorToken(start, s.i, DiagnosticUnknownCharacter, fmt.Sprintf("unknown character %q", b))
		}
	}
}

func (s *scanner) scanNumber() Token {
	start := s.i
	if s.src[s.i] == '0' && (s.peekByte(1) == 'x' || s.peekByte(1) == 'X') {
		s.i += 2
		hexStart := s.i
		for !s.eof() && isHexDigit(s.src[s.i]) {
			s.i++
		}
		if s.i == hexStart {
			return *s.makeErrorToken(start, s.i, DiagnosticInvalidHexLiteral, "invalid hex literal")
		}
		return Token{Kind: TokenIntLiteral, Span: span(start, s.i)}
	}

	for !s.eof() && isDigit(s.src[s.i]) {
		s.i++
	}

	kind := TokenIntLiteral
	if s.peekByte(0) == '.' && isDigit(s.peekByte(1)) {
		kind = TokenFloatLiteral
		s.i++ // '.'
		for !s.eof() && isDigit(s.src[s.i]) {
			s.i++
		}
	}

	if s.tryScanExponent() {
		kind = TokenFloatLiteral
	}

	return Token{Kind: kind, Span: span(start, s.i)}
}

func (s *scanner) scanLeadingDotFloat() Token {
	start := s.i
	s.i++ // '.'
	for !s.eof() && isDigit(s.src[s.i]) {
		s.i++
	}
	_ = s.tryScanExponent()
	return Token{Kind: TokenFloatLiteral, Span: span(start, s.i)}
}

func (s *scanner) tryScanExponent() bool {
	if s.eof() {
		return false
	}
	if s.src[s.i] != 'e' && s.src[s.i] != 'E' {
		return false
	}

	j := s.i + 1
	if j < len(s.src) && (s.src[j] == '+' || s.src[j] == '-') {
		j++
	}
	if j >= len(s.src) || !isDigit(s.src[j]) {
		return false
	}

	s.i = j + 1
	for !s.eof() && isDigit(s.src[s.i]) {
		s.i++
	}
	return true
}

func (s *scanner) scanString() Token {
	start := s.i
	quote := s.src[s.i]
	s.i++

	for !s.eof() {
		switch s.src[s.i] {
		case quote:
			s.i++
			return Token{Kind: TokenStringLiteral, Span: span(start, s.i)}
		case '\\':
			s.i++
			if !s.eof() {
				s.i++
			}
		case '\r', '\n':
			return *s.makeErrorToken(start, s.i, DiagnosticUnterminatedString, "unterminated string literal")
		default:
			s.i++
		}
	}

	return *s.makeErrorToken(start, s.i, DiagnosticUnterminatedString, "unterminated string literal")
}

func (s *scanner) scanLineComment() {
	// Caller handles prefixes ('#' or '//').
	for !s.eof() && s.src[s.i] != '\n' && s.src[s.i] != '\r' {
		s.i++
	}
}

func (s *scanner) scanBlockCommentOrError() (Trivia, *Token) {
	start := s.i
	isDoc := s.peekByte(2) == '*'
	s.i += 2 // consume /*

	for !s.eof() {
		if s.src[s.i] == '*' && s.peekByte(1) == '/' {
			s.i += 2
			kind := TriviaBlockComment
			if isDoc {
				kind = TriviaDocComment
			}
			return Trivia{Kind: kind, Span: span(start, s.i)}, nil
		}
		s.i++
	}

	return Trivia{}, s.makeErrorToken(start, s.i, DiagnosticUnterminatedBlockComment, "unterminated block comment")
}

func (s *scanner) makeErrorToken(start, end int, code DiagnosticCode, msg string) *Token {
	sp := span(start, end)
	s.diagnostics = append(s.diagnostics, Diagnostic{
		Code:    code,
		Message: msg,
		Span:    sp,
	})
	return &Token{
		Kind:  TokenError,
		Span:  sp,
		Flags: TokenFlagMalformed,
	}
}

func (s *scanner) eof() bool {
	return s.i >= len(s.src)
}

func (s *scanner) peekByte(delta int) byte {
	j := s.i + delta
	if j < 0 || j >= len(s.src) {
		return 0
	}
	return s.src[j]
}

func span(start, end int) text.Span {
	return text.Span{Start: text.ByteOffset(start), End: text.ByteOffset(end)}
}

func isHorizontalSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\v', '\f':
		return true
	default:
		return false
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHexDigit(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || isDigit(b)
}
