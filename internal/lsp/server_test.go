package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"
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
