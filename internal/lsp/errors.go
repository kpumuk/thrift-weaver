package lsp

import "errors"

const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternalError  = -32603

	// lspErrorContentModified indicates a stale versioned request in LSP.
	lspErrorContentModified = -32801
	// lspErrorRequestCancelled indicates cancellation.
	lspErrorRequestCancelled = -32800
	// lspErrorRequestFailed indicates request failure (unsafe formatting, etc.).
	lspErrorRequestFailed = -32803
)

var (
	// ErrShutdownRequested is returned internally after exit notification is handled.
	ErrShutdownRequested = errors.New("lsp server exit requested")
	// ErrDocumentNotOpen indicates a request referenced a document that is not tracked.
	ErrDocumentNotOpen = errors.New("document is not open")
	// ErrStaleVersion indicates a request version is older than the current snapshot.
	ErrStaleVersion = errors.New("stale document version")
)
