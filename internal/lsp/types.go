// Package lsp implements the thriftls LSP server and shared protocol types.
package lsp

import "encoding/json"

// JSONRPCVersion is the supported JSON-RPC protocol version.
const JSONRPCVersion = "2.0"

// Request identifies a JSON-RPC request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError is a JSON-RPC/LSP error object.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// InitializeParams is the LSP initialize request payload subset used in v1.
type InitializeParams struct {
	ProcessID *int64 `json:"processId,omitempty"`
}

// InitializeResult is the LSP initialize response payload.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities declares supported LSP features.
type ServerCapabilities struct {
	TextDocumentSync                TextDocumentSyncOptions `json:"textDocumentSync"`
	DocumentFormattingProvider      bool                    `json:"documentFormattingProvider,omitempty"`
	DocumentRangeFormattingProvider bool                    `json:"documentRangeFormattingProvider,omitempty"`
	DocumentSymbolProvider          bool                    `json:"documentSymbolProvider,omitempty"`
	FoldingRangeProvider            bool                    `json:"foldingRangeProvider,omitempty"`
	SelectionRangeProvider          bool                    `json:"selectionRangeProvider,omitempty"`
}

// TextDocumentSyncOptions declares document sync behavior.
type TextDocumentSyncOptions struct {
	OpenClose bool `json:"openClose,omitempty"`
	Change    int  `json:"change,omitempty"`
}

const (
	// TextDocumentSyncKindIncremental is LSP incremental sync mode.
	TextDocumentSyncKindIncremental = 2
)

// TextDocumentIdentifier identifies an open document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier identifies an open document version.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

// TextDocumentItem is an LSP didOpen document payload.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId,omitempty"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

// DidOpenParams is the didOpen notification payload.
type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// Position is an LSP UTF-16 position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP UTF-16 range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// TextDocumentContentChangeEvent is a didChange text edit.
type TextDocumentContentChangeEvent struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength *int   `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

// DidChangeParams is the didChange notification payload.
type DidChangeParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// DidCloseParams is the didClose notification payload.
type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// PublishDiagnosticsParams is the LSP publishDiagnostics notification payload.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostic is a minimal LSP diagnostic payload.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// FormattingOptions is the LSP formatting options subset used in v1.
type FormattingOptions struct {
	TabSize      int  `json:"tabSize,omitempty"`
	InsertSpaces bool `json:"insertSpaces,omitempty"`
}

// DocumentFormattingParams is the LSP document formatting request payload.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Version      *int32                 `json:"version,omitempty"` // non-standard extension for stale-request tests/guards
	Options      FormattingOptions      `json:"options"`
}

// DocumentRangeFormattingParams is the LSP range formatting request payload.
type DocumentRangeFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Version      *int32                 `json:"version,omitempty"` // non-standard extension for stale-request tests/guards
	Range        Range                  `json:"range"`
	Options      FormattingOptions      `json:"options"`
}

// TextEdit is an LSP text edit.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// DocumentSymbolParams identifies the target document for symbol requests.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol is a minimal LSP document symbol payload used in v1.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// FoldingRangeParams identifies the target document for folding requests.
type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// FoldingRange is a minimal LSP folding range.
type FoldingRange struct {
	StartLine      int `json:"startLine"`
	EndLine        int `json:"endLine"`
	StartCharacter int `json:"startCharacter,omitempty"`
	EndCharacter   int `json:"endCharacter,omitempty"`
}

// SelectionRangeParams is the LSP selectionRange request payload.
type SelectionRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}

// SelectionRange is a minimal LSP selection range result.
type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}
