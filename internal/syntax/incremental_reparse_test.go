package syntax

import (
	"context"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

func TestApplyIncrementalEditsAndReparseUsesTreeEditAndOldTree(t *testing.T) {
	src := []byte("struct User {\n  1: string name,\n}\n")
	oldTree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///incremental.thrift", Version: 1})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer oldTree.Close()

	nextSrc, edits := addByteEditAt(t, oldTree.Source, []byte("name"), []byte("xname"))

	var observed ReparseEvent
	restoreObserver := SetReparseObserverForTesting(func(ev ReparseEvent) {
		observed = ev
	})
	defer restoreObserver()

	nextTree, err := ApplyIncrementalEditsAndReparse(context.Background(), oldTree, nextSrc, ParseOptions{
		URI:     oldTree.URI,
		Version: 2,
	}, edits)
	if err != nil {
		t.Fatalf("ApplyIncrementalEditsAndReparse: %v", err)
	}
	defer nextTree.Close()

	if observed.Mode != "incremental" {
		t.Fatalf("event.Mode=%q, want incremental", observed.Mode)
	}
	if !observed.ProvidedOldTree {
		t.Fatal("expected old tree reuse")
	}
	if observed.AppliedTreeEdits != 1 {
		t.Fatalf("AppliedTreeEdits=%d, want 1", observed.AppliedTreeEdits)
	}
	if got := string(nextTree.Source); !strings.Contains(got, "xname") {
		t.Fatalf("unexpected reparsed source: %q", got)
	}
}

func TestIncrementalVerificationMismatchDisablesIncrementalMode(t *testing.T) {
	restoreCadence := setFullParseVerificationEveryForTesting(1)
	defer restoreCadence()
	restoreCompare := setVerificationCompareOverrideForTesting(func(_, _ *Tree) bool { return false })
	defer restoreCompare()

	src := []byte("struct User {\n  1: string name,\n}\n")
	oldTree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///verify.thrift", Version: 1})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer oldTree.Close()

	nextSrc, edits := addByteEditAt(t, oldTree.Source, []byte("name"), []byte("zname"))

	var events []ReparseEvent
	restoreObserver := SetReparseObserverForTesting(func(ev ReparseEvent) {
		events = append(events, ev)
	})
	defer restoreObserver()

	tree2, err := ApplyIncrementalEditsAndReparse(context.Background(), oldTree, nextSrc, ParseOptions{URI: oldTree.URI, Version: 2}, edits)
	if err != nil {
		t.Fatalf("first reparse: %v", err)
	}
	defer tree2.Close()
	if !hasDiagnosticContaining(tree2.Diagnostics, "incremental verification mismatch") {
		t.Fatalf("expected verification mismatch diagnostic, got %+v", tree2.Diagnostics)
	}

	src3, edits3 := addByteEditAt(t, tree2.Source, []byte("zname"), []byte("qzname"))
	tree3, err := ApplyIncrementalEditsAndReparse(context.Background(), tree2, src3, ParseOptions{URI: oldTree.URI, Version: 3}, edits3)
	if err != nil {
		t.Fatalf("second reparse: %v", err)
	}
	defer tree3.Close()

	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	last := events[len(events)-1]
	if last.Mode != "fallback_full" || last.FallbackReason != "incremental_disabled" {
		t.Fatalf("unexpected fallback event: %+v", last)
	}
}

func hasDiagnosticContaining(diags []Diagnostic, want string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, want) {
			return true
		}
	}
	return false
}

func addByteEditAt(t *testing.T, src []byte, oldNeedle []byte, replacement []byte) ([]byte, []InputEdit) {
	t.Helper()

	idx := strings.Index(string(src), string(oldNeedle))
	if idx < 0 {
		t.Fatalf("needle %q not found in source", string(oldNeedle))
	}
	start := text.ByteOffset(idx)
	oldEnd := text.ByteOffset(idx + len(oldNeedle))
	newEnd := text.ByteOffset(idx + len(replacement))

	oldLI := text.NewLineIndex(src)
	startPoint, err := oldLI.OffsetToPoint(start)
	if err != nil {
		t.Fatalf("start point: %v", err)
	}
	oldEndPoint, err := oldLI.OffsetToPoint(oldEnd)
	if err != nil {
		t.Fatalf("old end point: %v", err)
	}

	nextSrc, err := text.ApplyEdits(src, []text.ByteEdit{{
		Span:    text.Span{Start: start, End: oldEnd},
		NewText: replacement,
	}})
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	newLI := text.NewLineIndex(nextSrc)
	newEndPoint, err := newLI.OffsetToPoint(newEnd)
	if err != nil {
		t.Fatalf("new end point: %v", err)
	}

	return nextSrc, []InputEdit{{
		StartByte:   start,
		OldEndByte:  oldEnd,
		NewEndByte:  newEnd,
		StartPoint:  startPoint,
		OldEndPoint: oldEndPoint,
		NewEndPoint: newEndPoint,
	}}
}
