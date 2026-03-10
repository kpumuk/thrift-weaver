package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

func BenchmarkSummarizeTree(b *testing.B) {
	root := testutil.WorkspaceFixturePath(b, "navigation")
	path := filepath.Join(root, "main.thrift")
	src := testutil.ReadFile(b, path)
	uri, key, err := CanonicalizeDocumentURI(path)
	if err != nil {
		b.Fatalf("CanonicalizeDocumentURI: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: uri, Version: -1})
		if err != nil {
			b.Fatalf("Parse: %v", err)
		}
		if _, err := SummarizeTree(key, DocumentInput{URI: uri, Version: -1, Source: src}, tree); err != nil {
			tree.Close()
			b.Fatalf("SummarizeTree: %v", err)
		}
		tree.Close()
	}
}

func BenchmarkManagerIncrementalWorkspaceRebuild(b *testing.B) {
	root := testutil.CopyWorkspaceFixture(b, "shadowing")
	manager := NewManager(Options{WorkspaceRoots: []string{root}})
	defer manager.Close()
	manager.setRescanIntervalForTesting(time.Hour)

	if err := manager.RescanWorkspace(context.Background()); err != nil {
		b.Fatalf("RescanWorkspace: %v", err)
	}

	sharedPath := filepath.Join(root, "shared.thrift")
	variants := [][]byte{
		[]byte("struct User {\n  1: string name,\n}\n"),
		[]byte("struct Person {\n  1: string name,\n}\n"),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := manager.UpsertOpenDocumentWithReason(context.Background(), DocumentInput{
			URI:        sharedPath,
			Version:    int32(i + 1),
			Generation: uint64(i + 1),
			Source:     variants[i%len(variants)],
		}, RebuildReasonChange); err != nil {
			b.Fatalf("UpsertOpenDocumentWithReason: %v", err)
		}
	}
}

func BenchmarkDefinitionQuery(b *testing.B) {
	manager, doc, pos := benchmarkQuerySetup(b, "navigation", "main.thrift", "types.User input")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := manager.Definition(context.Background(), doc, pos); err != nil {
			b.Fatalf("Definition: %v", err)
		}
	}
}

func BenchmarkReferencesQuery(b *testing.B) {
	manager, doc, pos := benchmarkQuerySetup(b, "navigation", "types.thrift", "User {")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := manager.References(context.Background(), doc, pos, true); err != nil {
			b.Fatalf("References: %v", err)
		}
	}
}

func BenchmarkRenamePlan(b *testing.B) {
	manager, doc, pos := benchmarkQuerySetup(b, "rename", "main.thrift", "shared.User user")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := manager.Rename(context.Background(), doc, pos, "Person"); err != nil {
			b.Fatalf("Rename: %v", err)
		}
	}
}

func BenchmarkFirstOpenClosurePublication(b *testing.B) {
	root, mainPath, mainSource := benchmarkLazyDiscoveryWorkspace(b)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		manager := benchmarkLazyDiscoveryManager(b, root, mainPath, mainSource)
		snap := mustSnapshotForBenchmark(b, manager)
		if snap.DiscoveryComplete {
			manager.Close()
			b.Fatal("expected first-open closure snapshot to stay discovery-incomplete")
		}
		if len(snap.Documents) != 2 {
			manager.Close()
			b.Fatalf("first-open closure indexed %d documents, want 2", len(snap.Documents))
		}
		manager.Close()
	}
}

func BenchmarkBackgroundDiscoveryWidening(b *testing.B) {
	root, mainPath, mainSource := benchmarkLazyDiscoveryWorkspace(b)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		manager := benchmarkLazyDiscoveryManager(b, root, mainPath, mainSource)
		if err := manager.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
			manager.Close()
			b.Fatalf("RescanWorkspaceWithReason: %v", err)
		}
		snap := mustSnapshotForBenchmark(b, manager)
		if !snap.DiscoveryComplete {
			manager.Close()
			b.Fatal("expected background discovery to complete after workspace rescan")
		}
		if len(snap.Documents) != 252 {
			manager.Close()
			b.Fatalf("background discovery indexed %d documents, want 252", len(snap.Documents))
		}
		manager.Close()
	}
}

func benchmarkLazyDiscoveryWorkspace(b testing.TB) (root string, mainPath string, mainSource []byte) {
	b.Helper()
	root = b.TempDir()
	mainPath = filepath.Join(root, "main.thrift")
	sharedPath := filepath.Join(root, "shared.thrift")
	mainSource = []byte("include \"shared.thrift\"\n\nstruct Holder {\n  1: shared.User user,\n}\n")
	if err := os.WriteFile(mainPath, mainSource, 0o600); err != nil {
		b.Fatalf("WriteFile(%s): %v", mainPath, err)
	}
	if err := os.WriteFile(sharedPath, []byte("struct User {\n  1: string name,\n}\n"), 0o600); err != nil {
		b.Fatalf("WriteFile(%s): %v", sharedPath, err)
	}
	for i := range 250 {
		path := filepath.Join(root, fmt.Sprintf("file-%03d.thrift", i))
		src := fmt.Sprintf("struct Type%03d {\n  1: string name,\n}\n", i)
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			b.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	return root, mainPath, mainSource
}

func benchmarkLazyDiscoveryManager(tb testing.TB, root string, mainPath string, mainSource []byte) *Manager {
	tb.Helper()
	manager := NewManager(Options{WorkspaceRoots: []string{root}})
	manager.setRescanIntervalForTesting(time.Hour)
	if err := manager.UpsertOpenDocumentWithReason(context.Background(), DocumentInput{
		URI:        mainPath,
		Version:    1,
		Generation: 1,
		Source:     mainSource,
	}, RebuildReasonOpen); err != nil {
		manager.Close()
		tb.Fatalf("UpsertOpenDocumentWithReason: %v", err)
	}
	if err := manager.RefreshOpenDocumentClosureWithReason(context.Background(), RebuildReasonOpen); err != nil {
		manager.Close()
		tb.Fatalf("RefreshOpenDocumentClosureWithReason: %v", err)
	}
	return manager
}

func benchmarkQuerySetup(b testing.TB, fixture, relPath, needle string) (*Manager, QueryDocument, text.UTF16Position) {
	b.Helper()
	root := testutil.CopyWorkspaceFixture(b, fixture)
	manager := NewManager(Options{WorkspaceRoots: []string{root}})
	b.Cleanup(manager.Close)
	manager.setRescanIntervalForTesting(time.Hour)
	if err := manager.RescanWorkspace(context.Background()); err != nil {
		b.Fatalf("RescanWorkspace: %v", err)
	}

	path := filepath.Join(root, relPath)
	doc := mustDocumentForBenchmark(b, mustSnapshotForBenchmark(b, manager), path)
	pos := benchmarkUTF16PositionForSubstring(b, testutil.ReadFile(b, path), needle)
	return manager, QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos
}

func mustSnapshotForBenchmark(tb testing.TB, m *Manager) *WorkspaceSnapshot {
	tb.Helper()
	snap, ok := m.Snapshot()
	if !ok || snap == nil {
		tb.Fatal("expected workspace snapshot")
	}
	return snap
}

func mustDocumentForBenchmark(tb testing.TB, snap *WorkspaceSnapshot, path string) *DocumentSummary {
	tb.Helper()
	_, key, err := CanonicalizeDocumentURI(path)
	if err != nil {
		tb.Fatalf("CanonicalizeDocumentURI(%s): %v", path, err)
	}
	doc := snap.Documents[key]
	if doc == nil {
		tb.Fatalf("missing document for %s", path)
	}
	return doc
}

func benchmarkUTF16PositionForSubstring(tb testing.TB, src []byte, needle string) text.UTF16Position {
	tb.Helper()
	start := strings.Index(string(src), needle)
	if start < 0 {
		tb.Fatalf("substring %q not found", needle)
	}
	li := text.NewLineIndex(src)
	pos, err := li.OffsetToUTF16Position(text.ByteOffset(start))
	if err != nil {
		tb.Fatalf("OffsetToUTF16Position(%q): %v", needle, err)
	}
	return pos
}
