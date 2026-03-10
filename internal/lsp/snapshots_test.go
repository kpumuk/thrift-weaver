package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
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
	if snap.Generation != 1 {
		t.Fatalf("generation=%d, want 1", snap.Generation)
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
	if next.Generation != 2 {
		t.Fatalf("generation=%d, want 2", next.Generation)
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

	reopened, err := store.Open(context.Background(), uri, 1, openSrc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Generation != 4 {
		t.Fatalf("reopened generation=%d, want 4", reopened.Generation)
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

func TestSnapshotStoreChangeUsesIncrementalReparseForRangedEdits(t *testing.T) {
	store := NewSnapshotStore()
	uri := "file:///incremental.thrift"
	if _, err := store.Open(context.Background(), uri, 1, []byte("struct S {\n  1: string name,\n}\n")); err != nil {
		t.Fatalf("Open: %v", err)
	}

	var observed syntax.ReparseEvent
	restoreObserver := syntax.SetReparseObserverForTesting(func(ev syntax.ReparseEvent) {
		observed = ev
	})
	defer restoreObserver()

	_, err := store.Change(context.Background(), uri, 2, []TextDocumentContentChangeEvent{{
		Range: &Range{
			Start: Position{Line: 1, Character: 12},
			End:   Position{Line: 1, Character: 16},
		},
		Text: "xname",
	}})
	if err != nil {
		t.Fatalf("Change: %v", err)
	}
	if observed.Mode != "incremental" || !observed.ProvidedOldTree || observed.AppliedTreeEdits != 1 {
		t.Fatalf("unexpected incremental event: %+v", observed)
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

func TestSnapshotStoreAndServerCanonicalizeURIVariants(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	canonicalPath := filepath.Join(root, "demo.thrift")
	rawURI := "file://" + filepath.ToSlash(root) + "/nested/../demo.thrift"
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}

	canonicalURI, _, err := index.CanonicalizeDocumentURI(canonicalPath)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI: %v", err)
	}

	store := NewSnapshotStore()
	if _, err := store.Open(context.Background(), rawURI, 1, []byte("struct S {\n  1: string a\n}\n")); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := store.Snapshot(canonicalURI); !ok {
		t.Fatal("expected canonical snapshot lookup to succeed")
	}
	if _, err := store.Change(context.Background(), canonicalURI, 2, []TextDocumentContentChangeEvent{{Text: "struct S {\n  1: string b\n}\n"}}); err != nil {
		t.Fatalf("Change: %v", err)
	}
	if snap, ok := store.Snapshot(rawURI); !ok || !strings.Contains(string(snap.Tree.Source), "string b") {
		t.Fatalf("raw URI lookup failed after canonical change: ok=%v snap=%v", ok, snap)
	}
	store.Close(rawURI)
	if _, ok := store.Snapshot(canonicalURI); ok {
		t.Fatal("expected canonical snapshot removed after raw close")
	}

	s := NewServer()
	if err := s.DidOpen(context.Background(), DidOpenParams{
		TextDocument: TextDocumentItem{URI: rawURI, Version: 1, Text: "struct S {\n  1: string a\n}\n"},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if err := s.DidChange(context.Background(), DidChangeParams{
		TextDocument: VersionedTextDocumentIdentifier{URI: canonicalURI, Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{{
			Text: "struct S {\n  1: string c\n}\n",
		}},
	}); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	if snap, ok := s.Store().Snapshot(rawURI); !ok || !strings.Contains(string(snap.Tree.Source), "string c") {
		t.Fatalf("server snapshot after canonical change: ok=%v snap=%v", ok, snap)
	}
	if err := s.DidClose(context.Background(), DidCloseParams{
		TextDocument: TextDocumentIdentifier{URI: rawURI},
	}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if _, ok := s.Store().Snapshot(canonicalURI); ok {
		t.Fatal("expected canonical snapshot removed after raw DidClose")
	}
}
