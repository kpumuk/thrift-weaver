package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

func TestInitializeAdvertisesV1Capabilities(t *testing.T) {
	t.Parallel()

	s := NewServer()
	res, err := s.Initialize(context.Background(), InitializeParams{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got := res.Capabilities
	if !got.TextDocumentSync.OpenClose || got.TextDocumentSync.Change != TextDocumentSyncKindIncremental {
		t.Fatalf("unexpected textDocumentSync: %+v", got.TextDocumentSync)
	}
	if got.TextDocumentSync.Save == nil || got.TextDocumentSync.Save.IncludeText {
		t.Fatalf("unexpected didSave sync options: %+v", got.TextDocumentSync.Save)
	}
	if !got.DefinitionProvider || !got.ReferencesProvider || !got.RenameProvider || !got.WorkspaceSymbolProvider {
		t.Fatalf("navigation capabilities not advertised: %+v", got)
	}
	if !got.DocumentFormattingProvider || !got.DocumentRangeFormattingProvider || !got.DocumentSymbolProvider || !got.FoldingRangeProvider || !got.SelectionRangeProvider {
		t.Fatalf("unexpected capabilities: %+v", got)
	}
	if got.SemanticTokensProvider == nil || !got.SemanticTokensProvider.Full {
		t.Fatalf("semanticTokensProvider not advertised correctly: %+v", got.SemanticTokensProvider)
	}
}

func TestServerRunInitializeShutdownExit(t *testing.T) {
	t.Parallel()

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{}`)})
	writeReqFrame(t, &in, Request{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`2`), Method: "shutdown"})
	writeReqFrame(t, &in, Request{JSONRPC: JSONRPCVersion, Method: "exit"})

	var out bytes.Buffer
	s := NewServer()
	if err := s.Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	br := bufio.NewReader(bytes.NewReader(out.Bytes()))
	resp1 := readRespFrame(t, br)
	resp2 := readRespFrame(t, br)
	if _, err := readFramedMessage(br); err == nil {
		t.Fatal("expected exactly two responses")
	}
	if resp1.Error != nil || string(resp1.ID) != "1" {
		t.Fatalf("unexpected initialize response: %+v", resp1)
	}
	var initRes InitializeResult
	marshalRoundtrip(t, resp1.Result, &initRes)
	if initRes.Capabilities.TextDocumentSync.Change != TextDocumentSyncKindIncremental {
		t.Fatalf("unexpected initialize capabilities: %+v", initRes.Capabilities)
	}
	if resp2.Error != nil || string(resp2.ID) != "2" {
		t.Fatalf("unexpected shutdown response: %+v", resp2)
	}
}

func TestServerUnknownMethodReturnsMethodNotFound(t *testing.T) {
	t.Parallel()

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`99`), Method: "thrift/unknown"})
	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	resp := readRespFrame(t, bufio.NewReader(bytes.NewReader(out.Bytes())))
	if resp.Error == nil || resp.Error.Code != jsonRPCMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", resp)
	}
}

func TestServerRunPublishesDiagnosticsOnOpenChangeClose(t *testing.T) {
	t.Parallel()

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///diag.thrift",
				Version: 1,
				Text:    "struct S {\n  1: string a\n",
			},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///diag.thrift", Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  1: string a\n}\n",
			}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didClose",
		Params:  mustJSON(t, DidCloseParams{TextDocument: TextDocumentIdentifier{URI: "file:///diag.thrift"}}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())
	notifications := collectPublishDiagnosticsMessages(t, msgs)
	if len(notifications) != 3 {
		t.Fatalf("publishDiagnostics count=%d, want 3", len(notifications))
	}

	var openDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[0].Params, &openDiag)
	if openDiag.Version == nil || *openDiag.Version != 1 {
		t.Fatalf("open diagnostics version=%v, want 1", openDiag.Version)
	}
	if len(openDiag.Diagnostics) == 0 {
		t.Fatal("expected diagnostics for invalid open document")
	}

	var changeDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[1].Params, &changeDiag)
	if changeDiag.Version == nil || *changeDiag.Version != 2 {
		t.Fatalf("change diagnostics version=%v, want 2", changeDiag.Version)
	}
	if len(changeDiag.Diagnostics) != 0 {
		t.Fatalf("expected diagnostics cleared after valid change, got %d", len(changeDiag.Diagnostics))
	}

	var closeDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[2].Params, &closeDiag)
	if closeDiag.Version != nil {
		t.Fatalf("close diagnostics version=%v, want nil", closeDiag.Version)
	}
	if len(closeDiag.Diagnostics) != 0 {
		t.Fatalf("expected empty diagnostics on close, got %d", len(closeDiag.Diagnostics))
	}
}

func TestServerRunPublishesLintDiagnosticsOnOpenChangeSave(t *testing.T) {
	t.Parallel()

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///lint.thrift",
				Version: 1,
				Text:    "struct S {\n  string name,\n}\n",
			},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///lint.thrift", Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  1: string name xsd_optional,\n  1: string name,\n}\n",
			}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didSave",
		Params: mustJSON(t, DidSaveParams{
			TextDocument: TextDocumentIdentifier{URI: "file:///lint.thrift"},
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())
	notifications := collectPublishDiagnosticsMessages(t, msgs)
	if len(notifications) != 3 {
		t.Fatalf("publishDiagnostics count=%d, want 3", len(notifications))
	}

	var openDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[0].Params, &openDiag)
	if openDiag.Version == nil || *openDiag.Version != 1 {
		t.Fatalf("open diagnostics version=%v, want 1", openDiag.Version)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_FIELD_ID_REQUIRED") {
		t.Fatalf("expected LINT_FIELD_ID_REQUIRED in open diagnostics: %+v", openDiag.Diagnostics)
	}

	var changeDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[1].Params, &changeDiag)
	if changeDiag.Version == nil || *changeDiag.Version != 2 {
		t.Fatalf("change diagnostics version=%v, want 2", changeDiag.Version)
	}
	if len(changeDiag.Diagnostics) != 0 {
		t.Fatalf("didChange should publish syntax-only diagnostics before debounce, got %+v", changeDiag.Diagnostics)
	}

	var saveDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[2].Params, &saveDiag)
	if saveDiag.Version == nil || *saveDiag.Version != 2 {
		t.Fatalf("save diagnostics version=%v, want 2", saveDiag.Version)
	}
	if !containsDiagnosticCode(saveDiag.Diagnostics, "LINT_DEPRECATED_FIELD_XSD_OPTIONAL") {
		t.Fatalf("didSave diagnostics missing LINT_DEPRECATED_FIELD_XSD_OPTIONAL: %+v", saveDiag.Diagnostics)
	}
	if !containsDiagnosticCode(saveDiag.Diagnostics, "LINT_FIELD_ID_DUPLICATE") {
		t.Fatalf("didSave diagnostics missing LINT_FIELD_ID_DUPLICATE: %+v", saveDiag.Diagnostics)
	}
	if !containsDiagnosticCode(saveDiag.Diagnostics, "LINT_FIELD_NAME_DUPLICATE") {
		t.Fatalf("didSave diagnostics missing LINT_FIELD_NAME_DUPLICATE: %+v", saveDiag.Diagnostics)
	}
}

func TestServerRunPublishesSemanticLintDiagnosticsOnOpen(t *testing.T) {
	t.Parallel()

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///semantic.thrift",
				Version: 1,
				Text: "typedef Missing Alias\n" +
					"service API extends MissingService {\n" +
					"  oneway i32 ping(1: Missing req) throws (1: string msg),\n" +
					"}\n",
			},
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	notifications := collectPublishDiagnosticsMessages(t, readAllFrames(t, out.Bytes()))
	if len(notifications) != 1 {
		t.Fatalf("publishDiagnostics count=%d, want 1", len(notifications))
	}

	var openDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[0].Params, &openDiag)
	if openDiag.Version == nil || *openDiag.Version != 1 {
		t.Fatalf("open diagnostics version=%v, want 1", openDiag.Version)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_TYPEDEF_UNKNOWN_BASE") {
		t.Fatalf("expected LINT_TYPEDEF_UNKNOWN_BASE in open diagnostics: %+v", openDiag.Diagnostics)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_TYPE_UNKNOWN") {
		t.Fatalf("expected LINT_TYPE_UNKNOWN in open diagnostics: %+v", openDiag.Diagnostics)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_SERVICE_ONEWAY_RETURN_NOT_VOID") {
		t.Fatalf("expected LINT_SERVICE_ONEWAY_RETURN_NOT_VOID in open diagnostics: %+v", openDiag.Diagnostics)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_SERVICE_ONEWAY_HAS_THROWS") {
		t.Fatalf("expected LINT_SERVICE_ONEWAY_HAS_THROWS in open diagnostics: %+v", openDiag.Diagnostics)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_SERVICE_EXTENDS_UNKNOWN") {
		t.Fatalf("expected LINT_SERVICE_EXTENDS_UNKNOWN in open diagnostics: %+v", openDiag.Diagnostics)
	}
	if !containsDiagnosticCode(openDiag.Diagnostics, "LINT_SERVICE_THROWS_NOT_EXCEPTION") {
		t.Fatalf("expected LINT_SERVICE_THROWS_NOT_EXCEPTION in open diagnostics: %+v", openDiag.Diagnostics)
	}
}

func TestServerRunPublishesDebouncedLintDiagnosticsAfterDidChange(t *testing.T) {
	t.Parallel()

	s := NewServer()
	s.setLintDebounceForTesting(10 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///debounced.thrift",
				Version: 1,
				Text:    "struct S {\n  1: string name,\n}\n",
			},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///debounced.thrift", Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  1: string name xsd_optional,\n}\n",
			}},
		}),
	})

	time.Sleep(60 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	notifications := collectPublishDiagnosticsMessages(t, readAllFrames(t, out.Bytes()))
	if len(notifications) != 3 {
		t.Fatalf("publishDiagnostics count=%d, want 3", len(notifications))
	}

	var changeDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[1].Params, &changeDiag)
	if changeDiag.Version == nil || *changeDiag.Version != 2 {
		t.Fatalf("change diagnostics version=%v, want 2", changeDiag.Version)
	}
	if len(changeDiag.Diagnostics) != 0 {
		t.Fatalf("didChange should publish syntax-only diagnostics before debounce, got %+v", changeDiag.Diagnostics)
	}

	var lintDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[2].Params, &lintDiag)
	if lintDiag.Version == nil || *lintDiag.Version != 2 {
		t.Fatalf("lint diagnostics version=%v, want 2", lintDiag.Version)
	}
	if !containsDiagnosticCode(lintDiag.Diagnostics, "LINT_DEPRECATED_FIELD_XSD_OPTIONAL") {
		t.Fatalf("debounced lint diagnostics missing LINT_DEPRECATED_FIELD_XSD_OPTIONAL: %+v", lintDiag.Diagnostics)
	}
}

func TestServerRunSuppressesStaleDebouncedLintDiagnostics(t *testing.T) {
	t.Parallel()

	s := NewServer()
	s.setLintDebounceForTesting(40 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///stale.thrift",
				Version: 1,
				Text:    "struct S {\n  1: string name,\n}\n",
			},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///stale.thrift", Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  1: string name xsd_optional,\n}\n",
			}},
		}),
	})

	time.Sleep(10 * time.Millisecond)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///stale.thrift", Version: 3},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  1: string name,\n}\n",
			}},
		}),
	})

	time.Sleep(100 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	notifications := collectPublishDiagnosticsMessages(t, readAllFrames(t, out.Bytes()))
	if len(notifications) != 4 {
		t.Fatalf("publishDiagnostics count=%d, want 4", len(notifications))
	}

	for i, msg := range notifications {
		var diag PublishDiagnosticsParams
		marshalRoundtrip(t, msg.Params, &diag)
		if diag.Version != nil && *diag.Version == 2 && containsDiagnosticCode(diag.Diagnostics, "LINT_DEPRECATED_FIELD_XSD_OPTIONAL") {
			t.Fatalf("notification %d published stale v2 lint diagnostics: %+v", i, diag.Diagnostics)
		}
	}

	var finalDiag PublishDiagnosticsParams
	marshalRoundtrip(t, notifications[len(notifications)-1].Params, &finalDiag)
	if finalDiag.Version == nil || *finalDiag.Version != 3 {
		t.Fatalf("final diagnostics version=%v, want 3", finalDiag.Version)
	}
	if len(finalDiag.Diagnostics) != 0 {
		t.Fatalf("expected v3 debounced lint to clear diagnostics, got %+v", finalDiag.Diagnostics)
	}
}

func TestServerRunPublishesCurrentParserDiagnosticsOnDidChangeFailure(t *testing.T) {
	restoreBreaker := syntax.ResetBackendBreakerForTesting()
	defer restoreBreaker()

	restoreFactory := syntax.SetParserFactoryForTesting(&failSecondParseFactory{})
	defer restoreFactory()

	s := NewServer()
	s.setLintDebounceForTesting(10 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{
				URI:     "file:///parser-outage.thrift",
				Version: 1,
				Text:    "struct S {\n  string name,\n}\n",
			},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: "file:///parser-outage.thrift", Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct S {\n  string renamed,\n}\n",
			}},
		}),
	})

	time.Sleep(60 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	notifications := collectPublishDiagnosticsMessages(t, readAllFrames(t, out.Bytes()))
	if len(notifications) < 2 {
		t.Fatalf("publishDiagnostics count=%d, want at least 2", len(notifications))
	}

	var sawCurrentParserFailure bool
	for _, msg := range notifications {
		var diag PublishDiagnosticsParams
		marshalRoundtrip(t, msg.Params, &diag)
		if diag.Version == nil || *diag.Version != 2 {
			continue
		}
		if containsDiagnosticCode(diag.Diagnostics, string(syntax.DiagnosticInternalParse)) {
			sawCurrentParserFailure = true
		}
		if containsDiagnosticCode(diag.Diagnostics, "LINT_FIELD_ID_REQUIRED") {
			t.Fatalf("version 2 diagnostics reused stale lint findings: %+v", diag.Diagnostics)
		}
	}
	if !sawCurrentParserFailure {
		t.Fatalf("expected version 2 INTERNAL_PARSE diagnostics, got %+v", notifications)
	}
}

func TestServerSuppressesStaleDiagnosticsAcrossCloseReopenSameVersion(t *testing.T) {
	s := NewServer()
	var out bytes.Buffer
	s.attachRuntime(t.Context(), &out)
	defer s.detachRuntime()

	uri := "file:///generation-gate.thrift"
	if err := s.DidOpen(context.Background(), DidOpenParams{TextDocument: TextDocumentItem{URI: uri, Version: 1, Text: "struct S {\n  1: string name,\n}\n"}}); err != nil {
		t.Fatalf("DidOpen initial: %v", err)
	}
	initial, ok := s.Store().Snapshot(uri)
	if !ok {
		t.Fatal("expected initial snapshot")
	}

	if err := s.DidClose(context.Background(), DidCloseParams{TextDocument: TextDocumentIdentifier{URI: uri}}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}

	if err := s.DidOpen(context.Background(), DidOpenParams{TextDocument: TextDocumentItem{URI: uri, Version: 1, Text: "struct S {\n  1: string name\n}\n"}}); err != nil {
		t.Fatalf("DidOpen reopened: %v", err)
	}
	reopened, ok := s.Store().Snapshot(uri)
	if !ok {
		t.Fatal("expected reopened snapshot")
	}
	if err := s.writeVersionedDiagnostics(uri, initial.Version, initial.Generation, []Diagnostic{{Code: "LINT_TEST_STALE"}}); err != nil {
		t.Fatalf("writeVersionedDiagnostics: %v", err)
	}

	if len(readAllFrames(t, out.Bytes())) != 0 {
		t.Fatalf("expected no stale diagnostics, got %q", out.String())
	}
	if reopened.Generation <= initial.Generation {
		t.Fatalf("reopened generation=%d, want > %d", reopened.Generation, initial.Generation)
	}
}

func TestServerRunPublishesWorkspaceDiagnosticsAndClearsOnFix(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "missing_include")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))

	s := NewServer()
	s.setLintDebounceForTesting(10 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})

	time.Sleep(60 * time.Millisecond)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: mainURI, Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct Holder {\n  1: string name,\n}\n",
			}},
		}),
	})

	time.Sleep(80 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	notifications := diagnosticsForURI(t, readAllFrames(t, out.Bytes()), mainURI)
	if !diagnosticsContainVersionAndCode(notifications, 1, "LINT_INCLUDE_TARGET_UNKNOWN") {
		t.Fatalf("missing version 1 workspace diagnostics: %+v", notifications)
	}
	if !diagnosticsContainVersionAndCode(notifications, 1, "LINT_QUALIFIED_REFERENCE_UNKNOWN") {
		t.Fatalf("missing version 1 qualified reference diagnostics: %+v", notifications)
	}

	final := latestDiagnosticsForVersion(t, notifications, 2)
	if len(final.Diagnostics) != 0 {
		t.Fatalf("expected workspace diagnostics cleared after fix, got %+v", final.Diagnostics)
	}
}

func TestServerRunSuppressesStaleWorkspaceDiagnostics(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "missing_include")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))

	s := NewServer()
	s.setLintDebounceForTesting(40 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: mainURI, Version: 2},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: mainText + "\n",
			}},
		}),
	})

	time.Sleep(10 * time.Millisecond)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didChange",
		Params: mustJSON(t, DidChangeParams{
			TextDocument: VersionedTextDocumentIdentifier{URI: mainURI, Version: 3},
			ContentChanges: []TextDocumentContentChangeEvent{{
				Text: "struct Holder {\n  1: string name,\n}\n",
			}},
		}),
	})

	time.Sleep(100 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	notifications := diagnosticsForURI(t, readAllFrames(t, out.Bytes()), mainURI)
	for _, diag := range notifications {
		if diag.Version != nil && *diag.Version == 2 && containsDiagnosticCode(diag.Diagnostics, "LINT_INCLUDE_TARGET_UNKNOWN") {
			t.Fatalf("stale version 2 workspace diagnostics were published: %+v", notifications)
		}
	}

	final := latestDiagnosticsForVersion(t, notifications, 3)
	if len(final.Diagnostics) != 0 {
		t.Fatalf("expected final version 3 diagnostics cleared, got %+v", final.Diagnostics)
	}
}

func TestServerRunUpdatesWorkspaceDiagnosticsForShadowedDependents(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "shadowing")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	sharedURI := mustCanonicalURI(t, sharedPath)

	s := NewServer()
	s.setLintDebounceForTesting(10 * time.Millisecond)
	pw, out, errCh := runServerAsync(t, s)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: string(testutil.ReadFile(t, mainPath))},
		}),
	})

	time.Sleep(40 * time.Millisecond)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: sharedURI, Version: 1, Text: "struct Person {\n  1: string name,\n}\n"},
		}),
	})

	time.Sleep(60 * time.Millisecond)

	writeReqFrame(t, pw, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didClose",
		Params:  mustJSON(t, DidCloseParams{TextDocument: TextDocumentIdentifier{URI: sharedURI}}),
	})

	time.Sleep(80 * time.Millisecond)
	stopServerAsync(t, pw, errCh)

	mainDiagnostics := diagnosticsForURI(t, readAllFrames(t, out.Bytes()), mainURI)
	if !diagnosticsContainVersionAndCode(mainDiagnostics, 1, "LINT_QUALIFIED_REFERENCE_UNKNOWN") {
		t.Fatalf("missing dependent workspace diagnostics after shadowing include: %+v", mainDiagnostics)
	}
	if len(latestDiagnosticsForVersion(t, mainDiagnostics, 1).Diagnostics) != 0 {
		t.Fatalf("expected latest main diagnostics to clear after closing shadow, got %+v", latestDiagnosticsForVersion(t, mainDiagnostics, 1).Diagnostics)
	}
}

func TestServerRunNavigationQueries(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	typesURI := mustCanonicalURI(t, filepath.Join(root, "types.thrift"))
	mainText := string(testutil.ReadFile(t, mainPath))
	userPos := mustLSPPositionForSubstring(t, []byte(mainText), "types.User input")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`30`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`31`),
		Method:  "textDocument/definition",
		Params: mustJSON(t, DefinitionParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     userPos,
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`32`),
		Method:  "textDocument/references",
		Params: mustJSON(t, ReferenceParams{
			TextDocumentPositionParams: TextDocumentPositionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     userPos,
			},
			Context: ReferenceContext{IncludeDeclaration: true},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`33`),
		Method:  "workspace/symbol",
		Params:  mustJSON(t, WorkspaceSymbolParams{Query: "user"}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())

	defResp := responseByID(t, msgs, "31")
	if defResp.Error != nil {
		t.Fatalf("definition error: %+v", defResp.Error)
	}
	var definitions []Location
	marshalRoundtrip(t, defResp.Result, &definitions)
	if len(definitions) != 1 {
		t.Fatalf("definition count=%d, want 1", len(definitions))
	}
	if definitions[0].URI != typesURI {
		t.Fatalf("definition URI=%q, want %q", definitions[0].URI, typesURI)
	}
	if got := lspRangeText(t, definitions[0].URI, definitions[0].Range); got != "User" {
		t.Fatalf("definition text=%q, want %q", got, "User")
	}

	refResp := responseByID(t, msgs, "32")
	if refResp.Error != nil {
		t.Fatalf("references error: %+v", refResp.Error)
	}
	var references []Location
	marshalRoundtrip(t, refResp.Result, &references)
	if len(references) != 3 {
		t.Fatalf("references count=%d, want 3", len(references))
	}
	gotRefTexts := []string{
		lspRangeText(t, references[0].URI, references[0].Range),
		lspRangeText(t, references[1].URI, references[1].Range),
		lspRangeText(t, references[2].URI, references[2].Range),
	}
	wantRefTexts := []string{"types.User", "types.User", "User"}
	for i := range wantRefTexts {
		if gotRefTexts[i] != wantRefTexts[i] {
			t.Fatalf("reference %d text=%q, want %q", i, gotRefTexts[i], wantRefTexts[i])
		}
	}

	symbolResp := responseByID(t, msgs, "33")
	if symbolResp.Error != nil {
		t.Fatalf("workspace/symbol error: %+v", symbolResp.Error)
	}
	var symbols []SymbolInformation
	marshalRoundtrip(t, symbolResp.Result, &symbols)
	if len(symbols) != 2 {
		t.Fatalf("workspace symbol count=%d, want 2", len(symbols))
	}
	if symbols[0].Name != "User" || symbols[1].Name != "UserError" {
		t.Fatalf("workspace symbols=%+v, want User and UserError", symbols)
	}
}

func TestServerRunNavigationQueriesReturnEmptyForUnresolvedBinding(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "missing_include")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	refPos := mustLSPPositionForSubstring(t, []byte(mainText), "missing.User")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`40`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`41`),
		Method:  "textDocument/definition",
		Params: mustJSON(t, DefinitionParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     refPos,
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`42`),
		Method:  "textDocument/references",
		Params: mustJSON(t, ReferenceParams{
			TextDocumentPositionParams: TextDocumentPositionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     refPos,
			},
			Context: ReferenceContext{IncludeDeclaration: true},
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())
	defResp := responseByID(t, msgs, "41")
	if defResp.Error != nil {
		t.Fatalf("definition error: %+v", defResp.Error)
	}
	var definitions []Location
	marshalRoundtrip(t, defResp.Result, &definitions)
	if len(definitions) != 0 {
		t.Fatalf("definition returned %+v, want empty", definitions)
	}

	refResp := responseByID(t, msgs, "42")
	if refResp.Error != nil {
		t.Fatalf("references error: %+v", refResp.Error)
	}
	var references []Location
	marshalRoundtrip(t, refResp.Result, &references)
	if len(references) != 0 {
		t.Fatalf("references returned %+v, want empty", references)
	}
}

func TestServerRunDefinitionUsesOpenShadowAndSymlinkURI(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "shadowing")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainText := string(testutil.ReadFile(t, mainPath))
	shadowedShared := "\n\nstruct User {\n  1: string name,\n}\n"

	linkPath := filepath.Join(root, "main-link.thrift")
	if err := os.Symlink(mainPath, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	linkURI := rawFileURI(linkPath)
	sharedURI := mustCanonicalURI(t, sharedPath)
	refPos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`50`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: linkURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: sharedURI, Version: 1, Text: shadowedShared},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`51`),
		Method:  "textDocument/definition",
		Params: mustJSON(t, DefinitionParams{
			TextDocument: TextDocumentIdentifier{URI: linkURI},
			Position:     refPos,
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "51")
	if resp.Error != nil {
		t.Fatalf("definition error: %+v", resp.Error)
	}
	var definitions []Location
	marshalRoundtrip(t, resp.Result, &definitions)
	if len(definitions) != 1 {
		t.Fatalf("definition count=%d, want 1", len(definitions))
	}
	if definitions[0].URI != sharedURI {
		t.Fatalf("definition URI=%q, want %q", definitions[0].URI, sharedURI)
	}
	if definitions[0].Range.Start.Line != 2 {
		t.Fatalf("definition line=%d, want 2 for open shadow", definitions[0].Range.Start.Line)
	}
}

func TestServerDefinitionReturnsContentModifiedWhenWorkspaceSnapshotDrifts(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	userPos := mustLSPPositionForSubstring(t, []byte(mainText), "types.User input")

	s := NewServer()
	var out bytes.Buffer
	s.attachRuntime(t.Context(), &out)
	defer s.detachRuntime()

	if _, err := s.Initialize(context.Background(), InitializeParams{
		WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.DidOpen(context.Background(), DidOpenParams{
		TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if _, err := s.Store().Change(context.Background(), mainURI, 2, []TextDocumentContentChangeEvent{{
		Text: mainText + "\n",
	}}); err != nil {
		t.Fatalf("Store.Change: %v", err)
	}

	req := Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`60`),
		Method:  "textDocument/definition",
		Params: mustJSON(t, DefinitionParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     userPos,
		}),
	}
	if err := s.dispatch(context.Background(), req); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "60")
	if resp.Error == nil || resp.Error.Code != lspErrorContentModified {
		t.Fatalf("definition error=%+v, want ContentModified", resp.Error)
	}
}

func TestServerRunDefinitionSupportsCaseVariantURIOnCaseInsensitiveFS(t *testing.T) {
	t.Parallel()

	if !caseInsensitiveFilesystem() {
		t.Skip("case-variant URIs require a case-insensitive filesystem")
	}

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	typesURI := mustCanonicalURI(t, filepath.Join(root, "types.thrift"))
	mainText := string(testutil.ReadFile(t, mainPath))
	userPos := mustLSPPositionForSubstring(t, []byte(mainText), "types.User input")

	caseVariantPath, ok := alternateCasePath(mainPath)
	if !ok {
		t.Skip("no alphabetic characters available for case-variant path")
	}

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`70`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: rawFileURI(caseVariantPath), Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`71`),
		Method:  "textDocument/definition",
		Params: mustJSON(t, DefinitionParams{
			TextDocument: TextDocumentIdentifier{URI: rawFileURI(caseVariantPath)},
			Position:     userPos,
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "71")
	if resp.Error != nil {
		t.Fatalf("definition error: %+v", resp.Error)
	}
	var definitions []Location
	marshalRoundtrip(t, resp.Result, &definitions)
	if len(definitions) != 1 || definitions[0].URI != typesURI {
		t.Fatalf("definition=%+v, want single target %q", definitions, typesURI)
	}
}

func TestServerRunPrepareRenameAndRename(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	sharedURI := mustCanonicalURI(t, sharedPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User user")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`80`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`81`),
		Method:  "textDocument/prepareRename",
		Params: mustJSON(t, PrepareRenameParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     renamePos,
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`82`),
		Method:  "textDocument/rename",
		Params: mustJSON(t, RenameParams{
			TextDocumentPositionParams: TextDocumentPositionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     renamePos,
			},
			NewName: "Person",
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())

	prepareResp := responseByID(t, msgs, "81")
	if prepareResp.Error != nil {
		t.Fatalf("prepareRename error: %+v", prepareResp.Error)
	}
	var prepareResult PrepareRenameResult
	marshalRoundtrip(t, prepareResp.Result, &prepareResult)
	if prepareResult.Placeholder != "User" {
		t.Fatalf("prepareRename placeholder=%q, want %q", prepareResult.Placeholder, "User")
	}
	if got := lspRangeText(t, mainURI, prepareResult.Range); got != "User" {
		t.Fatalf("prepareRename text=%q, want %q", got, "User")
	}

	renameResp := responseByID(t, msgs, "82")
	if renameResp.Error != nil {
		t.Fatalf("rename error: %+v", renameResp.Error)
	}
	var edit WorkspaceEdit
	marshalRoundtrip(t, renameResp.Result, &edit)
	if len(edit.DocumentChanges) != 2 {
		t.Fatalf("rename documentChanges=%d, want 2", len(edit.DocumentChanges))
	}
	if edit.DocumentChanges[0].TextDocument.URI != mainURI || edit.DocumentChanges[1].TextDocument.URI != sharedURI {
		t.Fatalf("rename document order=%+v", edit.DocumentChanges)
	}
	if edit.DocumentChanges[0].TextDocument.Version == nil || *edit.DocumentChanges[0].TextDocument.Version != 1 {
		t.Fatalf("open document version=%v, want 1", edit.DocumentChanges[0].TextDocument.Version)
	}
	if edit.DocumentChanges[1].TextDocument.Version != nil {
		t.Fatalf("closed document version=%v, want nil", edit.DocumentChanges[1].TextDocument.Version)
	}
	if len(edit.DocumentChanges[0].Edits) != 2 {
		t.Fatalf("main rename edits=%+v, want 2", edit.DocumentChanges[0].Edits)
	}
	if compareRangeStarts(edit.DocumentChanges[0].Edits[0].Range, edit.DocumentChanges[0].Edits[1].Range) >= 0 {
		t.Fatalf("main rename edits should be sorted ascending: %+v", edit.DocumentChanges[0].Edits)
	}

	renamedMain := applyWorkspaceTextEdits(t, []byte(mainText), edit.DocumentChanges[0].Edits)
	if string(renamedMain) != "include \"shared.thrift\"\n\nstruct Holder {\n  1: shared.Person user,\n  2: shared.Person backup,\n}\n" {
		t.Fatalf("renamed main = %q", renamedMain)
	}
	renamedShared := applyWorkspaceTextEdits(t, testutil.ReadFile(t, sharedPath), edit.DocumentChanges[1].Edits)
	if string(renamedShared) != "struct Person {\n  1: string name,\n}\n" {
		t.Fatalf("renamed shared = %q", renamedShared)
	}
}

func TestServerRunRenameBlockedForAmbiguousReference(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "duplicate_alias")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`90`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`91`),
		Method:  "textDocument/prepareRename",
		Params: mustJSON(t, PrepareRenameParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     renamePos,
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "91")
	if resp.Error == nil || resp.Error.Code != lspErrorRequestFailed {
		t.Fatalf("prepareRename error=%+v, want RequestFailed", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "ambiguous") {
		t.Fatalf("prepareRename message=%q, want ambiguous", resp.Error.Message)
	}
}

func TestServerRunRenameBlockedForTaintedReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sharedPath := filepath.Join(root, "shared.thrift")
	if err := os.WriteFile(sharedPath, []byte("struct User {\n  1: string name,\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", sharedPath, err)
	}

	rootURI := mustCanonicalURI(t, root)
	mainURI := mustCanonicalURI(t, filepath.Join(root, "main.thrift"))
	mainText := "include \"shared.thrift\"\n\nstruct Holder {\n  1: shared.User user,\n"
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`95`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`96`),
		Method:  "textDocument/prepareRename",
		Params: mustJSON(t, PrepareRenameParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     renamePos,
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "96")
	if resp.Error == nil || resp.Error.Code != lspErrorRequestFailed {
		t.Fatalf("prepareRename error=%+v, want RequestFailed", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "tainted") {
		t.Fatalf("prepareRename message=%q, want tainted", resp.Error.Message)
	}
}

func TestServerRunRenameBlockedForInvalidName(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User user")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`100`),
		Method:  "initialize",
		Params: mustJSON(t, InitializeParams{
			WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`101`),
		Method:  "textDocument/rename",
		Params: mustJSON(t, RenameParams{
			TextDocumentPositionParams: TextDocumentPositionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     renamePos,
			},
			NewName: "struct",
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "101")
	if resp.Error == nil || resp.Error.Code != lspErrorRequestFailed {
		t.Fatalf("rename error=%+v, want RequestFailed", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "identifier") {
		t.Fatalf("rename message=%q, want identifier", resp.Error.Message)
	}
}

func TestServerRunRenameAbortsOnClosedFileDrift(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User user")

	s := NewServer()
	var out bytes.Buffer
	s.attachRuntime(t.Context(), &out)
	defer s.detachRuntime()

	if _, err := s.Initialize(context.Background(), InitializeParams{
		WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.DidOpen(context.Background(), DidOpenParams{
		TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if err := os.WriteFile(sharedPath, []byte("struct User {\n  1: string name,\n  2: string alias,\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", sharedPath, err)
	}

	req := Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`110`),
		Method:  "textDocument/rename",
		Params: mustJSON(t, RenameParams{
			TextDocumentPositionParams: TextDocumentPositionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     renamePos,
			},
			NewName: "Person",
		}),
	}
	if err := s.dispatch(context.Background(), req); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "110")
	if resp.Error == nil || resp.Error.Code != lspErrorContentModified {
		t.Fatalf("rename error=%+v, want ContentModified", resp.Error)
	}
}

func TestServerRunDocumentFormattingSuccessNoOpRefusalAndStale(t *testing.T) {
	t.Parallel()

	t.Run("success_and_noop", func(t *testing.T) {
		t.Parallel()

		var in bytes.Buffer
		src := "service S{async void ping(1:i32 id);}\n"
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didOpen",
			Params: mustJSON(t, DidOpenParams{
				TextDocument: TextDocumentItem{URI: "file:///fmt.thrift", Version: 1, Text: src},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`1`),
			Method:  "textDocument/formatting",
			Params: mustJSON(t, DocumentFormattingParams{
				TextDocument: TextDocumentIdentifier{URI: "file:///fmt.thrift"},
				Version:      int32Ptr(1),
				Options:      FormattingOptions{TabSize: 2, InsertSpaces: true},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didChange",
			Params: mustJSON(t, DidChangeParams{
				TextDocument: VersionedTextDocumentIdentifier{URI: "file:///fmt.thrift", Version: 2},
				ContentChanges: []TextDocumentContentChangeEvent{{
					Text: "service S {\n  async void ping(1: i32 id),\n}\n",
				}},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`2`),
			Method:  "textDocument/formatting",
			Params: mustJSON(t, DocumentFormattingParams{
				TextDocument: TextDocumentIdentifier{URI: "file:///fmt.thrift"},
				Version:      int32Ptr(2),
				Options:      FormattingOptions{TabSize: 2, InsertSpaces: true},
			}),
		})

		var out bytes.Buffer
		if err := NewServer().Run(context.Background(), &in, &out); err != nil {
			t.Fatalf("Run: %v", err)
		}
		msgs := readAllFrames(t, out.Bytes())
		resp1 := responseByID(t, msgs, "1")
		resp2 := responseByID(t, msgs, "2")
		if resp1.Error != nil {
			t.Fatalf("formatting response 1 error: %+v", resp1.Error)
		}
		if resp2.Error != nil {
			t.Fatalf("formatting response 2 error: %+v", resp2.Error)
		}
		var edits1 []TextEdit
		marshalRoundtrip(t, resp1.Result, &edits1)
		if len(edits1) != 1 {
			t.Fatalf("edits1 len=%d, want 1", len(edits1))
		}
		if !strings.Contains(edits1[0].NewText, "async void ping(1: i32 id),") {
			t.Fatalf("unexpected formatting output: %q", edits1[0].NewText)
		}
		var edits2 []TextEdit
		marshalRoundtrip(t, resp2.Result, &edits2)
		if len(edits2) != 0 {
			t.Fatalf("edits2 len=%d, want 0", len(edits2))
		}
	})

	t.Run("refusal_and_stale", func(t *testing.T) {
		t.Parallel()

		var in bytes.Buffer
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didOpen",
			Params: mustJSON(t, DidOpenParams{
				TextDocument: TextDocumentItem{URI: "file:///bad.thrift", Version: 1, Text: "const string BAD = \"unterminated\n"},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`3`),
			Method:  "textDocument/formatting",
			Params: mustJSON(t, DocumentFormattingParams{
				TextDocument: TextDocumentIdentifier{URI: "file:///bad.thrift"},
				Version:      int32Ptr(1),
				Options:      FormattingOptions{TabSize: 2, InsertSpaces: true},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didChange",
			Params: mustJSON(t, DidChangeParams{
				TextDocument: VersionedTextDocumentIdentifier{URI: "file:///bad.thrift", Version: 2},
				ContentChanges: []TextDocumentContentChangeEvent{{
					Text: "const string BAD = \"ok\"\n",
				}},
			}),
		})
		writeReqFrame(t, &in, Request{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`4`),
			Method:  "textDocument/formatting",
			Params: mustJSON(t, DocumentFormattingParams{
				TextDocument: TextDocumentIdentifier{URI: "file:///bad.thrift"},
				Version:      int32Ptr(1), // stale after didChange to v2
				Options:      FormattingOptions{TabSize: 2, InsertSpaces: true},
			}),
		})

		var out bytes.Buffer
		if err := NewServer().Run(context.Background(), &in, &out); err != nil {
			t.Fatalf("Run: %v", err)
		}
		msgs := readAllFrames(t, out.Bytes())
		resp3 := responseByID(t, msgs, "3")
		if resp3.Error == nil || resp3.Error.Code != lspErrorRequestFailed {
			t.Fatalf("response 3 error=%+v, want RequestFailed", resp3.Error)
		}
		resp4 := responseByID(t, msgs, "4")
		if resp4.Error == nil || resp4.Error.Code != lspErrorContentModified {
			t.Fatalf("response 4 error=%+v, want ContentModified", resp4.Error)
		}
	})
}

func TestServerRunRangeFormattingUsesUTF16RangeAndReturnsEdits(t *testing.T) {
	t.Parallel()

	src := "struct S {\n  1: optional string name = \"😀\" (ann='x');\n}\n"
	start := strings.Index(src, "ann")
	if start < 0 {
		t.Fatal("failed to find ann")
	}
	end := start + len("ann")

	store := NewSnapshotStore()
	snap, err := store.Open(context.Background(), "file:///range.thrift", 1, []byte(src))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	startPos, err := snap.Tree.LineIndex.OffsetToUTF16Position(itext.ByteOffset(start))
	if err != nil {
		t.Fatalf("OffsetToUTF16Position start: %v", err)
	}
	endPos, err := snap.Tree.LineIndex.OffsetToUTF16Position(itext.ByteOffset(end))
	if err != nil {
		t.Fatalf("OffsetToUTF16Position end: %v", err)
	}

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: "file:///range.thrift", Version: 1, Text: src},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`5`),
		Method:  "textDocument/rangeFormatting",
		Params: mustJSON(t, DocumentRangeFormattingParams{
			TextDocument: TextDocumentIdentifier{URI: "file:///range.thrift"},
			Version:      int32Ptr(1),
			Range: Range{
				Start: Position{Line: startPos.Line, Character: startPos.Character},
				End:   Position{Line: endPos.Line, Character: endPos.Character},
			},
			Options: FormattingOptions{TabSize: 2, InsertSpaces: true},
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	resp := responseByID(t, readAllFrames(t, out.Bytes()), "5")
	if resp.Error != nil {
		t.Fatalf("rangeFormatting error: %+v", resp.Error)
	}
	var edits []TextEdit
	marshalRoundtrip(t, resp.Result, &edits)
	if len(edits) == 0 {
		t.Fatal("expected non-empty range formatting edits")
	}
	if !strings.Contains(edits[0].NewText, `ann = 'x'`) {
		t.Fatalf("unexpected range edit output: %q", edits[0].NewText)
	}
}

func TestServerRunEditorQueryFeatures(t *testing.T) {
	t.Parallel()

	src := strings.TrimLeft(`
service Demo {
  // method comment
  void ping(1: i32 id) throws (1: string msg)
}

# header
# details
struct Holder {
  1: string value
  2: optional string note
}

enum Color {
  RED = 1
  BLUE = 2
}

typedef map<string, i32> StringIntMap
const i32 ANSWER = 42
`, "\n")

	valueStart := strings.Index(src, "value")
	if valueStart < 0 {
		t.Fatal("failed to find field name")
	}
	li := itext.NewLineIndex([]byte(src))
	valuePos, err := li.OffsetToUTF16Position(itext.ByteOffset(valueStart))
	if err != nil {
		t.Fatalf("OffsetToUTF16Position: %v", err)
	}

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: "file:///editor.thrift", Version: 1, Text: src},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`10`),
		Method:  "textDocument/documentSymbol",
		Params:  mustJSON(t, DocumentSymbolParams{TextDocument: TextDocumentIdentifier{URI: "file:///editor.thrift"}}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`11`),
		Method:  "textDocument/foldingRange",
		Params:  mustJSON(t, FoldingRangeParams{TextDocument: TextDocumentIdentifier{URI: "file:///editor.thrift"}}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`12`),
		Method:  "textDocument/selectionRange",
		Params: mustJSON(t, SelectionRangeParams{
			TextDocument: TextDocumentIdentifier{URI: "file:///editor.thrift"},
			Positions:    []Position{{Line: valuePos.Line, Character: valuePos.Character}},
		}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := readAllFrames(t, out.Bytes())

	respSymbols := responseByID(t, msgs, "10")
	if respSymbols.Error != nil {
		t.Fatalf("documentSymbol error: %+v", respSymbols.Error)
	}
	var symbols []DocumentSymbol
	marshalRoundtrip(t, respSymbols.Result, &symbols)
	if len(symbols) < 5 {
		t.Fatalf("documentSymbol count=%d, want >=5", len(symbols))
	}
	demo := findDocumentSymbol(symbols, "Demo")
	if demo == nil {
		t.Fatalf("expected Demo symbol in %+v", symbols)
	}
	if findDocumentSymbol(demo.Children, "ping") == nil {
		t.Fatalf("expected ping child under Demo, got %+v", demo.Children)
	}
	holder := findDocumentSymbol(symbols, "Holder")
	if holder == nil {
		t.Fatalf("expected Holder symbol")
	}
	if findDocumentSymbol(holder.Children, "value") == nil {
		t.Fatalf("expected value field child under Holder, got %+v", holder.Children)
	}
	color := findDocumentSymbol(symbols, "Color")
	if color == nil || findDocumentSymbol(color.Children, "RED") == nil {
		t.Fatalf("expected enum Color with RED child, got %+v", color)
	}

	respFolds := responseByID(t, msgs, "11")
	if respFolds.Error != nil {
		t.Fatalf("foldingRange error: %+v", respFolds.Error)
	}
	var folds []FoldingRange
	marshalRoundtrip(t, respFolds.Result, &folds)
	if len(folds) < 4 {
		t.Fatalf("foldingRange count=%d, want >=4", len(folds))
	}
	if !hasFoldingRangeStartingAtLine(folds, 5) { // # header block starts at line 5 (0-based)
		t.Fatalf("expected comment folding range starting at line 5, got %+v", folds)
	}

	respSelection := responseByID(t, msgs, "12")
	if respSelection.Error != nil {
		t.Fatalf("selectionRange error: %+v", respSelection.Error)
	}
	var sels []SelectionRange
	marshalRoundtrip(t, respSelection.Result, &sels)
	if len(sels) != 1 {
		t.Fatalf("selectionRange count=%d, want 1", len(sels))
	}
	if sels[0].Parent == nil || sels[0].Parent.Parent == nil {
		t.Fatalf("expected nested selection parents, got %+v", sels[0])
	}
	if sels[0].Range.Start.Line != valuePos.Line {
		t.Fatalf("inner selection line=%d, want %d", sels[0].Range.Start.Line, valuePos.Line)
	}
	if sels[0].Parent.Range.Start.Line > sels[0].Range.Start.Line ||
		sels[0].Parent.Range.End.Line < sels[0].Range.End.Line {
		t.Fatalf("parent range %+v should contain child %+v", sels[0].Parent.Range, sels[0].Range)
	}
}

func TestServerRunSemanticTokensFull(t *testing.T) {
	t.Parallel()

	src := strings.TrimLeft(`
// docs
service Demo {
  void ping(1: i32 id) throws (1: string msg)
}
`, "\n")

	var in bytes.Buffer
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		Method:  "textDocument/didOpen",
		Params: mustJSON(t, DidOpenParams{
			TextDocument: TextDocumentItem{URI: "file:///semantic.thrift", Version: 1, Text: src},
		}),
	})
	writeReqFrame(t, &in, Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`20`),
		Method:  "textDocument/semanticTokens/full",
		Params:  mustJSON(t, SemanticTokensParams{TextDocument: TextDocumentIdentifier{URI: "file:///semantic.thrift"}}),
	})

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := responseByID(t, readAllFrames(t, out.Bytes()), "20")
	if resp.Error != nil {
		t.Fatalf("semanticTokens/full error: %+v", resp.Error)
	}
	var tokens SemanticTokens
	marshalRoundtrip(t, resp.Result, &tokens)
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Fatalf("unexpected semantic token payload length=%d", len(tokens.Data))
	}

	keywordType := semanticTokenTypeIndex["keyword"]
	methodType := semanticTokenTypeIndex["method"]
	commentType := semanticTokenTypeIndex["comment"]

	var sawKeyword, sawMethod, sawComment bool
	for i := 0; i+4 < len(tokens.Data); i += 5 {
		switch tokens.Data[i+3] {
		case keywordType:
			sawKeyword = true
		case methodType:
			sawMethod = true
		case commentType:
			sawComment = true
		}
	}
	if !sawKeyword || !sawMethod || !sawComment {
		t.Fatalf("expected keyword/method/comment semantic tokens, got data=%v", tokens.Data)
	}
}

func writeReqFrame(t *testing.T, w io.Writer, req Request) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := writeFramedMessage(w, b); err != nil {
		t.Fatalf("writeFramedMessage: %v", err)
	}
}

func runServerAsync(t *testing.T, s *Server) (*io.PipeWriter, *bytes.Buffer, <-chan error) {
	t.Helper()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
	})

	out := new(bytes.Buffer)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(context.Background(), pr, out)
	}()

	return pw, out, errCh
}

func stopServerAsync(t *testing.T, pw *io.PipeWriter, errCh <-chan error) {
	t.Helper()

	if err := pw.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func readRespFrame(t *testing.T, r *bufio.Reader) Response {
	t.Helper()
	b, err := readFramedMessage(r)
	if err != nil {
		t.Fatalf("readFramedMessage: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("json.Unmarshal response: %v", err)
	}
	return resp
}

func marshalRoundtrip(t *testing.T, in any, out any) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal roundtrip: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("json.Unmarshal roundtrip: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal params: %v", err)
	}
	return json.RawMessage(b)
}

func int32Ptr(v int32) *int32 {
	p := new(int32)
	*p = v
	return p
}

func mustCanonicalURI(t *testing.T, path string) string {
	t.Helper()
	uri, _, err := index.CanonicalizeDocumentURI(path)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(%s): %v", path, err)
	}
	return uri
}

func mustLSPPositionForSubstring(t *testing.T, src []byte, needle string) Position {
	t.Helper()
	start := strings.Index(string(src), needle)
	if start < 0 {
		t.Fatalf("substring %q not found", needle)
	}
	li := itext.NewLineIndex(src)
	pos, err := li.OffsetToUTF16Position(itext.ByteOffset(start))
	if err != nil {
		t.Fatalf("OffsetToUTF16Position(%q): %v", needle, err)
	}
	return Position{Line: pos.Line, Character: pos.Character}
}

func lspRangeText(t *testing.T, uri string, rng Range) string {
	t.Helper()
	path, err := filePathFromDocumentURI(uri)
	if err != nil {
		t.Fatalf("filePathFromDocumentURI(%s): %v", uri, err)
	}
	src := testutil.ReadFile(t, path)
	li := itext.NewLineIndex(src)
	span, err := byteSpanFromLSPRange(li, rng)
	if err != nil {
		t.Fatalf("byteSpanFromLSPRange(%s): %v", uri, err)
	}
	if int(span.End) > len(src) {
		t.Fatalf("range %v out of bounds for %s", rng, uri)
	}
	return string(src[span.Start:span.End])
}

func applyWorkspaceTextEdits(t *testing.T, src []byte, edits []TextEdit) []byte {
	t.Helper()
	li := itext.NewLineIndex(src)
	byteEdits := make([]itext.ByteEdit, 0, len(edits))
	for _, edit := range edits {
		span, err := byteSpanFromLSPRange(li, edit.Range)
		if err != nil {
			t.Fatalf("byteSpanFromLSPRange: %v", err)
		}
		byteEdits = append(byteEdits, itext.ByteEdit{
			Span:    span,
			NewText: []byte(edit.NewText),
		})
	}
	out, err := itext.ApplyEdits(src, byteEdits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	return out
}

func compareRangeStarts(a, b Range) int {
	if a.Start.Line < b.Start.Line {
		return -1
	}
	if a.Start.Line > b.Start.Line {
		return 1
	}
	if a.Start.Character < b.Start.Character {
		return -1
	}
	if a.Start.Character > b.Start.Character {
		return 1
	}
	return 0
}

func rawFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func alternateCasePath(path string) (string, bool) {
	runes := []rune(path)
	for i, r := range runes {
		if !unicode.IsLetter(r) {
			continue
		}
		alt := append([]rune(nil), runes...)
		if unicode.IsUpper(r) {
			alt[i] = unicode.ToLower(r)
		} else {
			alt[i] = unicode.ToUpper(r)
		}
		return string(alt), true
	}
	return "", false
}

func caseInsensitiveFilesystem() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return false
	}
}

func diagnosticsForURI(t *testing.T, msgs []testFrame, uri string) []PublishDiagnosticsParams {
	t.Helper()
	notifications := collectPublishDiagnosticsMessages(t, msgs)
	out := make([]PublishDiagnosticsParams, 0, len(notifications))
	for _, msg := range notifications {
		var diag PublishDiagnosticsParams
		marshalRoundtrip(t, msg.Params, &diag)
		if diag.URI == uri {
			out = append(out, diag)
		}
	}
	return out
}

func diagnosticsContainVersionAndCode(diags []PublishDiagnosticsParams, version int32, code string) bool {
	for _, diag := range diags {
		if diag.Version != nil && *diag.Version == version && containsDiagnosticCode(diag.Diagnostics, code) {
			return true
		}
	}
	return false
}

func latestDiagnosticsForVersion(t *testing.T, diags []PublishDiagnosticsParams, version int32) PublishDiagnosticsParams {
	t.Helper()
	var (
		found bool
		last  PublishDiagnosticsParams
	)
	for _, diag := range diags {
		if diag.Version == nil || *diag.Version != version {
			continue
		}
		found = true
		last = diag
	}
	if !found {
		t.Fatalf("missing diagnostics for version %d in %+v", version, diags)
	}
	return last
}

type testFrame struct {
	body []byte
	msg  Request
}

func readAllFrames(t *testing.T, raw []byte) []testFrame {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(raw))
	var out []testFrame
	for {
		body, err := readFramedMessage(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("readFramedMessage: %v", err)
		}
		var msg Request
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("json.Unmarshal frame: %v", err)
		}
		out = append(out, testFrame{body: body, msg: msg})
	}
	return out
}

func collectPublishDiagnosticsMessages(t *testing.T, msgs []testFrame) []Request {
	t.Helper()
	out := make([]Request, 0, len(msgs))
	for _, msg := range msgs {
		if msg.msg.Method == "textDocument/publishDiagnostics" {
			out = append(out, msg.msg)
		}
	}
	return out
}

func responseByID(t *testing.T, msgs []testFrame, id string) Response {
	t.Helper()
	for _, msg := range msgs {
		if string(msg.msg.ID) != id {
			continue
		}
		var resp Response
		if err := json.Unmarshal(msg.body, &resp); err != nil {
			t.Fatalf("json.Unmarshal response: %v", err)
		}
		return resp
	}
	t.Fatalf("response id=%s not found", id)
	return Response{}
}

func findDocumentSymbol(in []DocumentSymbol, name string) *DocumentSymbol {
	for i := range in {
		if in[i].Name == name {
			return &in[i]
		}
	}
	return nil
}

func hasFoldingRangeStartingAtLine(in []FoldingRange, line int) bool {
	for _, fr := range in {
		if fr.StartLine == line && fr.EndLine > fr.StartLine {
			return true
		}
	}
	return false
}

func containsDiagnosticCode(in []Diagnostic, code string) bool {
	for _, d := range in {
		if d.Code == code {
			return true
		}
	}
	return false
}

type failSecondParseFactory struct{}

func (f *failSecondParseFactory) Name() string {
	return "fail-second-parse-factory"
}

func (f *failSecondParseFactory) NewParser() (parserbackend.Parser, error) {
	inner, err := ts.NewParser()
	if err != nil {
		return nil, err
	}
	return &failSecondParseParser{inner: inner}, nil
}

type failSecondParseParser struct {
	inner parserbackend.Parser
	calls int
}

func (p *failSecondParseParser) Parse(ctx context.Context, src []byte, old *ts.Tree) (*ts.Tree, error) {
	p.calls++
	if p.calls >= 2 {
		return nil, errors.New("injected parse failure")
	}
	return p.inner.Parse(ctx, src, old)
}

func (p *failSecondParseParser) Close() {
	if p.inner != nil {
		p.inner.Close()
		p.inner = nil
	}
}
