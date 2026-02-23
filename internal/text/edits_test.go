package text

import (
	"bytes"
	"testing"
)

func TestApplyEditsNonOverlappingAndUnsorted(t *testing.T) {
	t.Parallel()

	src := []byte("abcdef")
	edits := []ByteEdit{
		{Span: Span{Start: 4, End: 6}, NewText: []byte("XY")},
		{Span: Span{Start: 1, End: 3}, NewText: []byte("12")},
	}

	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("ApplyEdits error = %v", err)
	}
	if string(got) != "a12dXY" {
		t.Fatalf("ApplyEdits() = %q, want %q", got, "a12dXY")
	}
}

func TestApplyEditsInsertDeleteTouchingSpans(t *testing.T) {
	t.Parallel()

	src := []byte("abcdef")
	edits := []ByteEdit{
		{Span: Span{Start: 0, End: 0}, NewText: []byte("<")},
		{Span: Span{Start: 3, End: 3}, NewText: []byte("|")},
		{Span: Span{Start: 6, End: 6}, NewText: []byte(">")},
		{Span: Span{Start: 1, End: 2}, NewText: nil}, // delete "b"
	}

	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("ApplyEdits error = %v", err)
	}
	if string(got) != "<ac|def>" {
		t.Fatalf("ApplyEdits() = %q, want %q", got, "<ac|def>")
	}
}

func TestApplyEditsNoEditsReturnsCopy(t *testing.T) {
	t.Parallel()

	src := []byte("abc")
	got, err := ApplyEdits(src, nil)
	if err != nil {
		t.Fatalf("ApplyEdits error = %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("ApplyEdits() = %q, want %q", got, src)
	}
	if len(got) > 0 && &got[0] == &src[0] {
		t.Fatal("ApplyEdits() should return a copy when no edits are provided")
	}
}

func TestValidateEditsErrors(t *testing.T) {
	t.Parallel()

	if err := ValidateEdits(5, []ByteEdit{{Span: Span{Start: 4, End: 6}}}); err == nil {
		t.Fatal("expected out-of-bounds error")
	}
	if err := ValidateEdits(5, []ByteEdit{{Span: Span{Start: 3, End: 2}}}); err == nil {
		t.Fatal("expected invalid span error")
	}
	if err := ValidateEdits(5, []ByteEdit{
		{Span: Span{Start: 1, End: 3}},
		{Span: Span{Start: 2, End: 4}},
	}); err == nil {
		t.Fatal("expected overlapping edits error")
	}
}
