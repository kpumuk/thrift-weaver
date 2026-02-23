package text

import "testing"

func TestLineIndexOffsetPointLF(t *testing.T) {
	t.Parallel()

	src := []byte("ab\ncd")
	idx := NewLineIndex(src)

	if got := idx.LineCount(); got != 2 {
		t.Fatalf("LineCount() = %d, want 2", got)
	}

	tests := map[ByteOffset]Point{
		0: {Line: 0, Column: 0},
		2: {Line: 0, Column: 2}, // before '\n'
		3: {Line: 1, Column: 0},
		5: {Line: 1, Column: 2}, // EOF
	}

	for off, want := range tests {
		got, err := idx.OffsetToPoint(off)
		if err != nil {
			t.Fatalf("OffsetToPoint(%d) error = %v", off, err)
		}
		if got != want {
			t.Fatalf("OffsetToPoint(%d) = %+v, want %+v", off, got, want)
		}

		roundTrip, err := idx.PointToOffset(got)
		if err != nil {
			t.Fatalf("PointToOffset(%+v) error = %v", got, err)
		}
		if roundTrip != off {
			t.Fatalf("PointToOffset(OffsetToPoint(%d)) = %d, want %d", off, roundTrip, off)
		}
	}
}

func TestLineIndexOffsetPointCRLFAndMixedNewlines(t *testing.T) {
	t.Parallel()

	src := []byte("a\r\nb\n\nc")
	idx := NewLineIndex(src)

	if got := idx.LineCount(); got != 4 {
		t.Fatalf("LineCount() = %d, want 4", got)
	}

	// Offsets at newline bytes stay on the preceding line for byte-column positions.
	cases := []struct {
		off  ByteOffset
		want Point
	}{
		{off: 0, want: Point{Line: 0, Column: 0}},
		{off: 1, want: Point{Line: 0, Column: 1}}, // '\r'
		{off: 2, want: Point{Line: 0, Column: 2}}, // '\n'
		{off: 3, want: Point{Line: 1, Column: 0}},
		{off: 4, want: Point{Line: 1, Column: 1}}, // '\n'
		{off: 5, want: Point{Line: 2, Column: 0}}, // empty line
		{off: 6, want: Point{Line: 3, Column: 0}},
		{off: 7, want: Point{Line: 3, Column: 1}}, // EOF
	}

	for _, tc := range cases {
		got, err := idx.OffsetToPoint(tc.off)
		if err != nil {
			t.Fatalf("OffsetToPoint(%d) error = %v", tc.off, err)
		}
		if got != tc.want {
			t.Fatalf("OffsetToPoint(%d) = %+v, want %+v", tc.off, got, tc.want)
		}
	}
}

func TestLineIndexPointToOffsetValidation(t *testing.T) {
	t.Parallel()

	idx := NewLineIndex([]byte("x\ny"))

	if _, err := idx.PointToOffset(Point{Line: -1, Column: 0}); err == nil {
		t.Fatal("expected error for negative line")
	}
	if _, err := idx.PointToOffset(Point{Line: 10, Column: 0}); err == nil {
		t.Fatal("expected error for out-of-range line")
	}
	if _, err := idx.PointToOffset(Point{Line: 0, Column: -1}); err == nil {
		t.Fatal("expected error for negative column")
	}
	// Non-final line should not accept next-line start as a canonical point.
	if _, err := idx.PointToOffset(Point{Line: 0, Column: 2}); err == nil {
		t.Fatal("expected error for non-canonical next-line start column")
	}
}

func TestUTF16ConversionsMultibyteAndSurrogatePair(t *testing.T) {
	t.Parallel()

	// "a" (1 byte, 1 UTF-16), "é" (2 bytes, 1 UTF-16), "😀" (4 bytes, 2 UTF-16)
	src := []byte("aé😀\r\nz")
	idx := NewLineIndex(src)

	offsetCases := []struct {
		off  ByteOffset
		want UTF16Position
	}{
		{off: 0, want: UTF16Position{Line: 0, Character: 0}},
		{off: 1, want: UTF16Position{Line: 0, Character: 1}},
		{off: 3, want: UTF16Position{Line: 0, Character: 2}},
		{off: 7, want: UTF16Position{Line: 0, Character: 4}},
		{off: 8, want: UTF16Position{Line: 0, Character: 4}},  // '\r' canonicalized to line end
		{off: 9, want: UTF16Position{Line: 1, Character: 0}},  // start of next line
		{off: 10, want: UTF16Position{Line: 1, Character: 1}}, // EOF
	}

	for _, tc := range offsetCases {
		got, err := idx.OffsetToUTF16Position(tc.off)
		if err != nil {
			t.Fatalf("OffsetToUTF16Position(%d) error = %v", tc.off, err)
		}
		if got != tc.want {
			t.Fatalf("OffsetToUTF16Position(%d) = %+v, want %+v", tc.off, got, tc.want)
		}
	}

	posCases := []struct {
		pos  UTF16Position
		want ByteOffset
	}{
		{pos: UTF16Position{Line: 0, Character: 0}, want: 0},
		{pos: UTF16Position{Line: 0, Character: 1}, want: 1},
		{pos: UTF16Position{Line: 0, Character: 2}, want: 3},
		{pos: UTF16Position{Line: 0, Character: 4}, want: 7}, // line end before CRLF
		{pos: UTF16Position{Line: 1, Character: 0}, want: 9},
		{pos: UTF16Position{Line: 1, Character: 1}, want: 10},
	}

	for _, tc := range posCases {
		got, err := idx.UTF16PositionToOffset(tc.pos)
		if err != nil {
			t.Fatalf("UTF16PositionToOffset(%+v) error = %v", tc.pos, err)
		}
		if got != tc.want {
			t.Fatalf("UTF16PositionToOffset(%+v) = %d, want %d", tc.pos, got, tc.want)
		}
	}

	if _, err := idx.UTF16PositionToOffset(UTF16Position{Line: 0, Character: 3}); err == nil {
		t.Fatal("expected error for surrogate-pair split position")
	}
	if _, err := idx.UTF16PositionToOffset(UTF16Position{Line: 0, Character: 5}); err == nil {
		t.Fatal("expected error for out-of-range UTF-16 character")
	}
}

func TestUTF16ConversionsInvalidUTF8(t *testing.T) {
	t.Parallel()

	idx := NewLineIndex([]byte{0xff})
	if _, err := idx.OffsetToUTF16Position(1); err == nil {
		t.Fatal("expected error for invalid UTF-8 in OffsetToUTF16Position")
	}
	if _, err := idx.UTF16PositionToOffset(UTF16Position{Line: 0, Character: 1}); err == nil {
		t.Fatal("expected error for invalid UTF-8 in UTF16PositionToOffset")
	}
}
