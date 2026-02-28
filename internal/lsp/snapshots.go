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
	Tree    *syntax.Tree
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
	docs map[string]*Snapshot
}

// NewSnapshotStore creates an empty snapshot store.
func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{docs: make(map[string]*Snapshot)}
}

// Open parses and stores a document snapshot.
func (s *SnapshotStore) Open(ctx context.Context, uri string, version int32, src []byte) (*Snapshot, error) {
	if s == nil {
		return nil, errors.New("nil SnapshotStore")
	}
	tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: uri, Version: version})
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{URI: uri, Version: version, Tree: tree}
	s.mu.Lock()
	if prev, ok := s.docs[uri]; ok && prev != nil && prev.Tree != nil {
		prev.Tree.Close()
	}
	s.docs[uri] = snap
	s.mu.Unlock()
	return snap, nil
}

// Change applies incremental LSP changes, reparses, and replaces the snapshot.
func (s *SnapshotStore) Change(ctx context.Context, uri string, version int32, changes []TextDocumentContentChangeEvent) (*Snapshot, error) {
	if s == nil {
		return nil, errors.New("nil SnapshotStore")
	}
	s.mu.RLock()
	cur, ok := s.docs[uri]
	s.mu.RUnlock()
	if !ok {
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
	next := &Snapshot{URI: uri, Version: version, Tree: nextTree}
	s.mu.Lock()
	s.docs[uri] = next
	s.mu.Unlock()
	cur.Tree.Close()
	return next, nil
}

// Close removes a tracked document snapshot.
func (s *SnapshotStore) Close(uri string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if snap, ok := s.docs[uri]; ok && snap != nil && snap.Tree != nil {
		snap.Tree.Close()
	}
	delete(s.docs, uri)
	s.mu.Unlock()
}

// Snapshot returns the current snapshot for uri.
func (s *SnapshotStore) Snapshot(uri string) (*Snapshot, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.docs[uri]
	return snap, ok
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
