package text

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf16"
	"unicode/utf8"
)

// LineIndex maps byte offsets to line/column locations over a UTF-8 source buffer.
//
// Line and column semantics:
//   - Line numbers are 0-based.
//   - Point columns are byte columns.
//   - UTF-16 positions are LSP-facing and treat line terminators as not part of the line content.
type LineIndex struct {
	src        []byte
	lineStarts []ByteOffset
}

var (
	errNilLineIndex            = errors.New("nil LineIndex")
	errInvalidUTF8Sequence     = errors.New("invalid UTF-8 sequence")
	errSplitUTF16SurrogatePair = errors.New("UTF-16 position splits surrogate pair")
)

// NewLineIndex builds an index over src.
func NewLineIndex(src []byte) *LineIndex {
	starts := []ByteOffset{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, ByteOffset(i+1))
		}
	}
	return &LineIndex{
		src:        src,
		lineStarts: starts,
	}
}

// SourceLen returns the source length in bytes.
func (li *LineIndex) SourceLen() ByteOffset {
	if li == nil {
		return 0
	}
	return ByteOffset(len(li.src))
}

// LineCount returns the number of logical lines in the source.
func (li *LineIndex) LineCount() int {
	if li == nil {
		return 0
	}
	return len(li.lineStarts)
}

// OffsetToPoint converts a byte offset to a UTF-8 byte-based point.
func (li *LineIndex) OffsetToPoint(off ByteOffset) (Point, error) {
	if li == nil {
		return Point{}, errNilLineIndex
	}
	if err := li.validateOffset(off); err != nil {
		return Point{}, err
	}

	line := li.lineForOffset(off)
	start := li.lineStarts[line]
	return Point{
		Line:   line,
		Column: int(off - start),
	}, nil
}

// PointToOffset converts a UTF-8 byte-based point to a byte offset.
func (li *LineIndex) PointToOffset(p Point) (ByteOffset, error) {
	if li == nil {
		return 0, errNilLineIndex
	}
	if err := li.validateLine(p.Line); err != nil {
		return 0, err
	}
	if p.Column < 0 {
		return 0, fmt.Errorf("column out of range: %d", p.Column)
	}

	start, _, _ := li.lineBounds(p.Line)
	maxColumn := li.maxPointColumn(p.Line)
	if p.Column > maxColumn {
		return 0, fmt.Errorf("column out of range: line=%d column=%d max=%d", p.Line, p.Column, maxColumn)
	}
	return start + ByteOffset(p.Column), nil
}

// OffsetToUTF16Position converts a byte offset to an LSP-facing UTF-16 position.
func (li *LineIndex) OffsetToUTF16Position(off ByteOffset) (UTF16Position, error) {
	if li == nil {
		return UTF16Position{}, errNilLineIndex
	}
	if err := li.validateOffset(off); err != nil {
		return UTF16Position{}, err
	}

	line := li.lineForOffset(off)
	start, nextStart, contentEnd := li.lineBounds(line)

	// Canonicalize offsets inside line terminators to the line-end position.
	if off > contentEnd && off < nextStart {
		off = contentEnd
	}

	char, err := utf16UnitsForSlice(li.src[start:off])
	if err != nil {
		return UTF16Position{}, err
	}
	return UTF16Position{
		Line:      line,
		Character: char,
	}, nil
}

// UTF16PositionToOffset converts an LSP-facing UTF-16 position to a byte offset.
func (li *LineIndex) UTF16PositionToOffset(pos UTF16Position) (ByteOffset, error) {
	if li == nil {
		return 0, errNilLineIndex
	}
	if err := li.validateLine(pos.Line); err != nil {
		return 0, err
	}
	if pos.Character < 0 {
		return 0, fmt.Errorf("character out of range: %d", pos.Character)
	}

	start, _, contentEnd := li.lineBounds(pos.Line)
	rel, err := utf16UnitsToByteOffset(li.src[start:contentEnd], pos.Character)
	if err != nil {
		return 0, err
	}
	return start + rel, nil
}

func (li *LineIndex) validateOffset(off ByteOffset) error {
	if !off.IsValid() {
		return fmt.Errorf("offset out of range: %d", off)
	}
	if off > ByteOffset(len(li.src)) {
		return fmt.Errorf("offset out of range: %d > %d", off, len(li.src))
	}
	return nil
}

func (li *LineIndex) validateLine(line int) error {
	if line < 0 || line >= li.LineCount() {
		return fmt.Errorf("line out of range: %d", line)
	}
	return nil
}

func (li *LineIndex) lineForOffset(off ByteOffset) int {
	// largest i such that lineStarts[i] <= off
	i, found := slices.BinarySearch(li.lineStarts, off)
	if found {
		return i
	}
	return i - 1
}

func (li *LineIndex) lineBounds(line int) (start ByteOffset, nextStart ByteOffset, contentEnd ByteOffset) {
	start = li.lineStarts[line]
	if line+1 < len(li.lineStarts) {
		nextStart = li.lineStarts[line+1]
	} else {
		nextStart = ByteOffset(len(li.src))
	}
	contentEnd = nextStart
	if contentEnd > start && li.src[contentEnd-1] == '\n' {
		contentEnd--
		if contentEnd > start && li.src[contentEnd-1] == '\r' {
			contentEnd--
		}
	}
	return start, nextStart, contentEnd
}

func (li *LineIndex) maxPointColumn(line int) int {
	start, nextStart, _ := li.lineBounds(line)
	maxColumn := int(nextStart - start)
	if line < li.LineCount()-1 {
		// Non-final lines canonicalize the start of the next line to the next line, not current line.
		maxColumn--
	}
	return maxColumn
}

func utf16UnitsForSlice(b []byte) (int, error) {
	units := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return 0, errInvalidUTF8Sequence
		}
		units += utf16RuneUnits(r)
		b = b[size:]
	}
	return units, nil
}

func utf16UnitsToByteOffset(line []byte, wantUnits int) (ByteOffset, error) {
	units := 0
	i := 0
	for i < len(line) {
		if units == wantUnits {
			return ByteOffset(i), nil
		}

		r, size := utf8.DecodeRune(line[i:])
		if r == utf8.RuneError && size == 1 {
			return 0, errInvalidUTF8Sequence
		}

		rUnits := utf16RuneUnits(r)
		if wantUnits > units && wantUnits < units+rUnits {
			return 0, errSplitUTF16SurrogatePair
		}

		units += rUnits
		i += size
	}

	if units == wantUnits {
		return ByteOffset(i), nil
	}
	return 0, fmt.Errorf("character out of range: %d > %d", wantUnits, units)
}

func utf16RuneUnits(r rune) int {
	if utf16.IsSurrogate(r) {
		// Invalid scalar value for UTF-8 data; treat as one code unit if ever encountered.
		return 1
	}
	if r <= 0xFFFF {
		return 1
	}
	return 2
}
