// Package lexer provides a lossless token/trivia lexer for Thrift IDL source.
package lexer

import (
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

// TokenKind identifies the syntactic category of a token.
type TokenKind uint16

// TokenKind values used by the Thrift lexer.
const (
	TokenError TokenKind = iota
	TokenEOF
	TokenIdentifier
	TokenIntLiteral
	TokenFloatLiteral
	TokenStringLiteral

	TokenKwInclude
	TokenKwCppInclude
	TokenKwNamespace
	TokenKwConst
	TokenKwTypedef
	TokenKwEnum
	TokenKwSenum
	TokenKwStruct
	TokenKwUnion
	TokenKwException
	TokenKwService
	TokenKwExtends
	TokenKwOneway
	TokenKwAsync
	TokenKwThrows
	TokenKwRequired
	TokenKwOptional
	TokenKwVoid
	TokenKwBool
	TokenKwByte
	TokenKwi8
	TokenKwi16
	TokenKwi32
	TokenKwi64
	TokenKwDouble
	TokenKwString
	TokenKwBinary
	TokenKwMap
	TokenKwList
	TokenKwSet
	TokenKwTrue
	TokenKwFalse

	TokenLBrace
	TokenRBrace
	TokenLParen
	TokenRParen
	TokenLBracket
	TokenRBracket
	TokenLAngle
	TokenRAngle
	TokenComma
	TokenSemi
	TokenColon
	TokenEqual
	TokenDot
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
)

func (k TokenKind) String() string {
	switch k {
	case TokenError:
		return "Error"
	case TokenEOF:
		return "EOF"
	case TokenIdentifier:
		return "Identifier"
	case TokenIntLiteral:
		return "IntLiteral"
	case TokenFloatLiteral:
		return "FloatLiteral"
	case TokenStringLiteral:
		return "StringLiteral"
	case TokenKwInclude:
		return "KwInclude"
	case TokenKwCppInclude:
		return "KwCppInclude"
	case TokenKwNamespace:
		return "KwNamespace"
	case TokenKwConst:
		return "KwConst"
	case TokenKwTypedef:
		return "KwTypedef"
	case TokenKwEnum:
		return "KwEnum"
	case TokenKwSenum:
		return "KwSenum"
	case TokenKwStruct:
		return "KwStruct"
	case TokenKwUnion:
		return "KwUnion"
	case TokenKwException:
		return "KwException"
	case TokenKwService:
		return "KwService"
	case TokenKwExtends:
		return "KwExtends"
	case TokenKwOneway:
		return "KwOneway"
	case TokenKwAsync:
		return "KwAsync"
	case TokenKwThrows:
		return "KwThrows"
	case TokenKwRequired:
		return "KwRequired"
	case TokenKwOptional:
		return "KwOptional"
	case TokenKwVoid:
		return "KwVoid"
	case TokenKwBool:
		return "KwBool"
	case TokenKwByte:
		return "KwByte"
	case TokenKwi8:
		return "Kwi8"
	case TokenKwi16:
		return "Kwi16"
	case TokenKwi32:
		return "Kwi32"
	case TokenKwi64:
		return "Kwi64"
	case TokenKwDouble:
		return "KwDouble"
	case TokenKwString:
		return "KwString"
	case TokenKwBinary:
		return "KwBinary"
	case TokenKwMap:
		return "KwMap"
	case TokenKwList:
		return "KwList"
	case TokenKwSet:
		return "KwSet"
	case TokenKwTrue:
		return "KwTrue"
	case TokenKwFalse:
		return "KwFalse"
	case TokenLBrace:
		return "LBrace"
	case TokenRBrace:
		return "RBrace"
	case TokenLParen:
		return "LParen"
	case TokenRParen:
		return "RParen"
	case TokenLBracket:
		return "LBracket"
	case TokenRBracket:
		return "RBracket"
	case TokenLAngle:
		return "LAngle"
	case TokenRAngle:
		return "RAngle"
	case TokenComma:
		return "Comma"
	case TokenSemi:
		return "Semi"
	case TokenColon:
		return "Colon"
	case TokenEqual:
		return "Equal"
	case TokenDot:
		return "Dot"
	case TokenPlus:
		return "Plus"
	case TokenMinus:
		return "Minus"
	case TokenStar:
		return "Star"
	case TokenSlash:
		return "Slash"
	default:
		return fmt.Sprintf("TokenKind(%d)", k)
	}
}

// TokenFlags carry metadata about the token source or origin.
type TokenFlags uint8

// TokenFlags values describe token provenance or recovery state.
const (
	TokenFlagMalformed TokenFlags = 1 << iota
	TokenFlagSynthesized
	TokenFlagRecovered
)

// Has reports whether all bits in mask are set.
func (f TokenFlags) Has(mask TokenFlags) bool {
	return f&mask == mask
}

// Token is a lexed token with a source span and leading trivia.
type Token struct {
	Kind    TokenKind
	Span    text.Span
	Leading []Trivia
	Flags   TokenFlags
}

// Bytes returns the token bytes referenced by Span or nil if Span is invalid for src.
func (t Token) Bytes(src []byte) []byte {
	return bytesForSpan(src, t.Span)
}

var keywordKinds = map[string]TokenKind{
	"include":     TokenKwInclude,
	"cpp_include": TokenKwCppInclude,
	"namespace":   TokenKwNamespace,
	"const":       TokenKwConst,
	"typedef":     TokenKwTypedef,
	"enum":        TokenKwEnum,
	"senum":       TokenKwSenum,
	"struct":      TokenKwStruct,
	"union":       TokenKwUnion,
	"exception":   TokenKwException,
	"service":     TokenKwService,
	"extends":     TokenKwExtends,
	"oneway":      TokenKwOneway,
	"async":       TokenKwAsync,
	"throws":      TokenKwThrows,
	"required":    TokenKwRequired,
	"optional":    TokenKwOptional,
	"void":        TokenKwVoid,
	"bool":        TokenKwBool,
	"byte":        TokenKwByte,
	"i8":          TokenKwi8,
	"i16":         TokenKwi16,
	"i32":         TokenKwi32,
	"i64":         TokenKwi64,
	"double":      TokenKwDouble,
	"string":      TokenKwString,
	"binary":      TokenKwBinary,
	"map":         TokenKwMap,
	"list":        TokenKwList,
	"set":         TokenKwSet,
	"true":        TokenKwTrue,
	"false":       TokenKwFalse,
}

func bytesForSpan(src []byte, sp text.Span) []byte {
	if !sp.IsValid() {
		return nil
	}
	if sp.End > text.ByteOffset(len(src)) {
		return nil
	}
	return src[sp.Start:sp.End]
}
