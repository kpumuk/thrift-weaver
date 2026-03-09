package lsp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

// Snapshot is an immutable parsed document state.
type Snapshot struct {
	URI     string
	Version int32
	// Generation increments on each successful document mutation and is used to suppress stale async output.
	Generation uint64
	Tree       *syntax.Tree
}

// Bytes returns a copy of the snapshot source bytes.
func (s *Snapshot) Bytes() []byte {
	if s == nil || s.Tree == nil {
		return nil
	}
	return slices.Clone(s.Tree.Source)
}

// SnapshotStore stores versioned parsed documents.
type SnapshotStore struct {
	mu   sync.RWMutex
	docs map[string]*documentState
}

type documentState struct {
	mu       sync.RWMutex
	snapshot *Snapshot
	// generation survives close/reopen so async work for an older incarnation
	// of the same URI cannot publish into a newly opened document.
	generation uint64
}

// NewSnapshotStore creates an empty snapshot store.
func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{docs: make(map[string]*documentState)}
}

// Open parses and stores a document snapshot.
func (s *SnapshotStore) Open(ctx context.Context, uri string, version int32, src []byte) (*Snapshot, error) {
	if s == nil {
		return nil, errors.New("nil SnapshotStore")
	}
	doc := s.documentState(uri, true)
	doc.mu.Lock()
	defer doc.mu.Unlock()

	tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: uri, Version: version})
	if err != nil {
		return nil, err
	}
	return doc.storeSnapshotLocked(uri, version, tree), nil
}

// Change applies incremental LSP changes, reparses, and replaces the snapshot.
func (s *SnapshotStore) Change(ctx context.Context, uri string, version int32, changes []TextDocumentContentChangeEvent) (*Snapshot, error) {
	if s == nil {
		return nil, errors.New("nil SnapshotStore")
	}
	doc := s.documentState(uri, false)
	if doc == nil {
		return nil, ErrDocumentNotOpen
	}
	doc.mu.Lock()
	defer doc.mu.Unlock()

	cur := doc.snapshot
	if cur == nil {
		return nil, ErrDocumentNotOpen
	}
	if version <= cur.Version {
		return nil, ErrStaleVersion
	}

	nextSrc, incrementalEdits, incrementalEligible, err := applyContentChanges(cur.Tree.Source, changes)
	if err != nil {
		return nil, err
	}
	var nextTree *syntax.Tree
	if incrementalEligible {
		nextTree, err = syntax.ApplyIncrementalEditsAndReparse(ctx, cur.Tree, nextSrc, syntax.ParseOptions{URI: uri, Version: version}, incrementalEdits)
	} else {
		nextTree, err = syntax.Reparse(ctx, cur.Tree, nextSrc, syntax.ParseOptions{URI: uri, Version: version})
	}
	if err != nil {
		return nil, err
	}
	return doc.storeSnapshotLocked(uri, version, nextTree), nil
}

// Close removes a tracked document snapshot.
func (s *SnapshotStore) Close(uri string) {
	if s == nil {
		return
	}
	doc := s.documentState(uri, false)
	if doc == nil {
		return
	}

	doc.mu.Lock()
	snap := doc.clearSnapshotLocked()
	doc.mu.Unlock()

	if snap != nil && snap.Tree != nil {
		snap.Tree.Close()
	}
}

// Snapshot returns the current snapshot for uri.
func (s *SnapshotStore) Snapshot(uri string) (*Snapshot, bool) {
	if s == nil {
		return nil, false
	}
	doc := s.documentState(uri, false)
	if doc == nil {
		return nil, false
	}

	doc.mu.RLock()
	defer doc.mu.RUnlock()
	if doc.snapshot == nil {
		return nil, false
	}
	return doc.snapshot, true
}

// SnapshotAtVersion returns the current snapshot if the version matches exactly.
func (s *SnapshotStore) SnapshotAtVersion(uri string, version int32) (*Snapshot, error) {
	snap, ok := s.Snapshot(uri)
	if !ok {
		return nil, ErrDocumentNotOpen
	}
	if snap.Version != version {
		return nil, ErrStaleVersion
	}
	return snap, nil
}

func (s *SnapshotStore) documentState(uri string, create bool) *documentState {
	s.mu.RLock()
	doc := s.docs[uri]
	s.mu.RUnlock()
	if doc != nil || !create {
		return doc
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if doc = s.docs[uri]; doc != nil {
		return doc
	}
	doc = &documentState{}
	s.docs[uri] = doc
	return doc
}

func (d *documentState) nextGeneration() uint64 {
	d.generation++
	return d.generation
}

func (d *documentState) storeSnapshotLocked(uri string, version int32, tree *syntax.Tree) *Snapshot {
	snap := &Snapshot{
		URI:        uri,
		Version:    version,
		Generation: d.nextGeneration(),
		Tree:       tree,
	}
	prev := d.snapshot
	d.snapshot = snap
	if prev != nil && prev.Tree != nil {
		prev.Tree.Close()
	}
	return snap
}

func (d *documentState) clearSnapshotLocked() *Snapshot {
	snap := d.snapshot
	d.snapshot = nil
	d.nextGeneration()
	return snap
}

func applyContentChanges(src []byte, changes []TextDocumentContentChangeEvent) ([]byte, []syntax.InputEdit, bool, error) {
	const (
		maxIncrementalEdits      = 1024
		maxIncrementalEditedByte = 256 * 1024
	)
	if len(changes) == 0 {
		return slices.Clone(src), nil, false, nil
	}
	if len(changes) > maxIncrementalEdits {
		next, err := applyTextChanges(src, changes)
		return next, nil, false, err
	}

	cur := slices.Clone(src)
	edits := make([]syntax.InputEdit, 0, len(changes))
	incrementalEligible := true
	editedBytes := 0

	for _, ch := range changes {
		if ch.Range == nil {
			cur = []byte(ch.Text)
			incrementalEligible = false
			continue
		}
		li := itext.NewLineIndex(cur)
		start, end, err := utf16RangeToOffsets(li, *ch.Range)
		if err != nil {
			return nil, nil, false, err
		}

		startPoint, err := li.OffsetToPoint(start)
		if err != nil {
			return nil, nil, false, fmt.Errorf("change start point: %w", err)
		}
		oldEndPoint, err := li.OffsetToPoint(end)
		if err != nil {
			return nil, nil, false, fmt.Errorf("change old-end point: %w", err)
		}

		next, applyErr := applySingleChange(cur, start, end, ch.Text)
		if applyErr != nil {
			return nil, nil, false, applyErr
		}
		newEndOffset := start + itext.ByteOffset(len(ch.Text))
		newLI := itext.NewLineIndex(next)
		newEndPoint, err := newLI.OffsetToPoint(newEndOffset)
		if err != nil {
			return nil, nil, false, fmt.Errorf("change new-end point: %w", err)
		}
		edits = append(edits, syntax.InputEdit{
			StartByte:   start,
			OldEndByte:  end,
			NewEndByte:  newEndOffset,
			StartPoint:  startPoint,
			OldEndPoint: oldEndPoint,
			NewEndPoint: newEndPoint,
		})
		editedBytes += int(end-start) + len(ch.Text)
		if editedBytes > maxIncrementalEditedByte {
			incrementalEligible = false
		}
		cur = next
	}
	if !incrementalEligible || len(edits) == 0 {
		return cur, nil, false, nil
	}
	return cur, edits, true, nil
}

func applyTextChanges(src []byte, changes []TextDocumentContentChangeEvent) ([]byte, error) {
	cur := slices.Clone(src)
	for _, ch := range changes {
		if ch.Range == nil {
			cur = []byte(ch.Text)
			continue
		}
		li := itext.NewLineIndex(cur)
		start, end, err := utf16RangeToOffsets(li, *ch.Range)
		if err != nil {
			return nil, err
		}

		cur, err = applySingleChange(cur, start, end, ch.Text)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

func applySingleChange(src []byte, start, end itext.ByteOffset, newText string) ([]byte, error) {
	return itext.ApplyEdits(src, []itext.ByteEdit{{
		Span:    itext.Span{Start: start, End: end},
		NewText: []byte(newText),
	}})
}

func utf16RangeToOffsets(li *itext.LineIndex, r Range) (itext.ByteOffset, itext.ByteOffset, error) {
	start, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: r.Start.Line, Character: r.Start.Character})
	if err != nil {
		return 0, 0, fmt.Errorf("change range start: %w", err)
	}
	end, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: r.End.Line, Character: r.End.Character})
	if err != nil {
		return 0, 0, fmt.Errorf("change range end: %w", err)
	}
	if end < start {
		return 0, 0, errors.New("change range end before start")
	}
	return start, end, nil
}
