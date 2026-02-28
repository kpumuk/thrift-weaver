package syntax

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

var (
	// Verification is periodic and intentionally sparse to keep edit-path allocations low.
	fullParseVerificationEvery uint64 = 256

	verificationCompareOverrideMu sync.RWMutex
	verificationCompareOverride   func(a, b *Tree) bool
)

type parseRuntimeState struct {
	parser             parserbackend.Parser
	rawTree            *ts.Tree
	incrementalEnabled bool
	reparseCount       uint64
}

// InputEdit describes an incremental document edit in byte and point coordinates.
type InputEdit struct {
	StartByte   text.ByteOffset
	OldEndByte  text.ByteOffset
	NewEndByte  text.ByteOffset
	StartPoint  text.Point
	OldEndPoint text.Point
	NewEndPoint text.Point
}

// ReparseEvent reports whether a reparse used incremental or fallback behavior.
type ReparseEvent struct {
	Mode               string
	ProvidedOldTree    bool
	AppliedTreeEdits   int
	ChangedRangeCount  int
	VerificationRun    bool
	VerificationFailed bool
	FallbackReason     string
}

var (
	reparseObserverMu sync.RWMutex
	reparseObserver   func(ReparseEvent)
)

// SetReparseObserverForTesting installs a parser reparse observer for tests.
func SetReparseObserverForTesting(fn func(ReparseEvent)) func() {
	reparseObserverMu.Lock()
	prev := reparseObserver
	reparseObserver = fn
	reparseObserverMu.Unlock()
	return func() {
		reparseObserverMu.Lock()
		reparseObserver = prev
		reparseObserverMu.Unlock()
	}
}

func emitReparseEvent(ev ReparseEvent) {
	reparseObserverMu.RLock()
	observer := reparseObserver
	reparseObserverMu.RUnlock()
	if observer != nil {
		observer(ev)
	}
}

func (t *Tree) closeRuntime() {
	if t == nil || t.runtime == nil {
		return
	}
	if t.runtime.rawTree != nil {
		t.runtime.rawTree.Close()
		t.runtime.rawTree = nil
	}
	if t.runtime.parser != nil {
		t.runtime.parser.Close()
		t.runtime.parser = nil
	}
	t.runtime = nil
}

// Close releases parser/runtime resources associated with the tree.
func (t *Tree) Close() {
	if t == nil {
		return
	}
	t.closeRuntime()
}

func adoptRuntimeTree(out *Tree, state *parseRuntimeState) {
	if out == nil {
		return
	}
	out.runtime = state
}

func runtimeStateFromTree(t *Tree) *parseRuntimeState {
	if t == nil {
		return nil
	}
	return t.runtime
}

func shouldVerifyWithFullParse(state *parseRuntimeState) bool {
	if state == nil || state.reparseCount == 0 {
		return false
	}
	return state.reparseCount%fullParseVerificationEvery == 0
}

func validateIncrementalEdits(edits []InputEdit, srcLen int) error {
	for i, edit := range edits {
		if edit.StartByte < 0 || edit.OldEndByte < edit.StartByte || edit.NewEndByte < edit.StartByte {
			return fmt.Errorf("invalid edit bounds at index %d", i)
		}
		if int(edit.OldEndByte) > srcLen {
			return fmt.Errorf("old edit end exceeds source length at index %d", i)
		}
	}
	return nil
}

func toTSEdit(edit InputEdit) ts.InputEdit {
	return ts.InputEdit{
		StartByte:  int(edit.StartByte),
		OldEndByte: int(edit.OldEndByte),
		NewEndByte: int(edit.NewEndByte),
		StartPoint: ts.Point{
			Row:    edit.StartPoint.Line,
			Column: edit.StartPoint.Column,
		},
		OldEndPoint: ts.Point{
			Row:    edit.OldEndPoint.Line,
			Column: edit.OldEndPoint.Column,
		},
		NewEndPoint: ts.Point{
			Row:    edit.NewEndPoint.Line,
			Column: edit.NewEndPoint.Column,
		},
	}
}

func spansFromChangedRanges(ranges []ts.ChangedRange) ([]text.Span, error) {
	out := make([]text.Span, 0, len(ranges))
	for i, r := range ranges {
		sp := text.Span{
			Start: text.ByteOffset(r.StartByte),
			End:   text.ByteOffset(r.EndByte),
		}
		if !sp.IsValid() {
			return nil, fmt.Errorf("invalid changed range at index %d: %v..%v", i, r.StartByte, r.EndByte)
		}
		out = append(out, sp)
	}
	return out, nil
}

func validateChangedRanges(ranges []text.Span, srcLen int) error {
	prevEnd := text.ByteOffset(0)
	limit := text.ByteOffset(srcLen)
	for i, sp := range ranges {
		if !sp.IsValid() || sp.End > limit {
			return fmt.Errorf("changed range[%d] invalid: %s", i, sp)
		}
		if i > 0 && sp.Start < prevEnd {
			return fmt.Errorf("changed range[%d] overlaps previous range", i)
		}
		prevEnd = sp.End
	}
	return nil
}

func equivalentTrees(a, b *Tree) bool {
	verificationCompareOverrideMu.RLock()
	override := verificationCompareOverride
	verificationCompareOverrideMu.RUnlock()
	if override != nil {
		return override(a, b)
	}

	if a == nil || b == nil {
		return a == b
	}
	if !bytes.Equal(a.Source, b.Source) {
		return false
	}
	if len(a.Tokens) != len(b.Tokens) || len(a.Nodes) != len(b.Nodes) || len(a.Diagnostics) != len(b.Diagnostics) {
		return false
	}
	for i := range a.Tokens {
		if a.Tokens[i].Kind != b.Tokens[i].Kind || a.Tokens[i].Span != b.Tokens[i].Span {
			return false
		}
	}
	for i := range a.Nodes {
		an := a.Nodes[i]
		bn := b.Nodes[i]
		if an.Kind != bn.Kind || an.Span != bn.Span || an.FirstToken != bn.FirstToken || an.LastToken != bn.LastToken || an.Parent != bn.Parent || an.Flags != bn.Flags {
			return false
		}
		if len(an.Children) != len(bn.Children) {
			return false
		}
		for j := range an.Children {
			if an.Children[j] != bn.Children[j] {
				return false
			}
		}
	}
	for i := range a.Diagnostics {
		ad := a.Diagnostics[i]
		bd := b.Diagnostics[i]
		if ad.Code != bd.Code || ad.Message != bd.Message || ad.Severity != bd.Severity || ad.Span != bd.Span || ad.Source != bd.Source || ad.Recoverable != bd.Recoverable {
			return false
		}
	}
	return true
}

func parserWarningDiagnostic(message string) Diagnostic {
	return Diagnostic{
		Code:        DiagnosticInternalParse,
		Message:     message,
		Severity:    SeverityWarning,
		Span:        text.Span{Start: 0, End: 0},
		Source:      "parser",
		Recoverable: true,
	}
}

func setFullParseVerificationEveryForTesting(value uint64) func() {
	prev := fullParseVerificationEvery
	if value == 0 {
		value = 1
	}
	fullParseVerificationEvery = value
	return func() {
		fullParseVerificationEvery = prev
	}
}

func setVerificationCompareOverrideForTesting(fn func(a, b *Tree) bool) func() {
	verificationCompareOverrideMu.Lock()
	prev := verificationCompareOverride
	verificationCompareOverride = fn
	verificationCompareOverrideMu.Unlock()
	return func() {
		verificationCompareOverrideMu.Lock()
		verificationCompareOverride = prev
		verificationCompareOverrideMu.Unlock()
	}
}

func fullReparseWithExistingParser(ctx context.Context, old *Tree, src []byte, opts ParseOptions, reason string) (*Tree, error) {
	state := runtimeStateFromTree(old)
	if state == nil || state.parser == nil {
		return Parse(ctx, src, opts)
	}
	newRawTree, err := state.parser.Parse(ctx, src, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if old != nil {
			old.closeRuntime()
		}
		return buildDegradedTreeForParserFailure(src, opts, fullReparseFailureError(reason, err)), nil
	}
	out, err := buildSyntaxTreeFromRaw(src, opts, newRawTree)
	if err != nil {
		newRawTree.Close()
		return nil, err
	}
	nextState := &parseRuntimeState{
		parser:             state.parser,
		rawTree:            newRawTree,
		incrementalEnabled: state.incrementalEnabled,
		reparseCount:       state.reparseCount + 1,
	}
	if reason != "" {
		out.Diagnostics = append(out.Diagnostics, parserWarningDiagnostic(reason))
	}
	adoptRuntimeTree(out, nextState)
	if state.rawTree != nil {
		state.rawTree.Close()
		state.rawTree = nil
	}
	old.runtime = nil
	return out, nil
}

func errNoIncrementalState(old *Tree) error {
	if old == nil {
		return errors.New("nil old tree")
	}
	if old.runtime == nil {
		return errors.New("old tree runtime is unavailable")
	}
	if old.runtime.parser == nil || old.runtime.rawTree == nil {
		return errors.New("old tree incremental state is incomplete")
	}
	return nil
}

func fullReparseFailureError(reason string, err error) error {
	if reason == "" {
		return fmt.Errorf("full reparse failed: %w", err)
	}
	return fmt.Errorf("%s: %w", reason, err)
}
