package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/index"
)

// LSP requires the version key on OptionalVersionedTextDocumentIdentifier
// and allows null; vscode-languageclient rejects a document change without it.
func TestServerRenameWireVersionForOpenAndClosedDocuments(t *testing.T) {
	t.Parallel()

	root, s, manager, mainURI, mainText := openServerWorkspaceMainDocumentForTesting(t, "rename")
	sharedURI := mustCanonicalURI(t, filepath.Join(root, "shared.thrift"))
	if err := manager.RescanWorkspaceWithReason(context.Background(), index.RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	var out bytes.Buffer
	s.attachRuntime(t.Context(), &out)
	defer s.detachRuntime()

	if err := s.dispatch(context.Background(), Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`7`),
		Method:  "textDocument/rename",
		Params: mustJSON(t, RenameParams{
			TextDocument: TextDocumentIdentifier{URI: mainURI},
			Position:     mustLSPPositionForSubstring(t, mainText, "shared.User user"),
			NewName:      "Person",
		}),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var body []byte
	for _, frame := range readAllFrames(t, out.Bytes()) {
		if string(frame.msg.ID) == "7" {
			body = frame.body
		}
	}
	if body == nil {
		t.Fatal("rename response not found")
	}

	var resp struct {
		Result struct {
			DocumentChanges []struct {
				TextDocument map[string]json.RawMessage `json:"textDocument"`
			} `json:"documentChanges"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("json.Unmarshal response: %v", err)
	}

	want := map[string]string{mainURI: "1", sharedURI: "null"}
	if len(resp.Result.DocumentChanges) != len(want) {
		t.Fatalf("documentChanges=%s, want %d entries", body, len(want))
	}
	for _, change := range resp.Result.DocumentChanges {
		var uri string
		if err := json.Unmarshal(change.TextDocument["uri"], &uri); err != nil {
			t.Fatalf("textDocument.uri: %v in %s", err, body)
		}
		version, ok := change.TextDocument["version"]
		if !ok {
			t.Fatalf("textDocument for %s has no version key: %s", uri, body)
		}
		if string(version) != want[uri] {
			t.Fatalf("textDocument.version for %s = %s, want %s", uri, version, want[uri])
		}
	}
}
