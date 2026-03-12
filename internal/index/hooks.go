package index

import "time"

// EventKind identifies the category of a workspace index hook event.
type EventKind string

const (
	// EventKindRebuild reports a published workspace snapshot rebuild.
	EventKindRebuild EventKind = "rebuild"
	// EventKindQuery reports a workspace query latency sample.
	EventKindQuery EventKind = "query"
	// EventKindRenameBlockers reports rename blocker counts by code.
	EventKindRenameBlockers EventKind = "rename_blockers"
)

// RebuildReason identifies the reason for a workspace rebuild.
type RebuildReason string

const (
	// RebuildReasonOpen marks a rebuild caused by opening a document.
	RebuildReasonOpen RebuildReason = "open"
	// RebuildReasonChange marks a rebuild caused by document content changes.
	RebuildReasonChange RebuildReason = "change"
	// RebuildReasonClose marks a rebuild caused by closing a document.
	RebuildReasonClose RebuildReason = "close"
	// RebuildReasonWatch marks a rebuild caused by filesystem watcher events.
	RebuildReasonWatch RebuildReason = "watch"
	// RebuildReasonManualRescan marks a rebuild caused by an explicit rescan request.
	RebuildReasonManualRescan RebuildReason = "manual-rescan"
)

// Event is a structured observability hook payload for workspace indexing.
type Event struct {
	Kind                   EventKind
	Reason                 RebuildReason
	Method                 string
	Duration               time.Duration
	ScanDuration           time.Duration
	DirectLoads            int
	DiscoveredFiles        int
	GitIgnoreSkippedPaths  int
	IndexedDocuments       int
	DirectDocuments        int
	OpportunisticDocuments int
	ImpactedDocuments      int
	WorkspaceGeneration    uint64
	DiscoveryComplete      bool
	BackgroundQueueDepth   int
	RenameBlockers         map[string]int
}

// Hooks configures structured observability callbacks for workspace indexing.
type Hooks struct {
	OnEvent    func(Event)
	QueueDepth func() int
}
