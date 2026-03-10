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
