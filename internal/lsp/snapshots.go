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

	nextSrc, err := applyContentChanges(cur.Tree.Source, changes)
	if err != nil {
		return nil, err
	}
	nextTree, err := syntax.Reparse(ctx, cur.Tree, nextSrc, syntax.ParseOptions{URI: uri, Version: version})
	if err != nil {
		return nil, err
	}
	next := &Snapshot{URI: uri, Version: version, Tree: nextTree}
	s.mu.Lock()
	s.docs[uri] = next
	s.mu.Unlock()
	return next, nil
}

// Close removes a tracked document snapshot.
func (s *SnapshotStore) Close(uri string) {
	if s == nil {
		return
	}
	s.mu.Lock()
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

func applyContentChanges(src []byte, changes []TextDocumentContentChangeEvent) ([]byte, error) {
	if len(changes) == 0 {
		return slices.Clone(src), nil
	}
	cur := slices.Clone(src)
	for _, ch := range changes {
		if ch.Range == nil {
			cur = []byte(ch.Text)
			continue
		}
		li := itext.NewLineIndex(cur)
		start, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: ch.Range.Start.Line, Character: ch.Range.Start.Character})
		if err != nil {
			return nil, fmt.Errorf("change range start: %w", err)
		}
		end, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: ch.Range.End.Line, Character: ch.Range.End.Character})
		if err != nil {
			return nil, fmt.Errorf("change range end: %w", err)
		}
		if end < start {
			return nil, errors.New("change range end before start")
		}
		cur, err = itext.ApplyEdits(cur, []itext.ByteEdit{{
			Span:    itext.Span{Start: start, End: end},
			NewText: []byte(ch.Text),
		}})
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}
