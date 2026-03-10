package index

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxFiles       = 10000
	defaultMaxFileBytes   = 2 << 20
	defaultRescanInterval = 30 * time.Second
)

type documentState struct {
	input   DocumentInput
	summary *DocumentSummary
	size    int64
	modTime time.Time
}

type documentSlot struct {
	key        DocumentKey
	displayURI string
	disk       *documentState
	open       *documentState
}

func (s *documentSlot) active() *documentState {
	if s == nil {
		return nil
	}
	if s.open != nil {
		return s.open
	}
	return s.disk
}

// Manager publishes immutable workspace snapshots built from document summaries.
type Manager struct {
	mu sync.Mutex

	roots       []string
	includeDirs []string
	maxFiles    int
	maxFileSize int64
	onEvent     func(Event)

	slots map[DocumentKey]*documentSlot

	snapshot atomic.Pointer[WorkspaceSnapshot]

	rescanInterval atomic.Int64
	rescanReset    chan struct{}
	closeCh        chan struct{}
	closeOnce      sync.Once
}

// NewManager constructs a workspace manager and starts periodic metadata rescans.
func NewManager(opts Options) *Manager {
	roots := normalizeRootPaths(opts.WorkspaceRoots)
	includeDirs := normalizeIncludeDirs(opts.IncludeDirs)
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}
	maxFileSize := opts.MaxFileBytes
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileBytes
	}

	m := &Manager{
		roots:       roots,
		includeDirs: includeDirs,
		maxFiles:    maxFiles,
		maxFileSize: maxFileSize,
		onEvent:     opts.Hooks.OnEvent,
		slots:       make(map[DocumentKey]*documentSlot),
		rescanReset: make(chan struct{}, 1),
		closeCh:     make(chan struct{}),
	}
	m.rescanInterval.Store(int64(defaultRescanInterval))
	go m.rescanLoop()
	return m
}

// Close stops the background rescan loop.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		close(m.closeCh)
	})
}

// UpsertOpenDocument reparses and stores the active open-document shadow.
func (m *Manager) UpsertOpenDocument(ctx context.Context, in DocumentInput) error {
	return m.UpsertOpenDocumentWithReason(ctx, in, RebuildReasonChange)
}

// UpsertOpenDocumentWithReason reparses and stores the active open-document shadow.
func (m *Manager) UpsertOpenDocumentWithReason(ctx context.Context, in DocumentInput, reason RebuildReason) error {
	if m == nil {
		return errors.New("nil Manager")
	}
	start := time.Now()
	displayURI, key, err := CanonicalizeDocumentURI(in.URI)
	if err != nil {
		return err
	}
	in.URI = displayURI

	summary, err := ParseAndSummarize(ctx, key, in)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	slot := m.ensureSlotLocked(key, displayURI)
	hadActive := slot.active() != nil
	slot.open = &documentState{input: in, summary: summary}
	m.publishLocked([]DocumentKey{key}, !hadActive, reason, time.Since(start), 0)
	return nil
}

// CloseOpenDocument removes the active open-document shadow for uri.
func (m *Manager) CloseOpenDocument(ctx context.Context, uri string) error {
	return m.CloseOpenDocumentWithReason(ctx, uri, RebuildReasonClose)
}

// CloseOpenDocumentWithReason removes the active open-document shadow for uri.
func (m *Manager) CloseOpenDocumentWithReason(ctx context.Context, uri string, reason RebuildReason) error {
	_ = ctx
	if m == nil {
		return errors.New("nil Manager")
	}
	start := time.Now()
	displayURI, key, err := CanonicalizeDocumentURI(uri)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	slot := m.slots[key]
	if slot == nil {
		return nil
	}
	hadActive := slot.active() != nil
	slot.displayURI = displayURI
	slot.open = nil
	hasActive := slot.active() != nil
	if !hasActive {
		delete(m.slots, key)
	}
	m.publishLocked([]DocumentKey{key}, hadActive != hasActive, reason, time.Since(start), 0)
	return nil
}

// RescanWorkspace rescans configured workspace roots and include directories.
func (m *Manager) RescanWorkspace(ctx context.Context) error {
	return m.RescanWorkspaceWithReason(ctx, RebuildReasonManualRescan)
}

// RescanWorkspaceWithReason rescans configured workspace roots and include directories.
func (m *Manager) RescanWorkspaceWithReason(ctx context.Context, reason RebuildReason) error {
	if m == nil {
		return errors.New("nil Manager")
	}
	start := time.Now()
	files, err := scanWorkspace(ctx, m.roots, m.includeDirs, m.maxFiles, m.maxFileSize)
	if err != nil {
		return err
	}
	scanDuration := time.Since(start)

	type nextDiskState struct {
		file    scannedFile
		summary *DocumentSummary
	}

	next := make(map[DocumentKey]nextDiskState, len(files))
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := make([]DocumentKey, 0, len(files))
	fullRebuild := false

	for _, file := range files {
		slot := m.slots[file.Key]
		prev := documentState{}
		if slot != nil && slot.disk != nil {
			prev = *slot.disk
		}

		if slot != nil && slot.disk != nil && slot.disk.size == file.Size && slot.disk.modTime.Equal(file.ModTime) {
			next[file.Key] = nextDiskState{file: file, summary: slot.disk.summary}
			continue
		}

		src, readErr := os.ReadFile(file.Path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", file.Path, readErr)
		}
		in := DocumentInput{URI: file.DisplayURI, Version: -1, Generation: 0, Source: src}
		summary, sumErr := ParseAndSummarize(ctx, file.Key, in)
		if sumErr != nil {
			return sumErr
		}
		next[file.Key] = nextDiskState{file: file, summary: summary}
		changed = append(changed, file.Key)
		if prev.summary == nil {
			fullRebuild = fullRebuild || (slot == nil || slot.active() == nil)
		}
	}

	for key, slot := range m.slots {
		if slot.disk == nil {
			continue
		}
		if _, ok := next[key]; ok {
			continue
		}
		changed = append(changed, key)
		if slot.open == nil {
			fullRebuild = true
		}
		slot.disk = nil
		if slot.active() == nil {
			delete(m.slots, key)
		}
	}

	for key, state := range next {
		slot := m.ensureSlotLocked(key, state.file.DisplayURI)
		slot.disk = &documentState{
			input:   DocumentInput{URI: state.file.DisplayURI, Version: -1, Generation: 0},
			summary: state.summary,
			size:    state.file.Size,
			modTime: state.file.ModTime,
		}
	}

	if len(changed) == 0 && m.snapshot.Load() != nil {
		return nil
	}
	m.publishLocked(changed, fullRebuild || m.snapshot.Load() == nil, reason, scanDuration, len(files))
	return nil
}

// RefreshDocumentWithReason refreshes one on-disk document after a watcher event.
func (m *Manager) RefreshDocumentWithReason(ctx context.Context, uri string, deleted bool, reason RebuildReason) error {
	if m == nil {
		return errors.New("nil Manager")
	}
	start := time.Now()

	displayURI, key, err := CanonicalizeDocumentURI(uri)
	if err != nil {
		return err
	}
	path, err := filePathFromDocumentURI(displayURI)
	if err != nil {
		return err
	}
	if !m.pathAllowed(path) {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	slot := m.ensureSlotLocked(key, displayURI)
	if deleted {
		m.publishDiskRemovalLocked(key, slot, reason, start)
		return nil
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			return statErr
		}
		m.publishDiskRemovalLocked(key, slot, reason, start)
		return nil
	}
	if info.IsDir() {
		return nil
	}

	if slot.disk != nil && slot.disk.size == info.Size() && slot.disk.modTime.Equal(info.ModTime()) {
		return nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	in := DocumentInput{URI: displayURI, Version: -1, Generation: 0, Source: src}
	summary, err := ParseAndSummarize(ctx, key, in)
	if err != nil {
		return err
	}
	fullRebuild := slot.disk == nil
	slot.disk = &documentState{
		input:   DocumentInput{URI: displayURI, Version: -1, Generation: 0},
		summary: summary,
		size:    info.Size(),
		modTime: info.ModTime(),
	}
	m.publishLocked([]DocumentKey{key}, fullRebuild || m.snapshot.Load() == nil, reason, time.Since(start), 1)
	return nil
}

// Snapshot returns the latest published workspace snapshot.
func (m *Manager) Snapshot() (*WorkspaceSnapshot, bool) {
	if m == nil {
		return nil, false
	}
	snap := m.snapshot.Load()
	return snap, snap != nil
}

func (m *Manager) ensureSlotLocked(key DocumentKey, displayURI string) *documentSlot {
	slot := m.slots[key]
	if slot != nil {
		if displayURI != "" {
			slot.displayURI = displayURI
		}
		return slot
	}
	slot = &documentSlot{key: key, displayURI: displayURI}
	m.slots[key] = slot
	return slot
}

func (m *Manager) clearDiskStateLocked(key DocumentKey, slot *documentSlot) bool {
	if slot.disk == nil {
		return false
	}
	slot.disk = nil
	if slot.active() == nil {
		delete(m.slots, key)
	}
	return true
}

func (m *Manager) publishDiskRemovalLocked(key DocumentKey, slot *documentSlot, reason RebuildReason, start time.Time) {
	if !m.clearDiskStateLocked(key, slot) {
		return
	}
	m.publishLocked([]DocumentKey{key}, false, reason, time.Since(start), 1)
}

func (m *Manager) publishLocked(changed []DocumentKey, fullRebuild bool, reason RebuildReason, duration time.Duration, discoveredFiles int) {
	prev := m.snapshot.Load()
	baseDocs := make(map[DocumentKey]*DocumentSummary, len(m.slots))
	for key, slot := range m.slots {
		active := slot.active()
		if active == nil || active.summary == nil {
			continue
		}
		baseDocs[key] = active.summary
	}

	impactedCount := len(baseDocs)
	if !fullRebuild && prev != nil {
		impactedCount = len(expandInvalidation(prev, changed))
	}
	next := buildSnapshot(prev, baseDocs, changed, fullRebuild, resolverConfig{
		roots:       slices.Clone(m.roots),
		includeDirs: slices.Clone(m.includeDirs),
	})
	m.snapshot.Store(next)
	scanDuration := time.Duration(0)
	if discoveredFiles > 0 {
		scanDuration = duration
	}
	m.emit(Event{
		Kind:                EventKindRebuild,
		Reason:              reason,
		Duration:            duration,
		ScanDuration:        scanDuration,
		DiscoveredFiles:     discoveredFiles,
		IndexedDocuments:    len(next.Documents),
		ImpactedDocuments:   impactedCount,
		WorkspaceGeneration: next.Generation,
	})
}

func buildSnapshot(prev *WorkspaceSnapshot, baseDocs map[DocumentKey]*DocumentSummary, changed []DocumentKey, fullRebuild bool, cfg resolverConfig) *WorkspaceSnapshot {
	if fullRebuild || prev == nil {
		changed = sortedDocumentKeys(baseDocs)
	}
	impacted := expandInvalidation(prev, changed)

	docs := make(map[DocumentKey]*DocumentSummary, len(baseDocs))
	for _, key := range sortedDocumentKeys(baseDocs) {
		base := baseDocs[key]
		if canReuseResolvedSummary(prev, impacted, key, base) {
			docs[key] = cloneSummary(prev.Documents[key])
			continue
		}
		docs[key] = cloneSummary(base)
	}

	if fullRebuild || prev == nil {
		for _, key := range sortedDocumentKeys(docs) {
			resolveIncludesForSummary(docs[key], cfg, docs)
		}
	} else {
		for key := range impacted {
			if doc := docs[key]; doc != nil {
				resolveIncludesForSummary(doc, cfg, docs)
			}
		}
	}

	symbolsByID, symbolsByQName, byDocument := buildSymbolIndexes(docs)
	if fullRebuild || prev == nil {
		for _, key := range sortedDocumentKeys(docs) {
			bindReferencesForSummary(docs[key], byDocument)
		}
	} else {
		for key := range impacted {
			if doc := docs[key]; doc != nil {
				bindReferencesForSummary(doc, byDocument)
			}
		}
	}

	graph := buildIncludeGraph(docs)
	refsByTarget := make(map[SymbolID][]ReferenceSiteID)
	issues := make([]IndexDiagnostic, 0, 8)
	for _, key := range sortedDocumentKeys(docs) {
		doc := docs[key]
		if doc == nil {
			continue
		}
		for _, ref := range doc.References {
			if ref.Binding.Status != BindingStatusBound {
				continue
			}
			refsByTarget[ref.Binding.Target] = append(refsByTarget[ref.Binding.Target], ref.ID)
		}
		issues = append(issues, doc.Diagnostics...)
	}
	for _, ids := range refsByTarget {
		slices.Sort(ids)
	}
	sortDiagnostics(issues)

	nextGen := uint64(1)
	if prev != nil {
		nextGen = prev.Generation + 1
	}
	return &WorkspaceSnapshot{
		Generation:     nextGen,
		Documents:      docs,
		SymbolsByID:    symbolsByID,
		SymbolsByQName: symbolsByQName,
		RefsByTarget:   refsByTarget,
		IncludeGraph:   graph,
		ReverseDeps:    graph.Reverse,
		SnapshotIssues: issues,
	}
}

func canReuseResolvedSummary(prev *WorkspaceSnapshot, impacted map[DocumentKey]struct{}, key DocumentKey, base *DocumentSummary) bool {
	if prev == nil || base == nil {
		return false
	}
	if _, ok := impacted[key]; ok {
		return false
	}
	prevDoc := prev.Documents[key]
	if prevDoc == nil {
		return false
	}
	return prevDoc.ContentHash == base.ContentHash
}

func expandInvalidation(prev *WorkspaceSnapshot, changed []DocumentKey) map[DocumentKey]struct{} {
	out := make(map[DocumentKey]struct{})
	if prev == nil {
		for _, key := range changed {
			out[key] = struct{}{}
		}
		return out
	}

	queue := append([]DocumentKey(nil), changed...)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if _, ok := out[key]; ok {
			continue
		}
		out[key] = struct{}{}
		for _, dep := range prev.ReverseDeps[key] {
			if _, ok := out[dep]; ok {
				continue
			}
			queue = append(queue, dep)
		}
	}
	return out
}

func cloneSummary(in *DocumentSummary) *DocumentSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Includes = slices.Clone(in.Includes)
	out.Namespaces = slices.Clone(in.Namespaces)
	out.Declarations = slices.Clone(in.Declarations)
	out.Diagnostics = cloneDiagnostics(in.Diagnostics)
	out.References = make([]ReferenceSite, len(in.References))
	for i := range in.References {
		out.References[i] = in.References[i]
		out.References[i].ExpectedKinds = cloneKinds(in.References[i].ExpectedKinds)
	}
	return &out
}

func normalizeRootPaths(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{})
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		abs, err := filepath.Abs(raw)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func normalizeIncludeDirs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{})
	for _, raw := range in {
		raw = filepath.Clean(strings.TrimSpace(raw))
		if raw == "." || raw == "" {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	return out
}

func (m *Manager) rescanLoop() {
	timer := time.NewTimer(defaultRescanInterval)
	defer timer.Stop()

	for {
		select {
		case <-m.closeCh:
			return
		case <-m.rescanReset:
		case <-timer.C:
			_ = m.RescanWorkspaceWithReason(context.Background(), RebuildReasonPeriodic)
		}

		interval := time.Duration(m.rescanInterval.Load())
		if interval <= 0 {
			interval = defaultRescanInterval
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(interval)
	}
}

func (m *Manager) setRescanIntervalForTesting(d time.Duration) {
	if m == nil {
		return
	}
	if d <= 0 {
		d = defaultRescanInterval
	}
	m.rescanInterval.Store(int64(d))
	select {
	case m.rescanReset <- struct{}{}:
	default:
	}
}

func (m *Manager) pathAllowed(path string) bool {
	path = filepath.Clean(path)
	for _, root := range m.roots {
		if pathWithin(path, root) {
			return true
		}
	}
	for _, dir := range expandedIncludeDirs(m.roots, m.includeDirs) {
		if pathWithin(path, dir) {
			return true
		}
	}
	return false
}

func expandedIncludeDirs(roots, includeDirs []string) []string {
	out := make([]string, 0, len(includeDirs)*max(len(roots), 1))
	seen := make(map[string]struct{})
	addDir := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}

	for _, inc := range includeDirs {
		if filepath.IsAbs(inc) {
			addDir(inc)
			continue
		}
		for _, root := range roots {
			addDir(filepath.Join(root, inc))
		}
	}
	return out
}

func pathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Manager) emit(event Event) {
	if m == nil || m.onEvent == nil {
		return
	}
	if len(event.RenameBlockers) != 0 {
		event.RenameBlockers = maps.Clone(event.RenameBlockers)
	}
	m.onEvent(event)
}
