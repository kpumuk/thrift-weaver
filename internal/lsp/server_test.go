package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

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
	if !got.DocumentFormattingProvider || !got.DocumentRangeFormattingProvider || !got.DocumentSymbolProvider || !got.FoldingRangeProvider || !got.SelectionRangeProvider {
		t.Fatalf("unexpected capabilities: %+v", got)
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
	notifications := collectMethodMessages(t, msgs, "textDocument/publishDiagnostics")
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

func writeReqFrame(t *testing.T, w *bytes.Buffer, req Request) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := writeFramedMessage(w, b); err != nil {
		t.Fatalf("writeFramedMessage: %v", err)
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

func collectMethodMessages(t *testing.T, msgs []testFrame, method string) []Request {
	t.Helper()
	out := make([]Request, 0, len(msgs))
	for _, msg := range msgs {
		if msg.msg.Method == method {
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
