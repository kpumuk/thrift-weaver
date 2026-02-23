package text

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
)

// ByteEdit replaces the bytes in Span with NewText.
type ByteEdit struct {
	Span    Span
	NewText []byte
}

// ValidateEdits validates edit spans against a source length and checks overlap.
// Touching spans are allowed.
func ValidateEdits(srcLen ByteOffset, edits []ByteEdit) error {
	_, err := validatedSortedEdits(srcLen, edits)
	return err
}

// ApplyEdits applies non-overlapping byte edits and returns the updated buffer.
// Edits may be provided in any order.
func ApplyEdits(src []byte, edits []ByteEdit) ([]byte, error) {
	if len(edits) == 0 {
		return slices.Clone(src), nil
	}

	sorted, err := validatedSortedEdits(ByteOffset(len(src)), edits)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	cursor := ByteOffset(0)
	for _, e := range sorted {
		out.Write(src[cursor:e.Span.Start])
		out.Write(e.NewText)
		cursor = e.Span.End
	}
	out.Write(src[cursor:])
	return out.Bytes(), nil
}

func validatedSortedEdits(srcLen ByteOffset, edits []ByteEdit) ([]ByteEdit, error) {
	if !srcLen.IsValid() {
		return nil, fmt.Errorf("invalid source length: %d", srcLen)
	}
	for _, e := range edits {
		if err := e.Span.Validate(); err != nil {
			return nil, fmt.Errorf("invalid edit span %s: %w", e.Span, err)
		}
		if e.Span.End > srcLen {
			return nil, fmt.Errorf("edit span %s exceeds source length %d", e.Span, srcLen)
		}
	}

	sorted := sortByteEdits(edits)

	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1]
		cur := sorted[i]
		if cur.Span.Start < prev.Span.End {
			return nil, fmt.Errorf("overlapping edits: %s and %s", prev.Span, cur.Span)
		}
	}
	return sorted, nil
}

func sortByteEdits(edits []ByteEdit) []ByteEdit {
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, compareByteEdits)
	return sorted
}

func compareByteEdits(a, b ByteEdit) int {
	if c := cmp.Compare(a.Span.Start, b.Span.Start); c != 0 {
		return c
	}
	return cmp.Compare(a.Span.End, b.Span.End)
}
