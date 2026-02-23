package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Server is a thrift LSP server with an in-memory snapshot store.
type Server struct {
	store *SnapshotStore

	mu            sync.Mutex
	shutdown      bool
	exitRequested bool
}

// NewServer creates a new LSP server instance.
func NewServer() *Server {
	return &Server{store: NewSnapshotStore()}
}

// Store returns the backing snapshot store (primarily for tests and future handlers).
func (s *Server) Store() *SnapshotStore {
	if s == nil {
		return nil
	}
	return s.store
}

// Run serves JSON-RPC/LSP messages using Content-Length framing.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if s == nil {
		return errors.New("nil Server")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	br := bufio.NewReader(in)
	bw := bufio.NewWriter(out)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := readFramedMessage(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = s.writeErrorResponse(bw, nil, jsonRPCParseError, err.Error())
			_ = bw.Flush()
			continue
		}
		if len(body) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			_ = s.writeErrorResponse(bw, nil, jsonRPCParseError, err.Error())
			_ = bw.Flush()
			continue
		}
		if req.JSONRPC != "" && req.JSONRPC != JSONRPCVersion {
			_ = s.writeErrorResponse(bw, req.ID, jsonRPCInvalidRequest, "unsupported jsonrpc version")
			_ = bw.Flush()
			continue
		}
		if req.Method == "" {
			// Ignore client responses/unknown envelopes in v1.
			continue
		}

		if err := s.dispatch(ctx, bw, req); err != nil {
			if errors.Is(err, ErrShutdownRequested) {
				return nil
			}
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
	}
}

//nolint:funcorder // dispatch is kept near Run for readability of request flow.
func (s *Server) dispatch(ctx context.Context, w *bufio.Writer, req Request) error {
	isRequest := len(req.ID) != 0

	writeResp := func(result any) error {
		if !isRequest {
			return nil
		}
		return s.writeResponse(w, Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: result})
	}
	writeErr := func(code int, msg string) error {
		if !isRequest {
			return nil
		}
		return s.writeErrorResponse(w, req.ID, code, msg)
	}

	switch req.Method {
	case "initialize":
		var p InitializeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return writeErr(jsonRPCInvalidParams, err.Error())
			}
		}
		res, err := s.Initialize(ctx, p)
		if err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return writeResp(res)
	case "shutdown":
		if err := s.Shutdown(ctx); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return writeResp(struct{}{})
	case "exit":
		s.Exit()
		return ErrShutdownRequested
	case "textDocument/didOpen":
		var p DidOpenParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidOpen(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return nil
	case "textDocument/didChange":
		var p DidChangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidChange(ctx, p); err != nil {
			code := jsonRPCInternalError
			switch {
			case errors.Is(err, ErrStaleVersion):
				code = lspErrorContentModified
			case errors.Is(err, context.Canceled):
				code = lspErrorRequestCancelled
			case errors.Is(err, ErrDocumentNotOpen):
				code = jsonRPCInvalidParams
			}
			return writeErr(code, err.Error())
		}
		return nil
	case "textDocument/didClose":
		var p DidCloseParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidClose(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return nil
	case "textDocument/formatting":
		var p DocumentFormattingParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		edits, err := s.Formatting(ctx, p)
		if err != nil {
			return writeErr(lspErrorRequestFailed, err.Error())
		}
		return writeResp(edits)
	case "textDocument/rangeFormatting":
		var p DocumentRangeFormattingParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		edits, err := s.RangeFormatting(ctx, p)
		if err != nil {
			return writeErr(lspErrorRequestFailed, err.Error())
		}
		return writeResp(edits)
	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		symbols, err := s.DocumentSymbol(ctx, p)
		if err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return writeResp(symbols)
	case "textDocument/foldingRange":
		var p FoldingRangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		ranges, err := s.FoldingRange(ctx, p)
		if err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return writeResp(ranges)
	case "textDocument/selectionRange":
		var p SelectionRangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		ranges, err := s.SelectionRange(ctx, p)
		if err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return writeResp(ranges)
	default:
		return writeErr(jsonRPCMethodNotFound, "method not found")
	}
}

// Initialize handles the LSP initialize request.
func (s *Server) Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error) {
	_ = ctx
	_ = p
	return InitializeResult{Capabilities: DefaultServerCapabilities()}, nil
}

// Shutdown handles the LSP shutdown request. It is idempotent.
func (s *Server) Shutdown(ctx context.Context) error {
	_ = ctx
	if s == nil {
		return errors.New("nil Server")
	}
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	return nil
}

// Exit handles the LSP exit notification.
func (s *Server) Exit() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.exitRequested = true
	s.mu.Unlock()
}

// DidOpen parses and stores the opened document snapshot.
func (s *Server) DidOpen(ctx context.Context, p DidOpenParams) error {
	store, err := s.requireStore()
	if err != nil {
		return err
	}
	_, err = store.Open(ctx, p.TextDocument.URI, p.TextDocument.Version, []byte(p.TextDocument.Text))
	return err
}

// DidChange applies text changes and stores the reparsed snapshot.
func (s *Server) DidChange(ctx context.Context, p DidChangeParams) error {
	store, err := s.requireStore()
	if err != nil {
		return err
	}
	_, err = store.Change(ctx, p.TextDocument.URI, p.TextDocument.Version, p.ContentChanges)
	return err
}

// DidClose removes the document snapshot if present.
func (s *Server) DidClose(ctx context.Context, p DidCloseParams) error {
	_ = ctx
	store, err := s.requireStore()
	if err != nil {
		return err
	}
	store.Close(p.TextDocument.URI)
	return nil
}

// Formatting is a Track B placeholder that returns a valid empty result in Track A.
func (s *Server) Formatting(ctx context.Context, p DocumentFormattingParams) ([]TextEdit, error) {
	_ = ctx
	_ = p
	return []TextEdit{}, nil
}

// RangeFormatting is a Track B placeholder that returns a valid empty result in Track A.
func (s *Server) RangeFormatting(ctx context.Context, p DocumentRangeFormattingParams) ([]TextEdit, error) {
	_ = ctx
	_ = p
	return []TextEdit{}, nil
}

// DocumentSymbol is a Track C placeholder that returns a valid empty result in Track A.
func (s *Server) DocumentSymbol(ctx context.Context, p DocumentSymbolParams) ([]DocumentSymbol, error) {
	_ = ctx
	_ = p
	return []DocumentSymbol{}, nil
}

// FoldingRange is a Track C placeholder that returns a valid empty result in Track A.
func (s *Server) FoldingRange(ctx context.Context, p FoldingRangeParams) ([]FoldingRange, error) {
	_ = ctx
	_ = p
	return []FoldingRange{}, nil
}

// SelectionRange is a Track C placeholder that returns a valid empty result in Track A.
func (s *Server) SelectionRange(ctx context.Context, p SelectionRangeParams) ([]SelectionRange, error) {
	_ = ctx
	_ = p
	return []SelectionRange{}, nil
}

func (s *Server) writeResponse(w *bufio.Writer, resp Response) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return writeFramedMessage(w, body)
}

func (s *Server) writeErrorResponse(w *bufio.Writer, id json.RawMessage, code int, msg string) error {
	return s.writeResponse(w, Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &ResponseError{Code: code, Message: msg},
	})
}

func (s *Server) requireStore() (*SnapshotStore, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("nil Server")
	}
	return s.store, nil
}

func readFramedMessage(r *bufio.Reader) ([]byte, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header line %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", value)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, errors.New("missing Content-Length")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeFramedMessage(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}
