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

// CancelParams is the LSP $/cancelRequest notification payload.
type CancelParams struct {
	ID json.RawMessage `json:"id"`
}

// InitializeParams is the LSP initialize request payload subset used in v1.
type InitializeParams struct {
	ProcessID        *int64            `json:"processId,omitempty"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders,omitempty"`
}

// WorkspaceFolder is the LSP workspace folder descriptor used during initialize.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// InitializeResult is the LSP initialize response payload.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities declares supported LSP features.
type ServerCapabilities struct {
	TextDocumentSync                TextDocumentSyncOptions `json:"textDocumentSync"`
	DefinitionProvider              bool                    `json:"definitionProvider,omitempty"`
	ReferencesProvider              bool                    `json:"referencesProvider,omitempty"`
	RenameProvider                  bool                    `json:"renameProvider,omitempty"`
	DocumentFormattingProvider      bool                    `json:"documentFormattingProvider,omitempty"`
	DocumentRangeFormattingProvider bool                    `json:"documentRangeFormattingProvider,omitempty"`
	DocumentSymbolProvider          bool                    `json:"documentSymbolProvider,omitempty"`
	FoldingRangeProvider            bool                    `json:"foldingRangeProvider,omitempty"`
	SelectionRangeProvider          bool                    `json:"selectionRangeProvider,omitempty"`
	WorkspaceSymbolProvider         bool                    `json:"workspaceSymbolProvider,omitempty"`
	SemanticTokensProvider          *SemanticTokensOptions  `json:"semanticTokensProvider,omitempty"`
}

// TextDocumentSyncOptions declares document sync behavior.
type TextDocumentSyncOptions struct {
	OpenClose bool         `json:"openClose,omitempty"`
	Change    int          `json:"change,omitempty"`
	Save      *SaveOptions `json:"save,omitempty"`
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

// DidSaveParams is the didSave notification payload.
type DidSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// SaveOptions controls textDocument/didSave payload behavior.
type SaveOptions struct {
	IncludeText bool `json:"includeText,omitempty"`
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

// TextDocumentPositionParams identifies a document position request.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DefinitionParams is the textDocument/definition request payload.
type DefinitionParams = TextDocumentPositionParams

// ReferenceContext configures reference lookup behavior.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the textDocument/references request payload.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
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

// Location is a minimal LSP location.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// WorkspaceSymbolParams is the workspace/symbol request payload.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// SymbolInformation is a minimal workspace symbol result.
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// PrepareRenameParams is the textDocument/prepareRename request payload.
type PrepareRenameParams = TextDocumentPositionParams

// RenameParams is the textDocument/rename request payload.
type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

// PrepareRenameResult describes the editable range and placeholder for rename.
type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder,omitempty"`
}

// OptionalVersionedTextDocumentIdentifier identifies a document for workspace edits.
type OptionalVersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int32 `json:"version,omitempty"`
}

// TextDocumentEdit is a workspace edit for one document.
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// WorkspaceEdit is an LSP workspace edit response payload.
type WorkspaceEdit struct {
	DocumentChanges []TextDocumentEdit `json:"documentChanges,omitempty"`
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

// SemanticTokensParams identifies the target document for semantic token requests.
type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// SemanticTokens is the LSP semantic tokens full response payload.
type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

// SemanticTokensOptions declares semantic token server support.
type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full,omitempty"`
	Range  bool                 `json:"range,omitempty"`
}

// SemanticTokensLegend describes token types/modifiers indexes used in SemanticTokens.Data.
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}
