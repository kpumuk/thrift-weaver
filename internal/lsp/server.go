package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	fmtengine "github.com/kpumuk/thrift-weaver/internal/format"
	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/lint"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

// Server is a thrift LSP server with an in-memory snapshot store.
type Server struct {
	store *SnapshotStore
	lint  *lint.Runner

	mu            sync.Mutex
	shutdown      bool
	exitRequested bool
	output        io.Writer
	runCtx        context.Context

	writeMu sync.Mutex

	reqMu            sync.Mutex
	requestCancels   map[string]context.CancelFunc
	pendingCancelled map[string]struct{}

	lintMu       sync.Mutex
	lintDebounce time.Duration
	lintJobs     map[string]lintJobState
	lintWG       sync.WaitGroup
}

type lintJobState struct {
	version    int32
	generation uint64
	cancel     context.CancelFunc
}

const defaultLintDebounce = 150 * time.Millisecond

// NewServer creates a new LSP server instance.
func NewServer() *Server {
	return &Server{
		store:            NewSnapshotStore(),
		lint:             lint.NewDefaultRunner(),
		requestCancels:   make(map[string]context.CancelFunc),
		pendingCancelled: make(map[string]struct{}),
		lintDebounce:     defaultLintDebounce,
		lintJobs:         make(map[string]lintJobState),
	}
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
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.attachRuntime(runCtx, out)
	defer func() {
		s.cancelAllLintJobs()
		s.lintWG.Wait()
		s.detachRuntime()
	}()

	br := bufio.NewReader(in)

	for {
		if err := runCtx.Err(); err != nil {
			return err
		}
		body, err := readFramedMessage(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = s.writeErrorResponse(nil, jsonRPCParseError, err.Error())
			continue
		}
		if len(body) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			_ = s.writeErrorResponse(nil, jsonRPCParseError, err.Error())
			continue
		}
		if req.JSONRPC != "" && req.JSONRPC != JSONRPCVersion {
			_ = s.writeErrorResponse(req.ID, jsonRPCInvalidRequest, "unsupported jsonrpc version")
			continue
		}
		if req.Method == "" {
			// Ignore client responses/unknown envelopes in v1.
			continue
		}

		if err := s.dispatch(runCtx, req); err != nil {
			if errors.Is(err, ErrShutdownRequested) {
				return nil
			}
			return err
		}
	}
}

//nolint:funcorder // dispatch is kept near Run for readability of request flow.
func (s *Server) dispatch(ctx context.Context, req Request) error {
	isRequest := len(req.ID) != 0
	if isRequest {
		var cancel context.CancelFunc
		ctx, cancel = s.beginRequestContext(ctx, req.ID)
		defer cancel()
		defer s.endRequestContext(req.ID)
	}

	writeResp := func(result any) error {
		if !isRequest {
			return nil
		}
		return s.writeResponse(Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: result})
	}
	writeErr := func(code int, msg string) error {
		if !isRequest {
			return nil
		}
		return s.writeErrorResponse(req.ID, code, msg)
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
	case "$/cancelRequest":
		var p CancelParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		s.cancelRequest(p)
		return nil
	case "textDocument/didOpen":
		var p DidOpenParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidOpen(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.invalidateLintJob(p.TextDocument.URI)
		if err := s.publishDiagnosticsForURI(ctx, p.TextDocument.URI); err != nil {
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
		if err := s.publishSyntaxDiagnosticsForURI(p.TextDocument.URI); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.scheduleLintPublishForURI(p.TextDocument.URI)
		return nil
	case "textDocument/didSave":
		var p DidSaveParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidSave(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.invalidateLintJob(p.TextDocument.URI)
		if err := s.publishDiagnosticsForURI(ctx, p.TextDocument.URI); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
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
		s.invalidateLintJob(p.TextDocument.URI)
		if err := s.publishClearedDiagnostics(p.TextDocument.URI); err != nil {
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
			return writeErr(lspErrorCodeForFormatting(err), err.Error())
		}
		return writeResp(edits)
	case "textDocument/rangeFormatting":
		var p DocumentRangeFormattingParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		edits, err := s.RangeFormatting(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForFormatting(err), err.Error())
		}
		return writeResp(edits)
	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		symbols, err := s.DocumentSymbol(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(symbols)
	case "textDocument/foldingRange":
		var p FoldingRangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		ranges, err := s.FoldingRange(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(ranges)
	case "textDocument/selectionRange":
		var p SelectionRangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		ranges, err := s.SelectionRange(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(ranges)
	case "textDocument/semanticTokens/full":
		var p SemanticTokensParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		tokens, err := s.SemanticTokensFull(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(tokens)
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
	store, uri, err := s.storeForDocumentURI(p.TextDocument.URI)
	if err != nil {
		return err
	}
	_, err = store.Open(ctx, uri, p.TextDocument.Version, []byte(p.TextDocument.Text))
	return err
}

// DidChange applies text changes and stores the reparsed snapshot.
func (s *Server) DidChange(ctx context.Context, p DidChangeParams) error {
	store, uri, err := s.storeForDocumentURI(p.TextDocument.URI)
	if err != nil {
		return err
	}
	_, err = store.Change(ctx, uri, p.TextDocument.Version, p.ContentChanges)
	return err
}

// DidSave handles didSave notifications. v1 performs no extra state mutation.
func (s *Server) DidSave(ctx context.Context, p DidSaveParams) error {
	_ = ctx
	_ = p
	return nil
}

// DidClose removes the document snapshot if present.
func (s *Server) DidClose(ctx context.Context, p DidCloseParams) error {
	_ = ctx
	store, uri, err := s.storeForDocumentURI(p.TextDocument.URI)
	if err != nil {
		return err
	}
	store.Close(uri)
	return nil
}

// Formatting handles textDocument/formatting.
func (s *Server) Formatting(ctx context.Context, p DocumentFormattingParams) ([]TextEdit, error) {
	snap, err := s.formattingSnapshot(p.TextDocument.URI, p.Version)
	if err != nil {
		return nil, err
	}
	res, err := fmtengine.Document(ctx, snap.Tree, formattingOptionsFromLSP(p.Options))
	if err != nil {
		return nil, err
	}
	if !res.Changed {
		return []TextEdit{}, nil
	}
	fullRange, err := fullDocumentRange(snap.Tree.LineIndex)
	if err != nil {
		return nil, err
	}
	return []TextEdit{{
		Range:   fullRange,
		NewText: string(res.Output),
	}}, nil
}

// RangeFormatting handles textDocument/rangeFormatting.
func (s *Server) RangeFormatting(ctx context.Context, p DocumentRangeFormattingParams) ([]TextEdit, error) {
	snap, err := s.formattingSnapshot(p.TextDocument.URI, p.Version)
	if err != nil {
		return nil, err
	}
	byteRange, err := byteSpanFromLSPRange(snap.Tree.LineIndex, p.Range)
	if err != nil {
		return nil, err
	}
	res, err := fmtengine.Range(ctx, snap.Tree, byteRange, formattingOptionsFromLSP(p.Options))
	if err != nil {
		return nil, err
	}
	return lspTextEditsFromByteEdits(snap.Tree.LineIndex, res.Edits)
}

func (s *Server) writeResponse(resp Response) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return s.writeMessage(body)
}

func (s *Server) writeErrorResponse(id json.RawMessage, code int, msg string) error {
	return s.writeResponse(Response{
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

func (s *Server) storeForDocumentURI(raw string) (*SnapshotStore, string, error) {
	store, err := s.requireStore()
	if err != nil {
		return nil, "", err
	}
	uri, err := canonicalDocumentURI(raw)
	if err != nil {
		return nil, "", err
	}
	return store, uri, nil
}

func (s *Server) latestSnapshot(uri string) (*Snapshot, error) {
	store, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	snap, _ := store.Snapshot(uri)
	return snap, nil
}

func (s *Server) formattingSnapshot(uri string, version *int32) (*Snapshot, error) {
	store, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	if version != nil {
		return store.SnapshotAtVersion(uri, *version)
	}
	snap, ok := store.Snapshot(uri)
	if !ok {
		return nil, ErrDocumentNotOpen
	}
	return snap, nil
}

func (s *Server) publishDiagnosticsForURI(ctx context.Context, uri string) error {
	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if snap == nil {
		return nil
	}
	diags, err := s.collectLSPDiagnostics(ctx, snap.Tree)
	if err != nil {
		return err
	}
	return s.writeVersionedDiagnostics(snap.URI, snap.Version, snap.Generation, diags)
}

func (s *Server) publishSyntaxDiagnosticsForURI(uri string) error {
	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if snap == nil {
		return nil
	}

	diags, err := lspDiagnosticsFromSyntax(snap.Tree, slices.Clone(snap.Tree.Diagnostics))
	if err != nil {
		return err
	}
	return s.writeVersionedDiagnostics(snap.URI, snap.Version, snap.Generation, diags)
}

func (s *Server) collectLSPDiagnostics(ctx context.Context, tree *syntax.Tree) ([]Diagnostic, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	combined := slices.Clone(tree.Diagnostics)
	if s == nil || s.lint == nil {
		return lspDiagnosticsFromSyntax(tree, combined)
	}

	lintDiags, err := s.lint.Run(ctx, tree)
	if err != nil {
		return lspDiagnosticsFromSyntax(tree, combined)
	}
	combined = append(combined, lintDiags...)

	lint.SortDiagnostics(combined)
	return lspDiagnosticsFromSyntax(tree, combined)
}

func (s *Server) publishClearedDiagnostics(uri string) error {
	uri, err := canonicalDocumentURI(uri)
	if err != nil {
		return err
	}
	return s.writePublishDiagnostics(PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []Diagnostic{},
	})
}

func (s *Server) writePublishDiagnostics(params PublishDiagnosticsParams) error {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/publishDiagnostics",
		Params:  params,
	})
	if err != nil {
		return err
	}
	return s.writeMessage(body)
}

func (s *Server) writeVersionedDiagnostics(uri string, version int32, generation uint64, diags []Diagnostic) error {
	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if !snapshotMatchesVersion(snap, version, generation) {
		return nil
	}

	return s.writePublishDiagnostics(PublishDiagnosticsParams{
		URI:         snap.URI,
		Version:     &version,
		Diagnostics: diags,
	})
}

func (s *Server) writeMessage(body []byte) error {
	if s == nil {
		return errors.New("nil Server")
	}
	s.mu.Lock()
	out := s.output
	s.mu.Unlock()
	if out == nil {
		return errors.New("lsp output is not attached")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeFramedMessage(out, body)
}

func (s *Server) attachRuntime(ctx context.Context, out io.Writer) {
	s.mu.Lock()
	s.runCtx = ctx
	s.output = out
	s.mu.Unlock()
}

func (s *Server) detachRuntime() {
	s.mu.Lock()
	s.runCtx = nil
	s.output = nil
	s.mu.Unlock()
}

func (s *Server) runtimeContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runCtx
}

func (s *Server) scheduleLintPublishForURI(uri string) {
	if s == nil || s.lint == nil || uri == "" {
		return
	}
	canonicalURI, err := canonicalDocumentURI(uri)
	if err != nil {
		return
	}

	snap, err := s.latestSnapshot(canonicalURI)
	if err != nil || snap == nil {
		return
	}
	version := snap.Version
	generation := snap.Generation

	runCtx := s.runtimeContext()
	if runCtx == nil {
		return
	}

	ctx, cancel := context.WithCancel(runCtx)

	s.lintMu.Lock()
	if state := s.lintJobs[canonicalURI]; state.cancel != nil {
		state.cancel()
	}
	s.lintJobs[canonicalURI] = lintJobState{version: version, generation: generation, cancel: cancel}
	debounce := s.lintDebounce
	s.lintWG.Add(1)
	s.lintMu.Unlock()

	go func() {
		defer s.lintWG.Done()
		defer s.finishLintJob(canonicalURI, version, generation)

		timer := time.NewTimer(debounce)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if !s.isLintJobCurrent(canonicalURI, version, generation) {
			return
		}
		_ = s.publishDebouncedLintDiagnostics(ctx, canonicalURI, version, generation)
	}()
}

func (s *Server) publishDebouncedLintDiagnostics(ctx context.Context, uri string, version int32, generation uint64) error {
	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if !snapshotMatchesVersion(snap, version, generation) {
		return nil
	}

	diags, err := s.collectLSPDiagnostics(ctx, snap.Tree)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.isLintJobCurrent(uri, version, generation) {
		return nil
	}
	return s.writeVersionedDiagnostics(uri, version, generation, diags)
}

func (s *Server) invalidateLintJob(uri string) {
	if s == nil || uri == "" {
		return
	}
	canonicalURI, err := canonicalDocumentURI(uri)
	if err != nil {
		return
	}

	s.lintMu.Lock()
	defer s.lintMu.Unlock()

	if state := s.lintJobs[canonicalURI]; state.cancel != nil {
		state.cancel()
	}
	delete(s.lintJobs, canonicalURI)
}

func (s *Server) finishLintJob(uri string, version int32, generation uint64) {
	if s == nil || uri == "" {
		return
	}

	s.lintMu.Lock()
	defer s.lintMu.Unlock()

	state, ok := s.lintJobs[uri]
	if !ok || state.version != version || state.generation != generation {
		return
	}
	state.cancel = nil
	s.lintJobs[uri] = state
}

func (s *Server) isLintJobCurrent(uri string, version int32, generation uint64) bool {
	if s == nil || uri == "" {
		return false
	}

	s.lintMu.Lock()
	defer s.lintMu.Unlock()

	state, ok := s.lintJobs[uri]
	return ok && state.version == version && state.generation == generation
}

func snapshotMatchesVersion(snap *Snapshot, version int32, generation uint64) bool {
	return snap != nil && snap.Version == version && snap.Generation == generation
}

func canonicalDocumentURI(raw string) (string, error) {
	displayURI, _, err := index.CanonicalizeDocumentURI(raw)
	if err != nil {
		return "", err
	}
	return displayURI, nil
}

func (s *Server) cancelAllLintJobs() {
	if s == nil {
		return
	}

	s.lintMu.Lock()
	defer s.lintMu.Unlock()

	for _, state := range s.lintJobs {
		if state.cancel != nil {
			state.cancel()
		}
	}
	clear(s.lintJobs)
}

func (s *Server) setLintDebounceForTesting(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.lintMu.Lock()
	s.lintDebounce = d
	s.lintMu.Unlock()
}

// cancelRequest records or triggers cancellation for a request id.
//
// v1 note: the server processes messages sequentially, so $/cancelRequest can only
// cancel a request before dispatch begins (or future concurrent handlers). This is
// still useful for robustness tests and keeps cancellation non-fatal.
func (s *Server) cancelRequest(p CancelParams) {
	if s == nil {
		return
	}
	key := requestIDKey(p.ID)
	if key == "" {
		return
	}
	s.reqMu.Lock()
	cancel := s.requestCancels[key]
	if cancel != nil {
		delete(s.requestCancels, key)
	}
	s.pendingCancelled[key] = struct{}{}
	s.reqMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) beginRequestContext(parent context.Context, id json.RawMessage) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	key := requestIDKey(id)
	if s == nil || key == "" {
		return context.WithCancel(parent)
	}
	ctx, cancel := context.WithCancel(parent)
	s.reqMu.Lock()
	s.requestCancels[key] = cancel
	if _, ok := s.pendingCancelled[key]; ok {
		delete(s.pendingCancelled, key)
		cancel()
	}
	s.reqMu.Unlock()
	return ctx, cancel
}

func (s *Server) endRequestContext(id json.RawMessage) {
	if s == nil {
		return
	}
	key := requestIDKey(id)
	if key == "" {
		return
	}
	s.reqMu.Lock()
	delete(s.requestCancels, key)
	delete(s.pendingCancelled, key)
	s.reqMu.Unlock()
}

func requestIDKey(id json.RawMessage) string {
	if len(id) == 0 {
		return ""
	}
	return string(id)
}

func formattingOptionsFromLSP(in FormattingOptions) fmtengine.Options {
	opts := fmtengine.Options{}
	if in.InsertSpaces && in.TabSize > 0 {
		opts.Indent = strings.Repeat(" ", in.TabSize)
	}
	return opts
}

func fullDocumentRange(li *itext.LineIndex) (Range, error) {
	if li == nil {
		return Range{}, errors.New("nil line index")
	}
	end, err := li.OffsetToUTF16Position(li.SourceLen())
	if err != nil {
		return Range{}, err
	}
	return Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: end.Line, Character: end.Character},
	}, nil
}

func byteSpanFromLSPRange(li *itext.LineIndex, r Range) (itext.Span, error) {
	if li == nil {
		return itext.Span{}, errors.New("nil line index")
	}
	start, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: r.Start.Line, Character: r.Start.Character})
	if err != nil {
		return itext.Span{}, fmt.Errorf("range start: %w", err)
	}
	end, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: r.End.Line, Character: r.End.Character})
	if err != nil {
		return itext.Span{}, fmt.Errorf("range end: %w", err)
	}
	return itext.NewSpan(start, end)
}

func lspTextEditsFromByteEdits(li *itext.LineIndex, edits []itext.ByteEdit) ([]TextEdit, error) {
	if len(edits) == 0 {
		return []TextEdit{}, nil
	}
	out := make([]TextEdit, 0, len(edits))
	for _, e := range edits {
		rng, err := lspRangeFromSpan(li, e.Span)
		if err != nil {
			return nil, err
		}
		out = append(out, TextEdit{
			Range:   rng,
			NewText: string(e.NewText),
		})
	}
	return out, nil
}

func lspDiagnosticsFromSyntax(tree *syntax.Tree, diagnostics []syntax.Diagnostic) ([]Diagnostic, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	li := tree.LineIndex
	if li == nil {
		li = itext.NewLineIndex(tree.Source)
	}
	out := make([]Diagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		rng, err := lspRangeFromSpan(li, d.Span)
		if err != nil {
			return nil, err
		}
		out = append(out, Diagnostic{
			Range:    rng,
			Severity: lspSeverity(d.Severity),
			Code:     string(d.Code),
			Source:   d.Source,
			Message:  d.Message,
		})
	}
	return out, nil
}

func lspRangeFromSpan(li *itext.LineIndex, sp itext.Span) (Range, error) {
	if li == nil {
		return Range{}, errors.New("nil line index")
	}
	clamped := clampSpanToSource(sp, li.SourceLen())
	start, err := li.OffsetToUTF16Position(clamped.Start)
	if err != nil {
		return Range{}, err
	}
	end, err := li.OffsetToUTF16Position(clamped.End)
	if err != nil {
		return Range{}, err
	}
	return Range{
		Start: Position{Line: start.Line, Character: start.Character},
		End:   Position{Line: end.Line, Character: end.Character},
	}, nil
}

func clampSpanToSource(sp itext.Span, srcLen itext.ByteOffset) itext.Span {
	if !sp.Start.IsValid() {
		sp.Start = 0
	}
	if !sp.End.IsValid() {
		sp.End = sp.Start
	}
	if sp.Start > srcLen {
		sp.Start = srcLen
	}
	if sp.End > srcLen {
		sp.End = srcLen
	}
	if sp.End < sp.Start {
		sp.End = sp.Start
	}
	return sp
}

func lspSeverity(sev syntax.Severity) int {
	switch sev {
	case syntax.SeverityError:
		return 1
	case syntax.SeverityWarning:
		return 2
	case syntax.SeverityInfo:
		return 3
	default:
		return 1
	}
}

func lspErrorCodeForFormatting(err error) int {
	switch {
	case errors.Is(err, ErrStaleVersion):
		return lspErrorContentModified
	case errors.Is(err, ErrDocumentNotOpen):
		return jsonRPCInvalidParams
	case errors.Is(err, context.Canceled):
		return lspErrorRequestCancelled
	case fmtengine.IsErrUnsafeToFormat(err):
		return lspErrorRequestFailed
	default:
		return jsonRPCInternalError
	}
}

func lspErrorCodeForQuery(err error) int {
	if errors.Is(err, context.Canceled) {
		return lspErrorRequestCancelled
	}
	return jsonRPCInternalError
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
