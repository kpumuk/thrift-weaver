package index

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
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

type loadedDiskState struct {
	file    scannedFile
	summary *DocumentSummary
}

type diskSources struct {
	direct        bool
	opportunistic bool
}

type documentSlot struct {
	key        DocumentKey
	displayURI string
	disk       *documentState
	sources    diskSources
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

func (s *documentSlot) hasSource(kind diskSourceKind) bool {
	if s == nil {
		return false
	}
	switch kind {
	case diskSourceDirect:
		return s.sources.direct
	case diskSourceOpportunistic:
		return s.sources.opportunistic
	default:
		return false
	}
}

func (s *documentSlot) setSource(kind diskSourceKind, enabled bool) {
	if s == nil {
		return
	}
	switch kind {
	case diskSourceDirect:
		s.sources.direct = enabled
	case diskSourceOpportunistic:
		s.sources.opportunistic = enabled
	}
}

func (s *documentSlot) clearDiskIfUnused() {
	if s == nil || s.sources.direct || s.sources.opportunistic {
		return
	}
	s.disk = nil
}

type diskSourceKind uint8

const (
	diskSourceDirect diskSourceKind = iota + 1
	diskSourceOpportunistic
)

type activeDocumentIdentity struct {
	present    bool
	uri        string
	version    int32
	generation uint64
	hash       [32]byte
}

func identityForState(state *documentState) activeDocumentIdentity {
	if state == nil || state.summary == nil {
		return activeDocumentIdentity{}
	}
	return activeDocumentIdentity{
		present:    true,
		uri:        state.summary.URI,
		version:    state.summary.Version,
		generation: state.summary.Generation,
		hash:       state.summary.ContentHash,
	}
}

func sameActiveIdentity(a, b activeDocumentIdentity) bool {
	return a.present == b.present &&
		a.uri == b.uri &&
		a.version == b.version &&
		a.generation == b.generation &&
		a.hash == b.hash
}

// Manager publishes immutable workspace snapshots built from document summaries.
type Manager struct {
	mu sync.Mutex

	roots        []string
	includeDirs  []string
	maxFiles     int
	maxFileSize  int64
	parseWorkers int
	onEvent      func(Event)
	queueDepth   func() int

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
	parseWorkers := normalizeParseWorkers(opts.ParseWorkers)

	m := &Manager{
		roots:        roots,
		includeDirs:  includeDirs,
		maxFiles:     maxFiles,
		maxFileSize:  maxFileSize,
		parseWorkers: parseWorkers,
		onEvent:      opts.Hooks.OnEvent,
		queueDepth:   opts.Hooks.QueueDepth,
		slots:        make(map[DocumentKey]*documentSlot),
		rescanReset:  make(chan struct{}, 1),
		closeCh:      make(chan struct{}),
	}
	m.rescanInterval.Store(int64(defaultRescanInterval))
	go m.rescanLoop()
	return m
}

func normalizeParseWorkers(workers int) int {
	if workers > 0 {
		return workers
	}
	return min(max(runtime.GOMAXPROCS(0), 1), 4)
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
	m.publishLocked([]DocumentKey{key}, !hadActive, reason, time.Since(start), rebuildStats{}, m.discoveryCompleteLocked())
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
	m.publishLocked([]DocumentKey{key}, hadActive != hasActive, reason, time.Since(start), rebuildStats{}, m.discoveryCompleteLocked())
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
	result, err := scanWorkspace(ctx, m.roots, m.includeDirs, m.maxFiles, m.maxFileSize)
	if err != nil {
		return err
	}
	scanDuration := time.Since(start)

	cached := m.cachedDiskStates()
	next, err := m.summarizeScannedFiles(ctx, result.files, cached)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	changed, fullRebuild := m.replaceDiskStatesLocked(next, diskSourceOpportunistic)
	snapshot := m.snapshot.Load()
	if len(changed) == 0 && snapshot != nil && snapshot.DiscoveryComplete {
		return nil
	}
	m.publishLocked(changed, fullRebuild || snapshot == nil, reason, scanDuration, rebuildStats{
		discoveredFiles:       len(result.files),
		gitIgnoreSkippedPaths: result.gitIgnoreSkippedPaths,
	}, true)
	return nil
}

// RefreshOpenDocumentClosureWithReason rebuilds the on-disk closure reachable from open documents.
func (m *Manager) RefreshOpenDocumentClosureWithReason(ctx context.Context, reason RebuildReason) error {
	if m == nil {
		return errors.New("nil Manager")
	}

	start := time.Now()
	cfg := resolverConfig{
		roots:       slices.Clone(m.roots),
		includeDirs: slices.Clone(m.includeDirs),
	}

	m.mu.Lock()
	openDocs, wasDiscoveryComplete := m.openDocumentSeedsLocked()
	m.mu.Unlock()

	loaded, err := m.loadOpenDocumentClosure(ctx, cfg, openDocs)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	changed, fullRebuild := m.replaceDiskStatesLocked(loaded, diskSourceDirect)
	if len(changed) == 0 && m.snapshot.Load() != nil && wasDiscoveryComplete == m.discoveryCompleteLocked() {
		return nil
	}
	m.publishLocked(changed, fullRebuild || m.snapshot.Load() == nil, reason, time.Since(start), rebuildStats{
		directLoads: len(loaded),
	}, m.discoveryCompleteLocked())
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
		if !m.clearDiskStateLocked(key, slot) {
			return nil
		}
		if slot.open != nil {
			return nil
		}
		m.publishLocked([]DocumentKey{key}, false, reason, time.Since(start), rebuildStats{
			discoveredFiles: 1,
		}, m.discoveryCompleteLocked())
		return nil
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			return statErr
		}
		if !m.clearDiskStateLocked(key, slot) {
			return nil
		}
		if slot.open != nil {
			return nil
		}
		m.publishLocked([]DocumentKey{key}, false, reason, time.Since(start), rebuildStats{
			discoveredFiles: 1,
		}, m.discoveryCompleteLocked())
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
	before := identityForState(slot.active())
	slot.disk = &documentState{
		input:   DocumentInput{URI: displayURI, Version: -1, Generation: 0},
		summary: summary,
		size:    info.Size(),
		modTime: info.ModTime(),
	}
	slot.sources.opportunistic = true
	after := identityForState(slot.active())
	if slot.open != nil || sameActiveIdentity(before, after) {
		return nil
	}
	fullRebuild := !before.present || !after.present
	m.publishLocked([]DocumentKey{key}, fullRebuild || m.snapshot.Load() == nil, reason, time.Since(start), rebuildStats{
		discoveredFiles: 1,
	}, m.discoveryCompleteLocked())
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
	if slot == nil || slot.disk == nil {
		return false
	}
	slot.disk = nil
	slot.sources = diskSources{}
	if slot.active() == nil {
		delete(m.slots, key)
	}
	return true
}

func (m *Manager) openDocumentSeedsLocked() (map[DocumentKey]*DocumentSummary, bool) {
	openDocs := make(map[DocumentKey]*DocumentSummary)
	for key, slot := range m.slots {
		if slot == nil || slot.open == nil || slot.open.summary == nil {
			continue
		}
		openDocs[key] = cloneSummary(slot.open.summary)
	}
	return openDocs, m.discoveryCompleteLocked()
}

func (m *Manager) discoveryCompleteLocked() bool {
	snapshot := m.snapshot.Load()
	return snapshot != nil && snapshot.DiscoveryComplete
}

func (m *Manager) loadOpenDocumentClosure(ctx context.Context, cfg resolverConfig, openDocs map[DocumentKey]*DocumentSummary) (map[DocumentKey]loadedDiskState, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parser := syntax.NewReusableParser()
	defer parser.Close()

	loaded := make(map[DocumentKey]loadedDiskState)
	queue := make([]*DocumentSummary, 0, len(openDocs))
	for _, key := range sortedDocumentKeys(openDocs) {
		queue = append(queue, openDocs[key])
	}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		doc := queue[0]
		queue = queue[1:]
		if doc == nil {
			continue
		}
		for _, include := range doc.Includes {
			file, ok, err := m.resolveIncludeFile(doc.URI, include.RawPath, cfg)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, ok := openDocs[file.Key]; ok {
				continue
			}
			if _, ok := loaded[file.Key]; ok {
				continue
			}

			state, err := summarizeScannedFile(ctx, parser, file)
			if err != nil {
				return nil, err
			}
			loaded[file.Key] = state
			queue = append(queue, state.summary)
		}
	}
	return loaded, nil
}

func (m *Manager) resolveIncludeFile(uri, rawPath string, cfg resolverConfig) (scannedFile, bool, error) {
	includePath := unquoteMaybe(rawPath)
	if strings.TrimSpace(includePath) == "" {
		return scannedFile{}, false, nil
	}

	docPath, err := filePathFromDocumentURI(uri)
	if err != nil {
		return scannedFile{}, false, err
	}
	for _, base := range includeSearchCandidates(filepath.Dir(docPath), cfg.roots, cfg.includeDirs) {
		candidate := filepath.Join(base, filepath.FromSlash(includePath))
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return scannedFile{}, false, err
		}
		if info.IsDir() {
			continue
		}

		displayURI, key, err := CanonicalizeDocumentURI(candidate)
		if err != nil {
			continue
		}
		resolvedPath, err := filePathFromDocumentURI(displayURI)
		if err != nil {
			return scannedFile{}, false, err
		}
		if !m.pathAllowed(resolvedPath) {
			continue
		}
		return scannedFile{
			Path:       resolvedPath,
			DisplayURI: displayURI,
			Key:        key,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
		}, true, nil
	}
	return scannedFile{}, false, nil
}

func (m *Manager) replaceDiskStatesLocked(next map[DocumentKey]loadedDiskState, kind diskSourceKind) ([]DocumentKey, bool) {
	changed := make(map[DocumentKey]struct{}, len(next)+len(m.slots))
	fullRebuild := false
	affected := make(map[DocumentKey]struct{}, len(next)+len(m.slots))
	for key, slot := range m.slots {
		if slot == nil || !slot.hasSource(kind) {
			continue
		}
		affected[key] = struct{}{}
	}
	for key := range next {
		affected[key] = struct{}{}
	}

	before := make(map[DocumentKey]activeDocumentIdentity, len(affected))
	for key := range affected {
		before[key] = identityForState(m.slots[key].active())
	}

	for key := range affected {
		slot := m.slots[key]
		state, ok := next[key]
		if !ok {
			if slot == nil {
				continue
			}
			slot.setSource(kind, false)
			slot.clearDiskIfUnused()
			if slot.active() == nil {
				delete(m.slots, key)
			}
			continue
		}

		if slot == nil {
			slot = m.ensureSlotLocked(key, state.file.DisplayURI)
		}
		slot.displayURI = state.file.DisplayURI
		slot.setSource(kind, true)
		if sameLoadedDiskState(slot.disk, state) {
			continue
		}
		slot.disk = &documentState{
			input:   DocumentInput{URI: state.file.DisplayURI, Version: -1, Generation: 0},
			summary: state.summary,
			size:    state.file.Size,
			modTime: state.file.ModTime,
		}
	}

	for key, prev := range before {
		slot := m.slots[key]
		nextIdentity := identityForState(nil)
		if slot != nil {
			nextIdentity = identityForState(slot.active())
		}
		if sameActiveIdentity(prev, nextIdentity) {
			continue
		}
		changed[key] = struct{}{}
		if prev.present != nextIdentity.present {
			fullRebuild = true
		}
	}

	out := make([]DocumentKey, 0, len(changed))
	for key := range changed {
		out = append(out, key)
	}
	slices.Sort(out)
	return out, fullRebuild
}

func sameLoadedDiskState(current *documentState, next loadedDiskState) bool {
	if current == nil || current.summary == nil || next.summary == nil {
		return false
	}
	return current.input.URI == next.file.DisplayURI &&
		current.size == next.file.Size &&
		current.modTime.Equal(next.file.ModTime) &&
		current.summary.ContentHash == next.summary.ContentHash
}

func sameScannedFileMetadata(a, b scannedFile) bool {
	return a.Key == b.Key && a.DisplayURI == b.DisplayURI && a.Size == b.Size && a.ModTime.Equal(b.ModTime)
}

func (m *Manager) cachedDiskStates() map[DocumentKey]loadedDiskState {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[DocumentKey]loadedDiskState, len(m.slots))
	for key, slot := range m.slots {
		if slot == nil || slot.disk == nil || slot.disk.summary == nil {
			continue
		}
		out[key] = loadedDiskState{
			file: scannedFile{
				Path:       documentPathOrEmpty(slot.disk.input.URI),
				DisplayURI: slot.disk.input.URI,
				Key:        key,
				Size:       slot.disk.size,
				ModTime:    slot.disk.modTime,
			},
			summary: slot.disk.summary,
		}
	}
	return out
}

func documentPathOrEmpty(uri string) string {
	path, err := filePathFromDocumentURI(uri)
	if err != nil {
		return ""
	}
	return path
}

type rebuildStats struct {
	directLoads           int
	discoveredFiles       int
	gitIgnoreSkippedPaths int
}

func (m *Manager) publishLocked(changed []DocumentKey, fullRebuild bool, reason RebuildReason, duration time.Duration, stats rebuildStats, discoveryComplete bool) {
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
	}, discoveryComplete)
	m.snapshot.Store(next)
	scanDuration := time.Duration(0)
	if stats.discoveredFiles > 0 {
		scanDuration = duration
	}
	directDocuments, opportunisticDocuments := m.sourceCountsLocked()
	m.emit(Event{
		Kind:                   EventKindRebuild,
		Reason:                 reason,
		Duration:               duration,
		ScanDuration:           scanDuration,
		DirectLoads:            stats.directLoads,
		DiscoveredFiles:        stats.discoveredFiles,
		GitIgnoreSkippedPaths:  stats.gitIgnoreSkippedPaths,
		IndexedDocuments:       len(next.Documents),
		DirectDocuments:        directDocuments,
		OpportunisticDocuments: opportunisticDocuments,
		ImpactedDocuments:      impactedCount,
		WorkspaceGeneration:    next.Generation,
		DiscoveryComplete:      next.DiscoveryComplete,
	})
}

func (m *Manager) sourceCountsLocked() (direct int, opportunistic int) {
	for _, slot := range m.slots {
		if slot == nil || slot.active() == nil {
			continue
		}
		if slot.hasSource(diskSourceDirect) {
			direct++
		}
		if slot.hasSource(diskSourceOpportunistic) {
			opportunistic++
		}
	}
	return direct, opportunistic
}

func buildSnapshot(prev *WorkspaceSnapshot, baseDocs map[DocumentKey]*DocumentSummary, changed []DocumentKey, fullRebuild bool, cfg resolverConfig, discoveryComplete bool) *WorkspaceSnapshot {
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
		Generation:        nextGen,
		DiscoveryComplete: discoveryComplete,
		Documents:         docs,
		SymbolsByID:       symbolsByID,
		SymbolsByQName:    symbolsByQName,
		RefsByTarget:      refsByTarget,
		IncludeGraph:      graph,
		ReverseDeps:       graph.Reverse,
		SnapshotIssues:    issues,
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
		abs, err := normalizeRuntimePath(raw)
		if err != nil {
			continue
		}
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
		raw = strings.TrimSpace(raw)
		if filepath.IsAbs(raw) {
			normalized, err := normalizeRuntimePath(raw)
			if err == nil {
				raw = normalized
			}
		} else {
			raw = filepath.Clean(raw)
		}
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
			snapshot := m.snapshot.Load()
			switch {
			case snapshot == nil:
			case snapshot.DiscoveryComplete:
				_ = m.RescanWorkspaceWithReason(context.Background(), RebuildReasonPeriodic)
			default:
				_ = m.RefreshOpenDocumentClosureWithReason(context.Background(), RebuildReasonPeriodic)
				_ = m.RescanWorkspaceWithReason(context.Background(), RebuildReasonPeriodic)
			}
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
	if m.queueDepth != nil {
		event.BackgroundQueueDepth = m.queueDepth()
	}
	if len(event.RenameBlockers) != 0 {
		event.RenameBlockers = maps.Clone(event.RenameBlockers)
	}
	m.onEvent(event)
}
