package index

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestManagerConcurrentLazyDiscoveryOperations(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()

	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainSource := testutil.ReadFile(t, mainPath)
	mainVariants := [][]byte{
		mainSource,
		append(append([]byte(nil), mainSource...), '\n'),
	}
	sharedVariants := [][]byte{
		[]byte("struct User {\n  1: string name,\n}\n"),
		[]byte("struct User {\n  1: string name,\n  2: string alias,\n}\n"),
	}

	if err := m.UpsertOpenDocumentWithReason(context.Background(), DocumentInput{
		URI:        mainPath,
		Version:    1,
		Generation: 1,
		Source:     mainSource,
	}, RebuildReasonOpen); err != nil {
		t.Fatalf("UpsertOpenDocumentWithReason: %v", err)
	}
	if err := m.RefreshOpenDocumentClosureWithReason(context.Background(), RebuildReasonOpen); err != nil {
		t.Fatalf("RefreshOpenDocumentClosureWithReason: %v", err)
	}

	_, mainKey, err := CanonicalizeDocumentURI(mainPath)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(%s): %v", mainPath, err)
	}
	pos := mustUTF16PositionForSubstring(t, mainSource, "shared.User user")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		version := int32(1)
		generation := uint64(1)
		for i := 0; ctx.Err() == nil; i++ {
			version++
			generation++
			_ = m.UpsertOpenDocumentWithReason(ctx, DocumentInput{
				URI:        mainPath,
				Version:    version,
				Generation: generation,
				Source:     mainVariants[i%len(mainVariants)],
			}, RebuildReasonChange)
			_ = m.RefreshOpenDocumentClosureWithReason(ctx, RebuildReasonChange)
		}
	}()

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_ = m.RescanWorkspaceWithReason(ctx, RebuildReasonWatch)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil; i++ {
			_ = os.WriteFile(sharedPath, sharedVariants[i%len(sharedVariants)], 0o600)
			_ = m.RefreshDocumentWithReason(ctx, sharedPath, false, RebuildReasonWatch)
		}
	}()

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			snap, ok := m.Snapshot()
			if !ok || snap == nil {
				continue
			}
			doc := snap.Documents[mainKey]
			if doc == nil {
				continue
			}
			queryDoc := QueryDocument{
				URI:        doc.URI,
				Version:    doc.Version,
				Generation: doc.Generation,
			}
			_, _, _ = m.Definition(ctx, queryDoc, pos)
			_, _, _ = m.References(ctx, queryDoc, pos, true)
			_, _, _ = m.PrepareRename(ctx, queryDoc, pos)
			_, _, _ = m.Rename(ctx, queryDoc, pos, "Person")
		}
	}()

	wg.Wait()
}
