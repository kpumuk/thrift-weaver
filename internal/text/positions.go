// Package text defines source offsets, spans, and position/range types.
package text

import "fmt"

// ByteOffset is a byte index into a UTF-8 source buffer.
type ByteOffset int

// IsValid reports whether the offset is non-negative.
func (o ByteOffset) IsValid() bool {
	return o >= 0
}

// Span is a half-open byte range [Start, End).
type Span struct {
	Start ByteOffset // inclusive
	End   ByteOffset // exclusive
}

// NewSpan constructs a validated span.
func NewSpan(start, end ByteOffset) (Span, error) {
	s := Span{Start: start, End: end}
	if err := s.Validate(); err != nil {
		return Span{}, err
	}
	return s, nil
}

// Validate reports an error if the span is invalid.
func (s Span) Validate() error {
	if !s.Start.IsValid() {
		return fmt.Errorf("invalid span start: %d", s.Start)
	}
	if !s.End.IsValid() {
		return fmt.Errorf("invalid span end: %d", s.End)
	}
	if s.End < s.Start {
		return fmt.Errorf("invalid span bounds: end (%d) < start (%d)", s.End, s.Start)
	}
	return nil
}

// IsValid reports whether the span bounds are well-formed.
func (s Span) IsValid() bool {
	return s.Start.IsValid() && s.End.IsValid() && s.End >= s.Start
}

// IsEmpty reports whether the span covers zero bytes.
func (s Span) IsEmpty() bool {
	return s.Start == s.End
}

// Len returns the number of bytes covered by the span.
// For invalid spans, the result is undefined.
func (s Span) Len() ByteOffset {
	return s.End - s.Start
}

// Contains reports whether off is within the half-open span [Start, End).
func (s Span) Contains(off ByteOffset) bool {
	if !s.IsValid() || !off.IsValid() {
		return false
	}
	return s.Start <= off && off < s.End
}

// ContainsSpan reports whether other is fully contained within s.
func (s Span) ContainsSpan(other Span) bool {
	if !s.IsValid() || !other.IsValid() {
		return false
	}
	return s.Start <= other.Start && other.End <= s.End
}

// Intersects reports whether two spans overlap by at least one byte.
// Spans that only touch at a boundary do not intersect.
func (s Span) Intersects(other Span) bool {
	if !s.IsValid() || !other.IsValid() {
		return false
	}
	return s.Start < other.End && other.Start < s.End
}

func (s Span) String() string {
	return fmt.Sprintf("[%d,%d)", s.Start, s.End)
}

// Point is a UTF-8 byte-based source location.
type Point struct {
	Line   int // 0-based
	Column int // byte column
}

// Range is a source range expressed in UTF-8 byte-based points.
type Range struct {
	Start Point
	End   Point
}

// UTF16Position is an LSP-facing UTF-16 position kept at system edges.
type UTF16Position struct {
	Line      int
	Character int
}

// UTF16Range is an LSP-facing UTF-16 range kept at system edges.
type UTF16Range struct {
	Start UTF16Position
	End   UTF16Position
}
