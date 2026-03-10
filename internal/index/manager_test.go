package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestManagerResolvesIncludesAndBindings(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "resolved_include")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(time.Hour)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	snap := mustSnapshot(t, m)
	mainDoc := mustDocument(t, snap, filepath.Join(root, "main.thrift"))
	if got := len(mainDoc.Includes); got != 1 {
		t.Fatalf("len(Includes)=%d, want 1", got)
	}
	if got := mainDoc.Includes[0].Status; got != IncludeStatusResolved {
		t.Fatalf("include status=%q, want %q", got, IncludeStatusResolved)
	}
	if got := mainDoc.References[0].Binding.Status; got != BindingStatusBound {
		t.Fatalf("binding status=%q, want %q", got, BindingStatusBound)
	}

	target := snap.SymbolsByID[mainDoc.References[0].Binding.Target]
	if target.Name != "User" || target.Kind != SymbolKindStruct {
		t.Fatalf("target=%+v, want User struct", target)
	}
}

func TestManagerReportsMissingIncludeAndAliasConflicts(t *testing.T) {
	t.Parallel()

	t.Run("missing include", func(t *testing.T) {
		t.Parallel()
		root := testutil.CopyWorkspaceFixture(t, "missing_include")
		m := NewManager(Options{WorkspaceRoots: []string{root}})
		defer m.Close()
		m.setRescanIntervalForTesting(time.Minute)

		if err := m.RescanWorkspace(context.Background()); err != nil {
			t.Fatalf("RescanWorkspace: %v", err)
		}
		snap := mustSnapshot(t, m)
		mainDoc := mustDocument(t, snap, filepath.Join(root, "main.thrift"))
		if got := mainDoc.Includes[0].Status; got != IncludeStatusMissing {
			t.Fatalf("include status=%q, want %q", got, IncludeStatusMissing)
		}
		if got := mainDoc.References[0].Binding.Status; got != BindingStatusUnresolved {
			t.Fatalf("binding status=%q, want %q", got, BindingStatusUnresolved)
		}
		if !hasDiagnostic(mainDoc.Diagnostics, DiagnosticIncludeMissing) {
			t.Fatalf("missing %s diagnostic: %+v", DiagnosticIncludeMissing, mainDoc.Diagnostics)
		}
	})

	t.Run("duplicate alias", func(t *testing.T) {
		t.Parallel()
		root := testutil.CopyWorkspaceFixture(t, "duplicate_alias")
		m := NewManager(Options{WorkspaceRoots: []string{root}})
		defer m.Close()
		m.setRescanIntervalForTesting(2 * time.Minute)

		if err := m.RescanWorkspace(context.Background()); err != nil {
			t.Fatalf("RescanWorkspace: %v", err)
		}
		snap := mustSnapshot(t, m)
		mainDoc := mustDocument(t, snap, filepath.Join(root, "main.thrift"))
		if got := mainDoc.References[0].Binding.Status; got != BindingStatusAmbiguous {
			t.Fatalf("binding status=%q, want %q", got, BindingStatusAmbiguous)
		}
		if !hasDiagnostic(mainDoc.Diagnostics, DiagnosticIncludeAliasConflict) {
			t.Fatalf("missing %s diagnostic: %+v", DiagnosticIncludeAliasConflict, mainDoc.Diagnostics)
		}
	})
}

func TestManagerBuildsCycleComponentsDeterministically(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "cycle")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(3 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	snap := mustSnapshot(t, m)
	if got := len(snap.IncludeGraph.Components); got != 1 {
		t.Fatalf("len(Components)=%d, want 1", got)
	}
	if got := len(snap.IncludeGraph.Components[0]); got != 2 {
		t.Fatalf("component size=%d, want 2", got)
	}

	for _, name := range []string{"a.thrift", "b.thrift"} {
		doc := mustDocument(t, snap, filepath.Join(root, name))
		if got := doc.References[0].Binding.Status; got != BindingStatusBound {
			t.Fatalf("%s binding status=%q, want %q", name, got, BindingStatusBound)
		}
	}
}

func TestManagerShadowingInvalidatesReverseDependentsAndKeepsPublishedSnapshotsImmutable(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "shadowing")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(4 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	snap1 := mustSnapshot(t, m)
	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainBefore := mustDocument(t, snap1, mainPath)
	if got := mainBefore.References[0].Binding.Status; got != BindingStatusBound {
		t.Fatalf("initial binding status=%q, want %q", got, BindingStatusBound)
	}

	if err := m.UpsertOpenDocument(context.Background(), DocumentInput{
		URI:        sharedPath,
		Version:    1,
		Generation: 1,
		Source:     []byte("struct Person {\n  1: string name,\n}\n"),
	}); err != nil {
		t.Fatalf("UpsertOpenDocument: %v", err)
	}

	snap2 := mustSnapshot(t, m)
	if snap2.Generation <= snap1.Generation {
		t.Fatalf("snapshot generation=%d, want > %d", snap2.Generation, snap1.Generation)
	}
	mainAfter := mustDocument(t, snap2, mainPath)
	if got := mainAfter.References[0].Binding.Status; got != BindingStatusUnresolved {
		t.Fatalf("shadowed binding status=%q, want %q", got, BindingStatusUnresolved)
	}
	if got := mainBefore.References[0].Binding.Status; got != BindingStatusBound {
		t.Fatalf("old snapshot mutated to %q, want %q", got, BindingStatusBound)
	}

	if err := m.CloseOpenDocument(context.Background(), sharedPath); err != nil {
		t.Fatalf("CloseOpenDocument: %v", err)
	}

	snap3 := mustSnapshot(t, m)
	mainRestored := mustDocument(t, snap3, mainPath)
	if got := mainRestored.References[0].Binding.Status; got != BindingStatusBound {
		t.Fatalf("restored binding status=%q, want %q", got, BindingStatusBound)
	}
}

func TestManagerRefreshOpenDocumentClosurePublishesEmptySnapshot(t *testing.T) {
	t.Parallel()

	m := NewManager(Options{WorkspaceRoots: []string{t.TempDir()}})
	defer m.Close()
	m.setRescanIntervalForTesting(time.Hour)

	if err := m.RefreshOpenDocumentClosureWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RefreshOpenDocumentClosureWithReason: %v", err)
	}

	snap := mustSnapshot(t, m)
	if snap.DiscoveryComplete {
		t.Fatal("empty direct-load snapshot should not be discovery complete")
	}
	if len(snap.Documents) != 0 {
		t.Fatalf("len(Documents)=%d, want 0", len(snap.Documents))
	}
}

func TestManagerRefreshOpenDocumentClosureBypassesGitIgnore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("vendor/\n"), 0o600); err != nil {
		t.Fatalf("WriteFile .gitignore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o750); err != nil {
		t.Fatalf("MkdirAll vendor: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "vendor", "shared.thrift")
	mainSource := []byte("include \"vendor/shared.thrift\"\n\nstruct Holder {\n  1: shared.User user,\n}\n")
	sharedSource := []byte("struct User {\n  1: string name,\n}\n")
	if err := os.WriteFile(mainPath, mainSource, 0o600); err != nil {
		t.Fatalf("WriteFile main: %v", err)
	}
	if err := os.WriteFile(sharedPath, sharedSource, 0o600); err != nil {
		t.Fatalf("WriteFile shared: %v", err)
	}

	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(time.Hour)

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

	snap := mustSnapshot(t, m)
	if snap.DiscoveryComplete {
		t.Fatal("direct-load snapshot should not be discovery complete")
	}

	mainDoc := mustDocument(t, snap, mainPath)
	if got := mainDoc.Includes[0].Status; got != IncludeStatusResolved {
		t.Fatalf("include status=%q, want %q", got, IncludeStatusResolved)
	}
	if got := mainDoc.References[0].Binding.Status; got != BindingStatusBound {
		t.Fatalf("binding status=%q, want %q", got, BindingStatusBound)
	}
	if doc := mustDocument(t, snap, sharedPath); doc == nil {
		t.Fatal("expected ignored include target to load directly")
	}

	if err := m.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	snap = mustSnapshot(t, m)
	if !snap.DiscoveryComplete {
		t.Fatal("background discovery should mark snapshot complete")
	}
	if doc := mustDocument(t, snap, sharedPath); doc == nil {
		t.Fatal("expected direct include target to remain indexed after background discovery")
	}
}

func mustSnapshot(t *testing.T, m *Manager) *WorkspaceSnapshot {
	t.Helper()
	snap, ok := m.Snapshot()
	if !ok || snap == nil {
		t.Fatal("expected published snapshot")
	}
	return snap
}

func mustDocument(t *testing.T, snap *WorkspaceSnapshot, path string) *DocumentSummary {
	t.Helper()
	_, key, err := CanonicalizeDocumentURI(path)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(%s): %v", path, err)
	}
	doc := snap.Documents[key]
	if doc == nil {
		t.Fatalf("missing document for %s", path)
	}
	return doc
}

func hasDiagnostic(diags []IndexDiagnostic, code string) bool {
	for _, diag := range diags {
		if strings.TrimSpace(diag.Code) == code {
			return true
		}
	}
	return false
}
