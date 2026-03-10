package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
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

	workspaceMu       sync.Mutex
	workspace         *index.Manager
	workspaceRoots    []string
	workspaceLintMu   sync.Mutex
	workspaceLintJobs map[string]lintJobState
	workspaceLintWG   sync.WaitGroup

	diagMu      sync.Mutex
	diagnostics map[string]documentDiagnostics
}

type lintJobState struct {
	version    int32
	generation uint64
	cancel     context.CancelFunc
}

type diagnosticBucket string

const (
	diagnosticBucketParser    diagnosticBucket = "parser"
	diagnosticBucketLocal     diagnosticBucket = "local"
	diagnosticBucketWorkspace diagnosticBucket = "workspace"
)

type diagnosticBucketState struct {
	set         bool
	version     int32
	generation  uint64
	diagnostics []Diagnostic
}

type documentDiagnostics struct {
	parser    diagnosticBucketState
	local     diagnosticBucketState
	workspace diagnosticBucketState
}

const defaultLintDebounce = 150 * time.Millisecond

// NewServer creates a new LSP server instance.
func NewServer() *Server {
	return &Server{
		store:             NewSnapshotStore(),
		lint:              lint.NewDefaultRunner(),
		requestCancels:    make(map[string]context.CancelFunc),
		pendingCancelled:  make(map[string]struct{}),
		lintDebounce:      defaultLintDebounce,
		lintJobs:          make(map[string]lintJobState),
		workspaceLintJobs: make(map[string]lintJobState),
		diagnostics:       make(map[string]documentDiagnostics),
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
		s.cancelAllWorkspaceLintJobs()
		s.workspaceLintWG.Wait()
		s.closeWorkspaceManager()
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
	case "workspace/didChangeWorkspaceFolders":
		var p DidChangeWorkspaceFoldersParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidChangeWorkspaceFolders(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		return nil
	case "workspace/didChangeWatchedFiles":
		var p DidChangeWatchedFilesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		if err := s.DidChangeWatchedFiles(ctx, p); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
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
		s.invalidateWorkspaceLintJob(p.TextDocument.URI)
		if err := s.publishDiagnosticsForURI(ctx, p.TextDocument.URI); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.scheduleWorkspaceLintPublishForImpactedURI(p.TextDocument.URI)
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
		s.scheduleWorkspaceLintPublishForImpactedURI(p.TextDocument.URI)
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
		s.invalidateWorkspaceLintJob(p.TextDocument.URI)
		if err := s.publishDiagnosticsForURI(ctx, p.TextDocument.URI); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.scheduleWorkspaceLintPublishForImpactedURI(p.TextDocument.URI)
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
		s.invalidateWorkspaceLintJob(p.TextDocument.URI)
		if err := s.publishClearedDiagnostics(p.TextDocument.URI); err != nil {
			return writeErr(jsonRPCInternalError, err.Error())
		}
		s.scheduleWorkspaceLintPublishForImpactedURI(p.TextDocument.URI)
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
	case "textDocument/definition":
		var p DefinitionParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		locations, err := s.Definition(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(locations)
	case "textDocument/references":
		var p ReferenceParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		locations, err := s.References(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(locations)
	case "textDocument/prepareRename":
		var p PrepareRenameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		result, err := s.PrepareRename(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(result)
	case "textDocument/rename":
		var p RenameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		edit, err := s.Rename(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(edit)
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
	case "workspace/symbol":
		var p WorkspaceSymbolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return writeErr(jsonRPCInvalidParams, err.Error())
		}
		symbols, err := s.WorkspaceSymbols(ctx, p)
		if err != nil {
			return writeErr(lspErrorCodeForQuery(err), err.Error())
		}
		return writeResp(symbols)
	default:
		return writeErr(jsonRPCMethodNotFound, "method not found")
	}
}

// Initialize handles the LSP initialize request.
func (s *Server) Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error) {
	if err := s.configureWorkspaceFolders(ctx, p.WorkspaceFolders); err != nil {
		return InitializeResult{}, err
	}
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
	if _, err := store.Open(ctx, uri, p.TextDocument.Version, []byte(p.TextDocument.Text)); err != nil {
		return err
	}
	return s.syncWorkspaceDocumentWithReason(ctx, uri, index.RebuildReasonOpen)
}

// DidChange applies text changes and stores the reparsed snapshot.
func (s *Server) DidChange(ctx context.Context, p DidChangeParams) error {
	store, uri, err := s.storeForDocumentURI(p.TextDocument.URI)
	if err != nil {
		return err
	}
	if _, err := store.Change(ctx, uri, p.TextDocument.Version, p.ContentChanges); err != nil {
		return err
	}
	return s.syncWorkspaceDocumentWithReason(ctx, uri, index.RebuildReasonChange)
}

// DidSave refreshes the workspace view after a save while preserving the open-document shadow.
func (s *Server) DidSave(ctx context.Context, p DidSaveParams) error {
	manager := s.workspaceManager()
	if manager == nil {
		return nil
	}
	if err := manager.RescanWorkspaceWithReason(ctx, index.RebuildReasonChange); err != nil {
		return err
	}
	return s.syncWorkspaceDocumentWithReason(ctx, p.TextDocument.URI, index.RebuildReasonChange)
}

// DidClose removes the document snapshot if present.
func (s *Server) DidClose(ctx context.Context, p DidCloseParams) error {
	_ = ctx
	store, uri, err := s.storeForDocumentURI(p.TextDocument.URI)
	if err != nil {
		return err
	}
	store.Close(uri)
	if manager := s.workspaceManager(); manager != nil {
		if err := manager.CloseOpenDocumentWithReason(ctx, uri, index.RebuildReasonClose); err != nil {
			return err
		}
	}
	return nil
}

// DidChangeWorkspaceFolders updates the configured workspace roots.
func (s *Server) DidChangeWorkspaceFolders(ctx context.Context, p DidChangeWorkspaceFoldersParams) error {
	current := s.workspaceRootsSnapshot()
	removed, err := workspaceRootsFromFolders(p.Event.Removed)
	if err != nil {
		return err
	}
	added, err := workspaceRootsFromFolders(p.Event.Added)
	if err != nil {
		return err
	}
	next := updatedWorkspaceRoots(current, removed, added)

	if len(next) == 0 {
		s.closeWorkspaceManager()
		return nil
	}
	if err := s.configureWorkspaceManager(ctx, next); err != nil {
		return err
	}
	s.scheduleWorkspaceLintPublishForAllOpenDocuments()
	return nil
}

// DidChangeWatchedFiles refreshes the workspace index from filesystem watcher events.
func (s *Server) DidChangeWatchedFiles(ctx context.Context, p DidChangeWatchedFilesParams) error {
	manager := s.workspaceManager()
	if manager == nil {
		return nil
	}

	for _, change := range p.Changes {
		deleted := change.Type == FileChangeTypeDeleted
		if err := manager.RefreshDocumentWithReason(ctx, change.URI, deleted, index.RebuildReasonWatch); err != nil {
			if err := manager.RescanWorkspaceWithReason(ctx, index.RebuildReasonWatch); err != nil {
				return err
			}
			break
		}
	}
	s.scheduleWorkspaceLintPublishForAllOpenDocuments()
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

func (s *Server) configureWorkspaceFolders(ctx context.Context, folders []WorkspaceFolder) error {
	roots, err := workspaceRootsFromFolders(folders)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return nil
	}
	return s.configureWorkspaceManager(ctx, roots)
}

func (s *Server) configureWorkspaceManager(ctx context.Context, roots []string) error {
	manager := index.NewManager(index.Options{WorkspaceRoots: roots})
	if err := manager.RescanWorkspaceWithReason(ctx, index.RebuildReasonManualRescan); err != nil {
		manager.Close()
		return err
	}

	store, err := s.requireStore()
	if err != nil {
		manager.Close()
		return err
	}
	for _, snap := range store.Snapshots() {
		if err := manager.UpsertOpenDocumentWithReason(ctx, index.DocumentInput{
			URI:        snap.URI,
			Version:    snap.Version,
			Generation: snap.Generation,
			Source:     snap.Bytes(),
		}, index.RebuildReasonOpen); err != nil {
			manager.Close()
			return err
		}
	}

	s.workspaceMu.Lock()
	old := s.workspace
	s.workspace = manager
	s.workspaceRoots = slices.Clone(roots)
	s.workspaceMu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

func (s *Server) workspaceManager() *index.Manager {
	if s == nil {
		return nil
	}
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	return s.workspace
}

func (s *Server) closeWorkspaceManager() {
	if s == nil {
		return
	}
	s.workspaceMu.Lock()
	manager := s.workspace
	s.workspace = nil
	s.workspaceRoots = nil
	s.workspaceMu.Unlock()
	if manager != nil {
		manager.Close()
	}
}

func (s *Server) workspaceRootsSnapshot() []string {
	if s == nil {
		return nil
	}
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	return slices.Clone(s.workspaceRoots)
}

func updatedWorkspaceRoots(current, removed, added []string) []string {
	next := make([]string, 0, len(current)+len(added))
	removedSet := make(map[string]struct{}, len(removed))
	for _, root := range removed {
		removedSet[root] = struct{}{}
	}

	seen := make(map[string]struct{}, len(current)+len(added))
	appendUnique := func(root string) {
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		next = append(next, root)
	}

	for _, root := range current {
		if _, ok := removedSet[root]; ok {
			continue
		}
		appendUnique(root)
	}
	for _, root := range added {
		appendUnique(root)
	}
	return next
}

func (s *Server) ensureWorkspaceManagerForURI(ctx context.Context, uri string) (*index.Manager, error) {
	if manager := s.workspaceManager(); manager != nil {
		return manager, nil
	}

	root, err := documentRootFromURI(uri)
	if err != nil {
		return nil, err
	}
	if !shouldUseImplicitWorkspaceRoot(root) {
		return nil, nil
	}
	if err := s.configureWorkspaceManager(ctx, []string{root}); err != nil {
		return nil, err
	}
	return s.workspaceManager(), nil
}

func (s *Server) syncWorkspaceDocumentWithReason(ctx context.Context, uri string, reason index.RebuildReason) error {
	manager, err := s.ensureWorkspaceManagerForURI(ctx, uri)
	if err != nil || manager == nil {
		return err
	}

	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if snap == nil {
		return nil
	}
	return manager.UpsertOpenDocumentWithReason(ctx, index.DocumentInput{
		URI:        snap.URI,
		Version:    snap.Version,
		Generation: snap.Generation,
		Source:     snap.Bytes(),
	}, reason)
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
	parserDiags, err := lspDiagnosticsFromSyntax(snap.Tree, slices.Clone(snap.Tree.Diagnostics))
	if err != nil {
		return err
	}
	localSyntaxDiags, err := s.collectLocalLintDiagnostics(ctx, snap.Tree)
	if err != nil && !errors.Is(err, context.Canceled) {
		localSyntaxDiags = nil
	}
	localDiags, err := lspDiagnosticsFromSyntax(snap.Tree, localSyntaxDiags)
	if err != nil {
		return err
	}
	return s.replaceDiagnosticBuckets(
		snap.URI,
		snap.Version,
		snap.Generation,
		diagnosticBucketUpdate{bucket: diagnosticBucketParser, diagnostics: parserDiags},
		diagnosticBucketUpdate{bucket: diagnosticBucketLocal, diagnostics: localDiags},
	)
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
	return s.replaceDiagnosticBuckets(
		snap.URI,
		snap.Version,
		snap.Generation,
		diagnosticBucketUpdate{bucket: diagnosticBucketParser, diagnostics: diags},
	)
}

func (s *Server) collectLocalLintDiagnostics(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	if s == nil || s.lint == nil {
		return []syntax.Diagnostic{}, nil
	}

	lintDiags, err := s.lint.Run(ctx, tree)
	if err != nil {
		return nil, err
	}
	lint.SortDiagnostics(lintDiags)
	return lintDiags, nil
}

func (s *Server) collectWorkspaceLintDiagnostics(ctx context.Context, uri string, version int32, generation uint64) ([]Diagnostic, error) {
	if s == nil || s.lint == nil {
		return []Diagnostic{}, nil
	}

	manager := s.workspaceManager()
	if manager == nil {
		return []Diagnostic{}, nil
	}
	workspaceSnapshot, ok := manager.Snapshot()
	if !ok {
		return []Diagnostic{}, nil
	}
	view, ok, err := index.ViewForDocument(workspaceSnapshot, uri)
	if err != nil || !ok {
		return []Diagnostic{}, err
	}
	if view.Document.Version != version || view.Document.Generation != generation {
		return []Diagnostic{}, nil
	}

	snap, err := s.latestSnapshot(uri)
	if err != nil || !snapshotMatchesVersion(snap, version, generation) {
		return []Diagnostic{}, err
	}

	workspaceDiags, err := s.lint.RunWithWorkspace(ctx, view)
	if err != nil {
		return nil, err
	}
	return lspDiagnosticsFromSyntax(snap.Tree, workspaceDiags)
}

func (s *Server) publishClearedDiagnostics(uri string) error {
	uri, err := canonicalDocumentURI(uri)
	if err != nil {
		return err
	}
	s.diagMu.Lock()
	delete(s.diagnostics, uri)
	s.diagMu.Unlock()
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

type diagnosticBucketUpdate struct {
	bucket      diagnosticBucket
	diagnostics []Diagnostic
}

func (s *Server) replaceDiagnosticBuckets(uri string, version int32, generation uint64, updates ...diagnosticBucketUpdate) error {
	snap, err := s.latestSnapshot(uri)
	if err != nil {
		return err
	}
	if !snapshotMatchesVersion(snap, version, generation) {
		return nil
	}

	s.diagMu.Lock()
	state := s.diagnostics[snap.URI]
	changed := false
	for _, update := range updates {
		if sameDiagnosticBucket(state.bucket(update.bucket), version, generation, update.diagnostics) {
			continue
		}
		state.setBucket(update.bucket, diagnosticBucketState{
			set:         true,
			version:     version,
			generation:  generation,
			diagnostics: slices.Clone(update.diagnostics),
		})
		changed = true
	}
	if !changed {
		s.diagMu.Unlock()
		return nil
	}
	s.diagnostics[snap.URI] = state
	merged := mergeDiagnosticBuckets(state, version, generation)
	s.diagMu.Unlock()
	return s.writeVersionedDiagnostics(snap.URI, version, generation, merged)
}

func (d documentDiagnostics) bucket(kind diagnosticBucket) diagnosticBucketState {
	switch kind {
	case diagnosticBucketParser:
		return d.parser
	case diagnosticBucketLocal:
		return d.local
	case diagnosticBucketWorkspace:
		return d.workspace
	default:
		return diagnosticBucketState{}
	}
}

func (d *documentDiagnostics) setBucket(kind diagnosticBucket, state diagnosticBucketState) {
	switch kind {
	case diagnosticBucketParser:
		d.parser = state
	case diagnosticBucketLocal:
		d.local = state
	case diagnosticBucketWorkspace:
		d.workspace = state
	}
}

func sameDiagnosticBucket(state diagnosticBucketState, version int32, generation uint64, diags []Diagnostic) bool {
	if !state.set {
		return false
	}
	return state.version == version && state.generation == generation && slices.Equal(state.diagnostics, diags)
}

func mergeDiagnosticBuckets(state documentDiagnostics, version int32, generation uint64) []Diagnostic {
	out := make([]Diagnostic, 0, len(state.parser.diagnostics)+len(state.local.diagnostics)+len(state.workspace.diagnostics))
	appendBucketDiagnostics(&out, state.parser, version, generation)
	appendBucketDiagnostics(&out, state.local, version, generation)
	appendBucketDiagnostics(&out, state.workspace, version, generation)
	slices.SortFunc(out, compareDiagnostics)
	return out
}

func appendBucketDiagnostics(out *[]Diagnostic, state diagnosticBucketState, version int32, generation uint64) {
	if !state.set || state.version != version || state.generation != generation {
		return
	}
	*out = append(*out, state.diagnostics...)
}

func compareDiagnostics(a, b Diagnostic) int {
	if cmp := comparePosition(a.Range.Start, b.Range.Start); cmp != 0 {
		return cmp
	}
	if cmp := comparePosition(a.Range.End, b.Range.End); cmp != 0 {
		return cmp
	}
	if a.Severity != b.Severity {
		return a.Severity - b.Severity
	}
	if a.Source != b.Source {
		if a.Source < b.Source {
			return -1
		}
		return 1
	}
	if a.Code != b.Code {
		if a.Code < b.Code {
			return -1
		}
		return 1
	}
	if a.Message < b.Message {
		return -1
	}
	if a.Message > b.Message {
		return 1
	}
	return 0
}

func comparePosition(a, b Position) int {
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return a.Character - b.Character
}

func (s *Server) hasDiagnosticBucket(uri string, bucket diagnosticBucket) bool {
	canonicalURI, err := canonicalDocumentURI(uri)
	if err != nil {
		return false
	}
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	return s.diagnostics[canonicalURI].bucket(bucket).set
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

	localSyntaxDiags, err := s.collectLocalLintDiagnostics(ctx, snap.Tree)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	diags, err := lspDiagnosticsFromSyntax(snap.Tree, localSyntaxDiags)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.isLintJobCurrent(uri, version, generation) {
		return nil
	}
	return s.replaceDiagnosticBuckets(
		uri,
		version,
		generation,
		diagnosticBucketUpdate{bucket: diagnosticBucketLocal, diagnostics: diags},
	)
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

func (s *Server) scheduleWorkspaceLintPublishForURI(uri string) {
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

	s.workspaceLintMu.Lock()
	if state := s.workspaceLintJobs[canonicalURI]; state.cancel != nil {
		state.cancel()
	}
	s.workspaceLintJobs[canonicalURI] = lintJobState{version: version, generation: generation, cancel: cancel}
	debounce := s.lintDebounce
	s.workspaceLintWG.Add(1)
	s.workspaceLintMu.Unlock()

	go func() {
		defer s.workspaceLintWG.Done()
		defer s.finishWorkspaceLintJob(canonicalURI, version, generation)

		timer := time.NewTimer(debounce)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if !s.isWorkspaceLintJobCurrent(canonicalURI, version, generation) {
			return
		}
		_ = s.publishWorkspaceLintDiagnostics(ctx, canonicalURI, version, generation)
	}()
}

func (s *Server) scheduleWorkspaceLintPublishForImpactedURI(uri string) {
	s.scheduleWorkspaceLintPublishForURI(uri)

	manager := s.workspaceManager()
	if manager == nil {
		return
	}
	workspaceSnapshot, ok := manager.Snapshot()
	if !ok {
		return
	}
	_, key, err := index.CanonicalizeDocumentURI(uri)
	if err != nil {
		return
	}
	impacted := make(map[index.DocumentKey]struct{}, len(workspaceSnapshot.ReverseDeps[key])+1)
	impacted[key] = struct{}{}
	for _, dep := range workspaceSnapshot.ReverseDeps[key] {
		impacted[dep] = struct{}{}
	}

	store, err := s.requireStore()
	if err != nil {
		return
	}
	for _, snap := range store.Snapshots() {
		_, snapKey, err := index.CanonicalizeDocumentURI(snap.URI)
		if err != nil {
			continue
		}
		if _, ok := impacted[snapKey]; !ok {
			continue
		}
		if snap.URI == uri {
			continue
		}
		s.scheduleWorkspaceLintPublishForURI(snap.URI)
	}
}

func (s *Server) scheduleWorkspaceLintPublishForAllOpenDocuments() {
	store, err := s.requireStore()
	if err != nil {
		return
	}
	for _, snap := range store.Snapshots() {
		s.scheduleWorkspaceLintPublishForURI(snap.URI)
	}
}

func (s *Server) publishWorkspaceLintDiagnostics(ctx context.Context, uri string, version int32, generation uint64) error {
	if !s.isWorkspaceLintJobCurrent(uri, version, generation) {
		return nil
	}

	diags, err := s.collectWorkspaceLintDiagnostics(ctx, uri, version, generation)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.isWorkspaceLintJobCurrent(uri, version, generation) {
		return nil
	}
	if len(diags) == 0 && !s.hasDiagnosticBucket(uri, diagnosticBucketWorkspace) {
		return nil
	}
	return s.replaceDiagnosticBuckets(
		uri,
		version,
		generation,
		diagnosticBucketUpdate{bucket: diagnosticBucketWorkspace, diagnostics: diags},
	)
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

func (s *Server) invalidateWorkspaceLintJob(uri string) {
	if s == nil || uri == "" {
		return
	}
	canonicalURI, err := canonicalDocumentURI(uri)
	if err != nil {
		return
	}

	s.workspaceLintMu.Lock()
	defer s.workspaceLintMu.Unlock()

	if state := s.workspaceLintJobs[canonicalURI]; state.cancel != nil {
		state.cancel()
	}
	delete(s.workspaceLintJobs, canonicalURI)
}

func (s *Server) finishWorkspaceLintJob(uri string, version int32, generation uint64) {
	if s == nil || uri == "" {
		return
	}

	s.workspaceLintMu.Lock()
	defer s.workspaceLintMu.Unlock()

	state, ok := s.workspaceLintJobs[uri]
	if !ok || state.version != version || state.generation != generation {
		return
	}
	state.cancel = nil
	s.workspaceLintJobs[uri] = state
}

func (s *Server) isWorkspaceLintJobCurrent(uri string, version int32, generation uint64) bool {
	if s == nil || uri == "" {
		return false
	}

	s.workspaceLintMu.Lock()
	defer s.workspaceLintMu.Unlock()

	state, ok := s.workspaceLintJobs[uri]
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

func workspaceRootsFromFolders(folders []WorkspaceFolder) ([]string, error) {
	roots := make([]string, 0, len(folders))
	seen := make(map[string]struct{}, len(folders))
	for _, folder := range folders {
		root, err := documentRootFromURI(folder.URI)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots, nil
}

func documentRootFromURI(raw string) (string, error) {
	path, err := filePathFromDocumentURI(raw)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func shouldUseImplicitWorkspaceRoot(root string) bool {
	root = filepath.Clean(root)
	return root != filepath.Dir(root)
}

func filePathFromDocumentURI(raw string) (string, error) {
	displayURI, err := canonicalDocumentURI(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(displayURI)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", u.Scheme)
	}
	return filepath.Clean(filepath.FromSlash(u.Path)), nil
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

func (s *Server) cancelAllWorkspaceLintJobs() {
	if s == nil {
		return
	}

	s.workspaceLintMu.Lock()
	defer s.workspaceLintMu.Unlock()

	for _, state := range s.workspaceLintJobs {
		if state.cancel != nil {
			state.cancel()
		}
	}
	clear(s.workspaceLintJobs)
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
	switch {
	case errors.Is(err, context.Canceled):
		return lspErrorRequestCancelled
	case errors.Is(err, ErrDocumentNotOpen):
		return jsonRPCInvalidParams
	case errors.Is(err, index.ErrContentModified):
		return lspErrorContentModified
	case errors.Is(err, index.ErrWorkspaceClosed), errors.Is(err, index.ErrRenameBlocked):
		return lspErrorRequestFailed
	default:
		return jsonRPCInternalError
	}
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
