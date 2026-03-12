package index

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestParseAndSummarizeNavigationFixture(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.WorkspaceFixturePath(t, "navigation"), "main.thrift")
	displayURI, key, err := CanonicalizeDocumentURI(path)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI: %v", err)
	}

	sum, err := ParseAndSummarize(context.Background(), key, DocumentInput{
		URI:        displayURI,
		Version:    7,
		Generation: 3,
		Source:     testutil.ReadFile(t, path),
	})
	if err != nil {
		t.Fatalf("ParseAndSummarize: %v", err)
	}

	if got := len(sum.Includes); got != 1 {
		t.Fatalf("len(Includes)=%d, want 1", got)
	}
	if got := sum.Includes[0].Alias; got != "types" {
		t.Fatalf("include alias=%q, want %q", got, "types")
	}
	if got := len(sum.Declarations); got != 1 {
		t.Fatalf("len(Declarations)=%d, want 1", got)
	}
	if got := sum.Declarations[0].Name; got != "ExampleService" {
		t.Fatalf("declaration name=%q, want %q", got, "ExampleService")
	}

	counts := map[ReferenceKind]int{}
	for _, ref := range sum.References {
		if ref.Qualifier != "types" {
			t.Fatalf("reference qualifier=%q, want %q", ref.Qualifier, "types")
		}
		counts[ref.Context]++
	}
	if counts[ReferenceKindType] != 2 {
		t.Fatalf("type reference count=%d, want 2", counts[ReferenceKindType])
	}
	if counts[ReferenceKindServiceExtends] != 1 {
		t.Fatalf("service extends count=%d, want 1", counts[ReferenceKindServiceExtends])
	}
	if counts[ReferenceKindThrowsType] != 1 {
		t.Fatalf("throws reference count=%d, want 1", counts[ReferenceKindThrowsType])
	}
}
