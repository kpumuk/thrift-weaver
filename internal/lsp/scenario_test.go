package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPScenarioInitializeOpenChangeFormat(t *testing.T) {
	t.Parallel()

	initial := loadLSPScenarioFixture(t, "initialize_open_change_format", "initial.thrift")
	changed := loadLSPScenarioFixture(t, "initialize_open_change_format", "changed.thrift")

	msgs := runLSPScenario(t, []Request{
		{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`"init"`),
			Method:  "initialize",
			Params:  json.RawMessage(`{}`),
		},
		{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didOpen",
			Params: mustJSON(t, DidOpenParams{
				TextDocument: TextDocumentItem{
					URI:     "file:///scenario.thrift",
					Version: 1,
					Text:    initial,
				},
			}),
		},
		{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didChange",
			Params: mustJSON(t, DidChangeParams{
				TextDocument: VersionedTextDocumentIdentifier{URI: "file:///scenario.thrift", Version: 2},
				ContentChanges: []TextDocumentContentChangeEvent{{
					Text: changed,
				}},
			}),
		},
		{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`"fmt"`),
			Method:  "textDocument/formatting",
			Params:  mustJSON(t, formattingScenarioParams("file:///scenario.thrift", 2)),
		},
	})

	initResp := responseByID(t, msgs, `"init"`)
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	diagNotifications := collectMethodMessages(t, msgs, "textDocument/publishDiagnostics")
	if len(diagNotifications) != 2 {
		t.Fatalf("publishDiagnostics count=%d, want 2", len(diagNotifications))
	}

	fmtResp := responseByID(t, msgs, `"fmt"`)
	if fmtResp.Error != nil {
		t.Fatalf("formatting error: %+v", fmtResp.Error)
	}
	var edits []TextEdit
	marshalRoundtrip(t, fmtResp.Result, &edits)
	if len(edits) == 0 {
		t.Fatal("expected formatting edits")
	}
	if !strings.Contains(edits[0].NewText, "struct Scenario {") {
		t.Fatalf("formatted output missing struct declaration: %q", edits[0].NewText)
	}
	if !strings.Contains(edits[0].NewText, "optional string value,") {
		t.Fatalf("formatted output missing normalized field separator: %q", edits[0].NewText)
	}
}

func TestLSPScenarioCancelFormattingRequestRemainsUsable(t *testing.T) {
	t.Parallel()

	src := loadLSPScenarioFixture(t, "cancel_then_format", "input.thrift")

	msgs := runLSPScenario(t, []Request{
		{
			JSONRPC: JSONRPCVersion,
			Method:  "textDocument/didOpen",
			Params: mustJSON(t, DidOpenParams{
				TextDocument: TextDocumentItem{
					URI:     "file:///cancel.thrift",
					Version: 1,
					Text:    src,
				},
			}),
		},
		{
			JSONRPC: JSONRPCVersion,
			Method:  "$/cancelRequest",
			Params:  mustJSON(t, CancelParams{ID: json.RawMessage(`"fmt-cancel"`)}),
		},
		{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`"fmt-cancel"`),
			Method:  "textDocument/formatting",
			Params:  mustJSON(t, formattingScenarioParams("file:///cancel.thrift", 1)),
		},
		{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`"fmt-ok"`),
			Method:  "textDocument/formatting",
			Params:  mustJSON(t, formattingScenarioParams("file:///cancel.thrift", 1)),
		},
	})

	cancelResp := responseByID(t, msgs, `"fmt-cancel"`)
	if cancelResp.Error == nil || cancelResp.Error.Code != lspErrorRequestCancelled {
		t.Fatalf("cancelled formatting error=%+v, want RequestCancelled", cancelResp.Error)
	}

	okResp := responseByID(t, msgs, `"fmt-ok"`)
	if okResp.Error != nil {
		t.Fatalf("follow-up formatting error: %+v", okResp.Error)
	}
	var edits []TextEdit
	marshalRoundtrip(t, okResp.Result, &edits)
	if len(edits) == 0 {
		t.Fatal("expected follow-up formatting edits after cancellation")
	}
}

func runLSPScenario(t *testing.T, reqs []Request) []testFrame {
	t.Helper()

	var in bytes.Buffer
	for _, req := range reqs {
		writeReqFrame(t, &in, req)
	}

	var out bytes.Buffer
	if err := NewServer().Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return readAllFrames(t, out.Bytes())
}

func loadLSPScenarioFixture(t *testing.T, scenarioName, fileName string) string {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "lsp", "scenarios", scenarioName, fileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func formattingScenarioParams(uri string, version int32) DocumentFormattingParams {
	return DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Version:      int32Ptr(version),
		Options:      FormattingOptions{TabSize: 2, InsertSpaces: true},
	}
}
