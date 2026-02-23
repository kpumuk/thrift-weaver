package treesitter

import (
	"context"
	"testing"
)

func TestParserParsesSimpleFixture(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser() error = %v", err)
	}
	defer p.Close()

	tree, err := p.Parse(context.Background(), []byte("struct User { 1: string name, }"), nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	defer tree.Close()

	root := tree.Root()
	if got := root.Kind(); got != "source_file" {
		t.Fatalf("root.Kind() = %q, want source_file", got)
	}
	if root.ChildCount() == 0 {
		t.Fatal("expected root to have children")
	}
}
