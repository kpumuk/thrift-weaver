package lsp

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestServerConcurrentWorkspaceOperations(t *testing.T) {
	root := testutil.CopyWorkspaceFixture(t, "rename")
	rootURI := mustCanonicalURI(t, root)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainURI := mustCanonicalURI(t, mainPath)
	sharedURI := mustCanonicalURI(t, sharedPath)
	mainText := string(testutil.ReadFile(t, mainPath))
	sharedText := string(testutil.ReadFile(t, sharedPath))
	renamePos := mustLSPPositionForSubstring(t, []byte(mainText), "shared.User user")

	s := NewServer()
	s.setLintDebounceForTesting(5 * time.Millisecond)
	var out bytes.Buffer
	s.attachRuntime(context.Background(), &out)
	defer s.detachRuntime()

	if _, err := s.Initialize(context.Background(), InitializeParams{
		WorkspaceFolders: []WorkspaceFolder{{URI: rootURI}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := s.DidOpen(context.Background(), DidOpenParams{
		TextDocument: TextDocumentItem{URI: mainURI, Version: 1, Text: mainText},
	}); err != nil {
		t.Fatalf("DidOpen main: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var nextVersion atomic.Int32
	nextVersion.Store(1)

	wg.Add(4)
	go func() {
		defer wg.Done()
		texts := []string{mainText, mainText + "\n"}
		for i := 0; ctx.Err() == nil; i++ {
			version := nextVersion.Add(1)
			_ = s.DidChange(context.Background(), DidChangeParams{
				TextDocument: VersionedTextDocumentIdentifier{URI: mainURI, Version: version},
				ContentChanges: []TextDocumentContentChangeEvent{{
					Text: texts[i%len(texts)],
				}},
			})
		}
	}()

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_, _ = s.Definition(context.Background(), DefinitionParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     renamePos,
			})
			_, _ = s.References(context.Background(), ReferenceParams{
				TextDocumentPositionParams: TextDocumentPositionParams{
					TextDocument: TextDocumentIdentifier{URI: mainURI},
					Position:     renamePos,
				},
				Context: ReferenceContext{IncludeDeclaration: true},
			})
			_, _ = s.PrepareRename(context.Background(), PrepareRenameParams{
				TextDocument: TextDocumentIdentifier{URI: mainURI},
				Position:     renamePos,
			})
			_, _ = s.Rename(context.Background(), RenameParams{
				TextDocumentPositionParams: TextDocumentPositionParams{
					TextDocument: TextDocumentIdentifier{URI: mainURI},
					Position:     renamePos,
				},
				NewName: "Person",
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil; i++ {
			if err := s.DidOpen(context.Background(), DidOpenParams{
				TextDocument: TextDocumentItem{URI: sharedURI, Version: int32(i + 1), Text: sharedText},
			}); err == nil {
				_ = s.DidClose(context.Background(), DidCloseParams{
					TextDocument: TextDocumentIdentifier{URI: sharedURI},
				})
			}
		}
	}()

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if manager := s.workspaceManager(); manager != nil {
				_ = manager.RescanWorkspaceWithReason(context.Background(), index.RebuildReasonWatch)
			}
			_ = s.publishDiagnosticsForURI(context.Background(), mainURI)
		}
	}()

	wg.Wait()

	if err := s.DidClose(context.Background(), DidCloseParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
	}); err != nil {
		t.Fatalf("DidClose main: %v", err)
	}
}
