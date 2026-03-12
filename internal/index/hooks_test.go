package index

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

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

func TestManagerHooksReportLazyDiscoveryState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("vendor/\n"), 0o600); err != nil {
		t.Fatalf("WriteFile .gitignore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o750); err != nil {
		t.Fatalf("MkdirAll vendor: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	mainSource := []byte("include \"vendor/shared.thrift\"\n\nstruct Holder {\n  1: shared.User user,\n}\n")
	sharedPath := filepath.Join(root, "vendor", "shared.thrift")
	extraPath := filepath.Join(root, "extra.thrift")
	if err := os.WriteFile(mainPath, mainSource, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", mainPath, err)
	}
	if err := os.WriteFile(sharedPath, []byte("struct User {\n  1: string name,\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", sharedPath, err)
	}
	if err := os.WriteFile(extraPath, []byte("struct Extra {\n  1: string name,\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", extraPath, err)
	}

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
			QueueDepth: func() int { return 2 },
		},
	})
	defer m.Close()

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
	if err := m.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	pos := mustUTF16PositionForSubstring(t, mainSource, "shared.User user")
	if _, _, err := m.Definition(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos); err != nil {
		t.Fatalf("Definition: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindRebuild &&
			event.Reason == RebuildReasonOpen &&
			event.DirectLoads == 1 &&
			event.DirectDocuments == 1 &&
			event.OpportunisticDocuments == 0 &&
			!event.DiscoveryComplete &&
			event.BackgroundQueueDepth == 2
	}) {
		t.Fatalf("missing direct-load rebuild event in %+v", events)
	}
	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindRebuild &&
			event.Reason == RebuildReasonManualRescan &&
			event.DiscoveredFiles >= 2 &&
			event.GitIgnoreSkippedPaths > 0 &&
			event.DirectDocuments == 1 &&
			event.OpportunisticDocuments >= 2 &&
			event.DiscoveryComplete &&
			event.BackgroundQueueDepth == 2
	}) {
		t.Fatalf("missing opportunistic rebuild event in %+v", events)
	}
	if !hasEvent(events, func(event Event) bool {
		return event.Kind == EventKindQuery &&
			event.Method == "definition" &&
			event.DiscoveryComplete &&
			event.BackgroundQueueDepth == 2
	}) {
		t.Fatalf("missing query discovery metadata in %+v", events)
	}
}

func hasEvent(events []Event, match func(Event) bool) bool {
	return slices.ContainsFunc(events, match)
}
