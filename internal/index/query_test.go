package index

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

func TestManagerDefinitionReferencesAndWorkspaceSymbols(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	typesPath := filepath.Join(root, "types.thrift")
	mainDoc := mustDocument(t, mustSnapshot(t, m), mainPath)
	mainSource := testutil.ReadFile(t, mainPath)
	userRefPos := mustUTF16PositionForSubstring(t, mainSource, "types.User input")

	definitions, _, err := m.Definition(context.Background(), QueryDocument{
		URI:        mainDoc.URI,
		Version:    mainDoc.Version,
		Generation: mainDoc.Generation,
	}, userRefPos)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("len(Definition)=%d, want 1", len(definitions))
	}
	if got := locationText(t, definitions[0]); got != "User" {
		t.Fatalf("definition text=%q, want %q", got, "User")
	}

	typesDoc := mustDocument(t, mustSnapshot(t, m), typesPath)
	declPos := mustUTF16PositionForSubstring(t, testutil.ReadFile(t, typesPath), "User {")
	references, _, err := m.References(context.Background(), QueryDocument{
		URI:        typesDoc.URI,
		Version:    typesDoc.Version,
		Generation: typesDoc.Generation,
	}, declPos, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(references) != 3 {
		t.Fatalf("len(References)=%d, want 3", len(references))
	}
	gotTexts := []string{
		locationText(t, references[0]),
		locationText(t, references[1]),
		locationText(t, references[2]),
	}
	wantTexts := []string{"types.User", "types.User", "User"}
	for i := range wantTexts {
		if gotTexts[i] != wantTexts[i] {
			t.Fatalf("reference %d text=%q, want %q", i, gotTexts[i], wantTexts[i])
		}
	}

	symbols, _, err := m.WorkspaceSymbols(context.Background(), "user")
	if err != nil {
		t.Fatalf("WorkspaceSymbols: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("len(WorkspaceSymbols)=%d, want 2", len(symbols))
	}
	if symbols[0].Name != "User" || symbols[1].Name != "UserError" {
		t.Fatalf("workspace symbols=%+v, want User and UserError", symbols)
	}
}

func TestManagerDefinitionAndWorkspaceSymbolsWorkBeforeDiscoveryCompletes(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	mainPath := filepath.Join(root, "main.thrift")
	mainSource := testutil.ReadFile(t, mainPath)
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

	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	userPos := mustUTF16PositionForSubstring(t, mainSource, "types.User input")

	definitions, meta, err := m.Definition(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, userPos)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if meta.DiscoveryComplete {
		t.Fatal("definition query should report incomplete discovery before workspace rescan")
	}
	if len(definitions) != 1 || locationText(t, definitions[0]) != "User" {
		t.Fatalf("definition results=%+v", definitions)
	}

	if _, meta, err = m.References(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, userPos, true); !errors.Is(err, ErrWorkspaceIncomplete) {
		t.Fatalf("References error=%v, want %v", err, ErrWorkspaceIncomplete)
	}
	if meta.DiscoveryComplete {
		t.Fatal("references query should report incomplete discovery before workspace rescan")
	}

	symbols, meta, err := m.WorkspaceSymbols(context.Background(), "user")
	if err != nil {
		t.Fatalf("WorkspaceSymbols: %v", err)
	}
	if meta.DiscoveryComplete {
		t.Fatal("workspace symbols should report incomplete discovery before workspace rescan")
	}
	if len(symbols) != 2 {
		t.Fatalf("workspace symbols=%+v, want two loaded symbols", symbols)
	}

	if err := m.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	doc = mustDocument(t, mustSnapshot(t, m), mainPath)
	references, meta, err := m.References(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, userPos, true)
	if err != nil {
		t.Fatalf("References after rescan: %v", err)
	}
	if !meta.DiscoveryComplete {
		t.Fatal("references query should report complete discovery after workspace rescan")
	}
	if len(references) != 3 {
		t.Fatalf("len(References)=%d, want 3", len(references))
	}
}

func TestManagerDefinitionAndReferencesReturnEmptyForUnresolvedBinding(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "missing_include")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	pos := mustUTF16PositionForSubstring(t, testutil.ReadFile(t, mainPath), "missing.User")

	definitions, _, err := m.Definition(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("Definition returned %+v, want empty", definitions)
	}

	references, _, err := m.References(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("References returned %+v, want empty", references)
	}
}

func TestManagerDefinitionRejectsContentModified(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "navigation")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	source := testutil.ReadFile(t, mainPath)
	if err := m.UpsertOpenDocument(context.Background(), DocumentInput{
		URI:        doc.URI,
		Version:    1,
		Generation: 1,
		Source:     source,
	}); err != nil {
		t.Fatalf("UpsertOpenDocument: %v", err)
	}

	_, _, err := m.Definition(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, mustUTF16PositionForSubstring(t, source, "types.User input"))
	if !errors.Is(err, ErrContentModified) {
		t.Fatalf("Definition error=%v, want %v", err, ErrContentModified)
	}
}

func TestManagerPrepareRenameAndRename(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	source := testutil.ReadFile(t, mainPath)
	pos := mustUTF16PositionForSubstring(t, source, "shared.User user")

	prep, _, err := m.PrepareRename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos)
	if err != nil {
		t.Fatalf("PrepareRename: %v", err)
	}
	if prep.Placeholder != "User" {
		t.Fatalf("PrepareRename placeholder=%q, want %q", prep.Placeholder, "User")
	}
	if got := string(source[prep.Span.Start:prep.Span.End]); got != "User" {
		t.Fatalf("PrepareRename span text=%q, want %q", got, "User")
	}

	result, _, err := m.Rename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos, "Person")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("Rename documents=%d, want 2", len(result.Documents))
	}
	if result.Documents[0].URI >= result.Documents[1].URI {
		t.Fatalf("Rename documents out of order: %+v", result.Documents)
	}
	if result.Documents[0].Version != nil || result.Documents[1].Version != nil {
		t.Fatalf("on-disk rename should not set document versions: %+v", result.Documents)
	}
	if len(result.Documents[0].Edits) != 2 || result.Documents[0].Edits[0].Span.Start <= result.Documents[0].Edits[1].Span.Start {
		t.Fatalf("main edits should be descending and contain two replacements: %+v", result.Documents[0].Edits)
	}

	renamedMain := applyRenameEdits(t, mainPath, result.Documents[0].Edits)
	if string(renamedMain) != "include \"shared.thrift\"\n\nstruct Holder {\n  1: shared.Person user,\n  2: shared.Person backup,\n}\n" {
		t.Fatalf("renamed main = %q", renamedMain)
	}
	renamedShared := applyRenameEdits(t, filepath.Join(root, "shared.thrift"), result.Documents[1].Edits)
	if string(renamedShared) != "struct Person {\n  1: string name,\n}\n" {
		t.Fatalf("renamed shared = %q", renamedShared)
	}
}

func TestManagerRenameRequiresDiscoveryComplete(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	mainPath := filepath.Join(root, "main.thrift")
	mainSource := testutil.ReadFile(t, mainPath)
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

	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	pos := mustUTF16PositionForSubstring(t, mainSource, "shared.User user")

	prep, meta, err := m.PrepareRename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos)
	if err != nil {
		t.Fatalf("PrepareRename: %v", err)
	}
	if meta.DiscoveryComplete {
		t.Fatal("prepareRename should report incomplete discovery before workspace rescan")
	}
	if prep.Placeholder != "User" {
		t.Fatalf("PrepareRename placeholder=%q, want %q", prep.Placeholder, "User")
	}

	result, meta, err := m.Rename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos, "Person")
	if !errors.Is(err, ErrWorkspaceIncomplete) {
		t.Fatalf("Rename error=%v, want %v", err, ErrWorkspaceIncomplete)
	}
	if meta.DiscoveryComplete {
		t.Fatal("rename should report incomplete discovery before workspace rescan")
	}
	if result == nil || result.Placeholder != "User" {
		t.Fatalf("Rename result=%+v, want placeholder User", result)
	}

	if err := m.RescanWorkspaceWithReason(context.Background(), RebuildReasonManualRescan); err != nil {
		t.Fatalf("RescanWorkspaceWithReason: %v", err)
	}

	doc = mustDocument(t, mustSnapshot(t, m), mainPath)
	result, meta, err = m.Rename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, pos, "Person")
	if err != nil {
		t.Fatalf("Rename after rescan: %v", err)
	}
	if !meta.DiscoveryComplete {
		t.Fatal("rename should report complete discovery after workspace rescan")
	}
	if len(result.Documents) != 2 {
		t.Fatalf("Rename documents=%d, want 2", len(result.Documents))
	}
}

func TestManagerRenameBlocksInvalidName(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "rename")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	result, _, err := m.Rename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, mustUTF16PositionForSubstring(t, testutil.ReadFile(t, mainPath), "shared.User user"), "struct")
	if !errors.Is(err, ErrRenameBlocked) {
		t.Fatalf("Rename error=%v, want %v", err, ErrRenameBlocked)
	}
	if len(result.Blockers) != 1 || result.Blockers[0].Code != DiagnosticRenameInvalidName {
		t.Fatalf("Rename blockers=%+v, want invalid-name blocker", result.Blockers)
	}
}

func TestManagerPrepareRenameBlocksAmbiguousReference(t *testing.T) {
	t.Parallel()

	root := testutil.CopyWorkspaceFixture(t, "duplicate_alias")
	m := NewManager(Options{WorkspaceRoots: []string{root}})
	defer m.Close()
	m.setRescanIntervalForTesting(5 * time.Minute)

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	mainPath := filepath.Join(root, "main.thrift")
	doc := mustDocument(t, mustSnapshot(t, m), mainPath)
	result, _, err := m.PrepareRename(context.Background(), QueryDocument{
		URI:        doc.URI,
		Version:    doc.Version,
		Generation: doc.Generation,
	}, mustUTF16PositionForSubstring(t, testutil.ReadFile(t, mainPath), "shared.User"))
	if !errors.Is(err, ErrRenameBlocked) {
		t.Fatalf("PrepareRename error=%v, want %v", err, ErrRenameBlocked)
	}
	if len(result.Blockers) != 1 || result.Blockers[0].Code != DiagnosticRenameTargetAmbiguous {
		t.Fatalf("PrepareRename blockers=%+v, want ambiguous blocker", result.Blockers)
	}
}

func mustUTF16PositionForSubstring(t *testing.T, src []byte, needle string) text.UTF16Position {
	t.Helper()
	start := strings.Index(string(src), needle)
	if start < 0 {
		t.Fatalf("substring %q not found", needle)
	}
	li := text.NewLineIndex(src)
	pos, err := li.OffsetToUTF16Position(text.ByteOffset(start))
	if err != nil {
		t.Fatalf("OffsetToUTF16Position(%q): %v", needle, err)
	}
	return pos
}

func locationText(t *testing.T, location Location) string {
	t.Helper()
	path, err := filePathFromDocumentURI(location.URI)
	if err != nil {
		t.Fatalf("filePathFromDocumentURI(%s): %v", location.URI, err)
	}
	src := testutil.ReadFile(t, path)
	if !location.Span.IsValid() || int(location.Span.End) > len(src) {
		t.Fatalf("invalid location span %v for %s", location.Span, location.URI)
	}
	return string(src[location.Span.Start:location.Span.End])
}

func applyRenameEdits(t *testing.T, path string, edits []text.ByteEdit) []byte {
	t.Helper()
	src := testutil.ReadFile(t, path)
	out, err := text.ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("ApplyEdits(%s): %v", path, err)
	}
	return out
}
