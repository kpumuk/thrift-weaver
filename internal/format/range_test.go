package format

import (
	"bytes"
	"context"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

func TestRangeWidensToFieldAncestorAndReturnsEdit(t *testing.T) {
	t.Parallel()

	src := []byte("struct Foo{1:required i32 id;2: optional string name(ann='x'),3: byte flag = 1;}\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "x.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	start := bytes.Index(src, []byte("ann"))
	if start < 0 {
		t.Fatal("failed to find range marker")
	}
	r := text.Span{Start: text.ByteOffset(start), End: text.ByteOffset(start + 3)}

	got, err := Range(context.Background(), tree, r, Options{})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(got.Edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(got.Edits))
	}

	out, err := text.ApplyEdits(src, got.Edits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	want := []byte("struct Foo{1:required i32 id;2: optional string name(ann = 'x'),3: byte flag = 1;}\n")
	if !bytes.Equal(out, want) {
		t.Fatalf("range formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

func TestRangeWidensWhitespaceSelectionToBlockAncestor(t *testing.T) {
	t.Parallel()

	src := []byte("struct Foo{1:required i32 id;  2: optional string name;}\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "x.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	needle := []byte(";  2:")
	pos := bytes.Index(src, needle)
	if pos < 0 {
		t.Fatal("failed to find whitespace selection marker")
	}
	// Select the whitespace between members.
	r := text.Span{Start: text.ByteOffset(pos + 1), End: text.ByteOffset(pos + 3)}

	got, err := Range(context.Background(), tree, r, Options{})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(got.Edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(got.Edits))
	}

	edit := got.Edits[0]
	if string(src[edit.Span.Start:edit.Span.End]) != "{1:required i32 id;  2: optional string name;}" {
		t.Fatalf("expected block-span edit, got span %s text %q", edit.Span, src[edit.Span.Start:edit.Span.End])
	}
	if string(edit.NewText) != "{\n  1: required i32 id;\n  2: optional string name;\n}" {
		t.Fatalf("unexpected block formatting output %q", edit.NewText)
	}
}

func TestRangeRefusesWhenNoSafeAncestorExists(t *testing.T) {
	t.Parallel()

	src := []byte("include \"a.thrift\";\n\nnamespace go x\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "x.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	blankStart := bytes.Index(src, []byte("\n\n"))
	if blankStart < 0 {
		t.Fatal("failed to find blank line")
	}
	r := text.Span{Start: text.ByteOffset(blankStart + 1), End: text.ByteOffset(blankStart + 2)}

	res, err := Range(context.Background(), tree, r, Options{})
	if err == nil || !IsErrUnsafeToFormat(err) {
		t.Fatalf("expected ErrUnsafeToFormat, got %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected formatter blocking diagnostic")
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == DiagnosticFormatterRangeNoSafeAncestor {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q diagnostic, got %+v", DiagnosticFormatterRangeNoSafeAncestor, res.Diagnostics)
	}
}

func TestRangeRefusesUnboundedAncestorCoverage(t *testing.T) {
	t.Parallel()

	src := []byte("struct Foo{1: optional string name;}\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "x.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var field *syntax.Node
	for i := 1; i < len(tree.Nodes); i++ {
		n := &tree.Nodes[i]
		if syntax.KindName(n.Kind) == "field" {
			field = n
			break
		}
	}
	if field == nil {
		t.Fatal("field node not found")
	}
	field.Span.End--

	start := bytes.Index(src, []byte("name"))
	r := text.Span{Start: text.ByteOffset(start), End: text.ByteOffset(start + 4)}

	res, err := Range(context.Background(), tree, r, Options{})
	if err == nil || !IsErrUnsafeToFormat(err) {
		t.Fatalf("expected ErrUnsafeToFormat, got %v", err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == DiagnosticFormatterRangeUnboundedNode {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q diagnostic, got %+v", DiagnosticFormatterRangeUnboundedNode, res.Diagnostics)
	}
}
