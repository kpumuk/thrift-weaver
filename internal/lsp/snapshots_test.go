package lsp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSnapshotStoreOpenChangeCloseLifecycle(t *testing.T) {
	t.Parallel()

	store := NewSnapshotStore()
	uri := "file:///demo.thrift"
	openSrc := []byte("struct S {\n  1: string a\n}\n")
	snap, err := store.Open(context.Background(), uri, 1, openSrc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if snap.Version != 1 {
		t.Fatalf("version=%d, want 1", snap.Version)
	}

	next, err := store.Change(context.Background(), uri, 2, []TextDocumentContentChangeEvent{{
		Range: &Range{
			Start: Position{Line: 1, Character: 12},
			End:   Position{Line: 1, Character: 13},
		},
		Text: "b",
	}})
	if err != nil {
		t.Fatalf("Change: %v", err)
	}
	if next.Version != 2 {
		t.Fatalf("version=%d, want 2", next.Version)
	}
	if got := string(next.Tree.Source); !strings.Contains(got, "string b") {
		t.Fatalf("unexpected source after change: %q", got)
	}

	if _, err := store.Change(context.Background(), uri, 2, []TextDocumentContentChangeEvent{{Text: string(next.Tree.Source)}}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale version error = %v, want %v", err, ErrStaleVersion)
	}

	store.Close(uri)
	if _, ok := store.Snapshot(uri); ok {
		t.Fatal("expected snapshot removed after close")
	}
}

func TestSnapshotStoreChangeAllowsInvalidSyntaxAndKeepsDiagnostics(t *testing.T) {
	t.Parallel()

	store := NewSnapshotStore()
	uri := "file:///invalid.thrift"
	if _, err := store.Open(context.Background(), uri, 1, []byte("struct S {\n  1: string a\n}\n")); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := store.Change(context.Background(), uri, 2, []TextDocumentContentChangeEvent{{Text: "struct S {\n  1: string a\n"}})
	if err != nil {
		t.Fatalf("Change invalid syntax: %v", err)
	}
	snap, ok := store.Snapshot(uri)
	if !ok {
		t.Fatal("expected snapshot after invalid change")
	}
	if len(snap.Tree.Diagnostics) == 0 {
		t.Fatal("expected recoverable parser diagnostics for invalid syntax")
	}
}

func TestSnapshotStoreChangeRejectsUnknownDocumentAndBadRange(t *testing.T) {
	t.Parallel()

	store := NewSnapshotStore()
	if _, err := store.Change(context.Background(), "file:///missing.thrift", 1, []TextDocumentContentChangeEvent{{Text: "x"}}); !errors.Is(err, ErrDocumentNotOpen) {
		t.Fatalf("missing doc error = %v, want %v", err, ErrDocumentNotOpen)
	}

	uri := "file:///bad-range.thrift"
	if _, err := store.Open(context.Background(), uri, 1, []byte("struct S {\n  1: string a\n}\n")); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := store.Change(context.Background(), uri, 2, []TextDocumentContentChangeEvent{{
		Range: &Range{Start: Position{Line: 99, Character: 0}, End: Position{Line: 99, Character: 1}},
		Text:  "x",
	}})
	if err == nil {
		t.Fatal("expected invalid range error")
	}
}

func TestServerDidOpenDidChangeDidCloseLifecycle(t *testing.T) {
	t.Parallel()

	s := NewServer()
	uri := "file:///server.thrift"
	if err := s.DidOpen(context.Background(), DidOpenParams{TextDocument: TextDocumentItem{URI: uri, Version: 1, Text: "struct S {\n  1: string a\n}\n"}}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if err := s.DidChange(context.Background(), DidChangeParams{
		TextDocument: VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{{
			Range: &Range{Start: Position{Line: 1, Character: 12}, End: Position{Line: 1, Character: 13}},
			Text:  "z",
		}},
	}); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	snap, ok := s.Store().Snapshot(uri)
	if !ok || !strings.Contains(string(snap.Tree.Source), "string z") {
		t.Fatalf("unexpected snapshot after didChange: ok=%v src=%q", ok, snap.Tree.Source)
	}
	if err := s.DidClose(context.Background(), DidCloseParams{TextDocument: TextDocumentIdentifier{URI: uri}}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if _, ok := s.Store().Snapshot(uri); ok {
		t.Fatal("expected document closed")
	}
}
