package syntax

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

func BenchmarkReparseMediumFull(b *testing.B) {
	src, markerPrefix := mediumBenchmarkFixture()
	tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///bench.thrift", Version: 1})
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	current := append([]byte(nil), src...)
	version := int32(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		nextSrc, _ := toggleMarkerByte(b, current, markerPrefix, i)
		version++
		nextTree, err := Reparse(context.Background(), tree, nextSrc, ParseOptions{URI: "file:///bench.thrift", Version: version})
		if err != nil {
			b.Fatalf("Reparse: %v", err)
		}
		current = nextSrc
		tree = nextTree
	}
}

func BenchmarkReparseMediumIncremental(b *testing.B) {
	src, markerPrefix := mediumBenchmarkFixture()
	tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///bench.thrift", Version: 1})
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	current := append([]byte(nil), src...)
	version := int32(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		nextSrc, edits := toggleMarkerByte(b, current, markerPrefix, i)
		version++
		nextTree, err := ApplyIncrementalEditsAndReparse(context.Background(), tree, nextSrc, ParseOptions{
			URI:     "file:///bench.thrift",
			Version: version,
		}, edits)
		if err != nil {
			b.Fatalf("ApplyIncrementalEditsAndReparse: %v", err)
		}
		current = nextSrc
		tree = nextTree
	}
}

func mediumBenchmarkFixture() ([]byte, []byte) {
	var buf bytes.Buffer
	buf.WriteString("namespace go bench\n\nstruct Bench {\n")
	for i := range 240 {
		fmt.Fprintf(&buf, "  %d: optional string field_%04d,\n", i+1, i)
	}
	buf.WriteString("}\n")
	return buf.Bytes(), []byte("field_020")
}

func toggleMarkerByte(tb testing.TB, src []byte, markerPrefix []byte, iter int) ([]byte, []InputEdit) {
	tb.Helper()
	idx := bytes.Index(src, markerPrefix)
	if idx < 0 {
		tb.Fatalf("marker prefix %q not found", markerPrefix)
	}
	last := idx + len(markerPrefix)
	next := append([]byte(nil), src...)
	newByte := byte('0')
	if iter%2 == 1 {
		newByte = '1'
	}
	next[last] = newByte

	start := text.ByteOffset(last)
	oldEnd := start + 1
	newEnd := start + 1
	oldLI := text.NewLineIndex(src)
	newLI := text.NewLineIndex(next)
	startPoint, err := oldLI.OffsetToPoint(start)
	if err != nil {
		tb.Fatalf("start point: %v", err)
	}
	oldEndPoint, err := oldLI.OffsetToPoint(oldEnd)
	if err != nil {
		tb.Fatalf("old end point: %v", err)
	}
	newEndPoint, err := newLI.OffsetToPoint(newEnd)
	if err != nil {
		tb.Fatalf("new end point: %v", err)
	}
	return next, []InputEdit{{
		StartByte:   start,
		OldEndByte:  oldEnd,
		NewEndByte:  newEnd,
		StartPoint:  startPoint,
		OldEndPoint: oldEndPoint,
		NewEndPoint: newEndPoint,
	}}
}
