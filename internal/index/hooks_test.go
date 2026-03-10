package index

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestManagerHooksEmitRebuildQueryAndRenameBlockers(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	var (
		mu     sync.Mutex
		events []Event
	)

	m := NewManager(Options{
		WorkspaceRoots: []string{root},
		Hooks: Hooks{
			OnEvent: func(event Event) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, event)
			},
		},
	})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	pos := mustUTF16PositionForSubstring(t, testutil.ReadFile(t, mainPath), "shared.User user")
	if _, _, err := m.Definition(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos); err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if _, _, err := m.Rename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos, "struct"); err == nil {
		t.Fatal("Rename should fail for invalid name")
	}

	mu.Lock()
	defer mu.Unlock()

	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindRebuild && event.Reason == RebuildReasonManualRescan && event.IndexedDocuments >= 2
	}) {
		t.Fatalf("missing rebuild event in %+v", events)
	}
	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindQuery && event.Method == "definition" && event.WorkspaceGeneration > 0
	}) {
		t.Fatalf("missing query event in %+v", events)
	}
	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindRenameBlockers && event.RenameBlockers[DiagnosticRenameInvalidName] == 1
	}) {
		t.Fatalf("missing rename blocker event in %+v", events)
	}
}

func hasEvent(events []Event, match func(Event) bool) bool {
	return slices.ContainsFunc(events, match)
}
