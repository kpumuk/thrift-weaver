package index

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

type queryContext struct {
	snapshot *WorkspaceSnapshot
	view     *DocumentView
	meta     QueryMeta
	offset   text.ByteOffset
}

type renameTarget struct {
	symbol Symbol
	span   text.Span
}

// Definition resolves the declaration location for the symbol or reference under pos.
func (m *Manager) Definition(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (locations []Location, meta QueryMeta, err error) {
	start := time.Now()
	defer func() { m.observeQuery("definition", start, meta) }()

	qctx, symbol, err := m.queryTargetSymbol(ctx, doc, pos)
	if err != nil {
		return nil, qctx.meta, err
	}
	if symbol == nil {
		return []Location{}, qctx.meta, nil
	}
	return []Location{{URI: symbol.URI, Span: symbol.NameSpan}}, qctx.meta, nil
}

// References resolves all indexed reference sites for the symbol under pos.
func (m *Manager) References(ctx context.Context, doc QueryDocument, pos text.UTF16Position, includeDecl bool) (locations []Location, meta QueryMeta, err error) {
	start := time.Now()
	defer func() { m.observeQuery("references", start, meta) }()

	qctx, symbol, err := m.queryTargetSymbol(ctx, doc, pos)
	if err != nil {
		return nil, qctx.meta, err
	}
	if symbol == nil {
		return []Location{}, qctx.meta, nil
	}
	if !qctx.meta.DiscoveryComplete {
		return nil, qctx.meta, ErrWorkspaceIncomplete
	}

	refs := qctx.snapshot.RefsByTarget[symbol.ID]
	out := make([]Location, 0, len(refs)+1)
	if includeDecl {
		out = append(out, Location{URI: symbol.URI, Span: symbol.NameSpan})
	}
	for _, refID := range refs {
		ref, ok := referenceByID(qctx.snapshot, refID)
		if !ok {
			continue
		}
		out = append(out, Location{URI: ref.URI, Span: ref.Span})
	}
	sortLocations(out)
	return out, qctx.meta, nil
}

// WorkspaceSymbols searches indexed top-level symbols by name.
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) (symbols []WorkspaceSymbol, meta QueryMeta, err error) {
	start := time.Now()
	defer func() { m.observeQuery("workspace/symbol", start, meta) }()

	if err := contextOrBackground(ctx).Err(); err != nil {
		return nil, QueryMeta{}, err
	}

	snapshot, ok := m.Snapshot()
	if !ok || snapshot == nil {
		return nil, QueryMeta{}, ErrWorkspaceClosed
	}
	meta = QueryMeta{
		WorkspaceGeneration: snapshot.Generation,
		DiscoveryComplete:   snapshot.DiscoveryComplete,
	}
	query = strings.TrimSpace(strings.ToLower(query))

	out := make([]WorkspaceSymbol, 0, len(snapshot.SymbolsByID))
	for _, key := range sortedDocumentKeys(snapshot.Documents) {
		doc := snapshot.Documents[key]
		if doc == nil {
			continue
		}
		for _, symbol := range doc.Declarations {
			if query != "" && !strings.Contains(strings.ToLower(symbol.Name), query) {
				continue
			}
			out = append(out, WorkspaceSymbol{
				Name:          symbol.Name,
				Kind:          symbol.Kind,
				URI:           symbol.URI,
				Span:          symbol.NameSpan,
				ContainerName: symbol.QName.DeclaringURI,
			})
		}
	}
	slices.SortFunc(out, compareWorkspaceSymbols)
	return out, meta, nil
}

// PrepareRename validates that the position resolves to an exact rename target.
func (m *Manager) PrepareRename(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (result *PrepareRenameResult, meta QueryMeta, err error) {
	start := time.Now()
	defer func() { m.observeQuery("prepareRename", start, meta) }()

	qctx, target, blockers, err := m.queryRenameTarget(ctx, doc, pos)
	if err != nil {
		return nil, qctx.meta, err
	}
	if len(blockers) > 0 {
		m.observeRenameBlockers(blockers)
		return &PrepareRenameResult{Blockers: blockers}, qctx.meta, ErrRenameBlocked
	}
	return &PrepareRenameResult{
		Placeholder: target.symbol.Name,
		Span:        target.span,
	}, qctx.meta, nil
}

// Rename plans a fail-closed workspace rename for an indexed top-level declaration.
func (m *Manager) Rename(ctx context.Context, doc QueryDocument, pos text.UTF16Position, newName string) (result *RenameResult, meta QueryMeta, err error) {
	start := time.Now()
	defer func() { m.observeQuery("rename", start, meta) }()

	qctx, target, blockers, err := m.queryRenameTarget(ctx, doc, pos)
	if err != nil {
		return nil, qctx.meta, err
	}
	placeholder := ""
	if target != nil {
		placeholder = target.symbol.Name
	}
	if len(blockers) > 0 {
		m.observeRenameBlockers(blockers)
		return &RenameResult{Placeholder: placeholder, Blockers: blockers}, qctx.meta, ErrRenameBlocked
	}
	if !qctx.meta.DiscoveryComplete {
		return &RenameResult{Placeholder: placeholder}, qctx.meta, ErrWorkspaceIncomplete
	}

	if blocker := validateRenameIdentifier(target.symbol, newName); blocker != nil {
		m.observeRenameBlockers([]IndexDiagnostic{*blocker})
		return &RenameResult{
			Placeholder: placeholder,
			Blockers:    []IndexDiagnostic{*blocker},
		}, qctx.meta, ErrRenameBlocked
	}

	result, blockers, err = m.planRename(qctx, target.symbol, newName)
	if err != nil {
		if len(blockers) > 0 {
			m.observeRenameBlockers(blockers)
		}
		if errors.Is(err, ErrContentModified) {
			return &RenameResult{Placeholder: placeholder, Blockers: blockers}, qctx.meta, err
		}
		return nil, qctx.meta, err
	}
	if len(blockers) > 0 {
		m.observeRenameBlockers(blockers)
		return &RenameResult{
			Placeholder: placeholder,
			Blockers:    blockers,
		}, qctx.meta, ErrRenameBlocked
	}

	result.Placeholder = placeholder
	return result, qctx.meta, nil
}

func (m *Manager) queryTargetSymbol(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (queryContext, *Symbol, error) {
	qctx, err := m.queryDocumentContext(ctx, doc, pos)
	if err != nil {
		return qctx, nil, err
	}

	if symbol, ok := declarationAtOffset(qctx.view.Document, qctx.offset); ok {
		return qctx, &symbol, nil
	}
	ref, ok := referenceAtOffset(qctx.view.Document, qctx.offset)
	if !ok || ref.Binding.Status != BindingStatusBound {
		return qctx, nil, nil
	}
	symbol, ok := qctx.snapshot.SymbolsByID[ref.Binding.Target]
	if !ok {
		return qctx, nil, nil
	}
	return qctx, &symbol, nil
}

func (m *Manager) queryRenameTarget(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (queryContext, *renameTarget, []IndexDiagnostic, error) {
	qctx, err := m.queryDocumentContext(ctx, doc, pos)
	if err != nil {
		return qctx, nil, nil, err
	}

	if symbol, ok := declarationAtOffset(qctx.view.Document, qctx.offset); ok {
		return qctx, &renameTarget{symbol: symbol, span: symbol.NameSpan}, nil, nil
	}

	ref, ok := referenceAtOffset(qctx.view.Document, qctx.offset)
	if !ok {
		return qctx, nil, []IndexDiagnostic{renameBlocker(
			qctx.view.Document.URI,
			DiagnosticRenameTargetUnavailable,
			"rename requires a declaration or exact bound reference",
			text.Span{Start: qctx.offset, End: qctx.offset},
		)}, nil
	}

	switch ref.Binding.Status {
	case BindingStatusBound:
		symbol, ok := qctx.snapshot.SymbolsByID[ref.Binding.Target]
		if !ok {
			return qctx, nil, []IndexDiagnostic{renameBlocker(
				ref.URI,
				DiagnosticRenameTargetUnavailable,
				"rename target is no longer available",
				renameReferenceSpan(ref),
			)}, nil
		}
		return qctx, &renameTarget{symbol: symbol, span: renameReferenceSpan(ref)}, nil, nil
	case BindingStatusTainted:
		return qctx, nil, []IndexDiagnostic{renameBlocker(
			ref.URI,
			DiagnosticRenameTargetTainted,
			"rename is blocked because the reference is tainted by parser recovery",
			renameReferenceSpan(ref),
		)}, nil
	case BindingStatusAmbiguous:
		return qctx, nil, []IndexDiagnostic{renameBlocker(
			ref.URI,
			DiagnosticRenameTargetAmbiguous,
			"rename is blocked because the reference is ambiguous",
			renameReferenceSpan(ref),
		)}, nil
	case BindingStatusUnknown, BindingStatusUnresolved, BindingStatusUnsupported:
		return qctx, nil, []IndexDiagnostic{renameBlocker(
			ref.URI,
			DiagnosticRenameTargetUnavailable,
			"rename requires an exact bound reference",
			renameReferenceSpan(ref),
		)}, nil
	}

	return qctx, nil, []IndexDiagnostic{renameBlocker(
		ref.URI,
		DiagnosticRenameTargetUnavailable,
		"rename requires an exact bound reference",
		renameReferenceSpan(ref),
	)}, nil
}

func (m *Manager) queryDocumentContext(ctx context.Context, doc QueryDocument, pos text.UTF16Position) (queryContext, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return queryContext{}, err
	}

	snapshot, ok := m.Snapshot()
	if !ok || snapshot == nil {
		return queryContext{}, ErrWorkspaceClosed
	}
	view, ok, err := ViewForDocument(snapshot, doc.URI)
	if err != nil {
		return queryContext{}, err
	}
	if !ok || view.Document.Version != doc.Version || view.Document.Generation != doc.Generation {
		return queryContext{}, ErrContentModified
	}

	src, err := m.queryDocumentSource(view.Document)
	if err != nil {
		return queryContext{}, err
	}
	lineIndex := text.NewLineIndex(src)
	offset, err := lineIndex.UTF16PositionToOffset(pos)
	if err != nil {
		return queryContext{}, err
	}

	meta := QueryMeta{
		WorkspaceGeneration: snapshot.Generation,
		DocumentURI:         view.Document.URI,
		DocumentVersion:     view.Document.Version,
		DocumentGeneration:  view.Document.Generation,
		DiscoveryComplete:   snapshot.DiscoveryComplete,
	}
	return queryContext{
		snapshot: snapshot,
		view:     view,
		meta:     meta,
		offset:   offset,
	}, nil
}

func (m *Manager) queryDocumentSource(doc *DocumentSummary) ([]byte, error) {
	if m == nil || doc == nil {
		return nil, errors.New("query document source unavailable")
	}

	m.mu.Lock()
	slot := m.slots[doc.Key]
	var src []byte
	if slot != nil && slot.open != nil && slot.open.summary != nil {
		if slot.open.summary.Version == doc.Version && slot.open.summary.Generation == doc.Generation {
			src = slices.Clone(slot.open.input.Source)
		}
	}
	m.mu.Unlock()
	if src != nil {
		return src, nil
	}

	path, err := filePathFromDocumentURI(doc.URI)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (m *Manager) planRename(qctx queryContext, target Symbol, newName string) (*RenameResult, []IndexDiagnostic, error) {
	targetDoc := qctx.snapshot.Documents[target.Key]
	if targetDoc == nil {
		return nil, nil, ErrContentModified
	}

	references := referencesForTarget(qctx.snapshot, target.ID)
	blockers := renameCollisionBlockers(target, targetDoc, references, newName)
	if len(blockers) > 0 {
		return nil, blockers, nil
	}

	documents := make(map[DocumentKey]*VersionedDocumentEdits)
	captured := make(map[DocumentKey]*DocumentSummary)
	addEdits := func(doc *DocumentSummary, edits ...text.ByteEdit) {
		if doc == nil || len(edits) == 0 {
			return
		}
		entry := documents[doc.Key]
		if entry == nil {
			entry = &VersionedDocumentEdits{
				URI:         doc.URI,
				ContentHash: doc.ContentHash,
			}
			if doc.Version >= 0 {
				entry.Version = &doc.Version
			}
			documents[doc.Key] = entry
			captured[doc.Key] = doc
		}
		entry.Edits = append(entry.Edits, edits...)
	}

	replacement := []byte(newName)
	addEdits(targetDoc, text.ByteEdit{Span: target.NameSpan, NewText: replacement})
	for _, ref := range references {
		doc, ok := queryDocumentSummary(qctx.snapshot, ref.URI)
		if !ok {
			return nil, nil, ErrContentModified
		}
		addEdits(doc, text.ByteEdit{Span: renameReferenceSpan(ref), NewText: replacement})
	}

	keys := make([]DocumentKey, 0, len(documents))
	for key, docEdits := range documents {
		doc := captured[key]
		if doc == nil {
			return nil, nil, ErrContentModified
		}
		src, err := m.queryDocumentSource(doc)
		if err != nil {
			return nil, nil, err
		}
		if err := text.ValidateEdits(text.ByteOffset(len(src)), docEdits.Edits); err != nil {
			return nil, nil, err
		}
		sortByteEditsDescending(docEdits.Edits)
		if err := m.validateRenameDocument(doc); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b DocumentKey) int {
		return strings.Compare(documents[a].URI, documents[b].URI)
	})

	out := make([]VersionedDocumentEdits, 0, len(keys))
	for _, key := range keys {
		out = append(out, *documents[key])
	}
	return &RenameResult{Documents: out}, nil, nil
}

func validateRenameIdentifier(target Symbol, newName string) *IndexDiagnostic {
	if strings.TrimSpace(newName) != newName || newName == "" {
		return invalidRenameIdentifier(target, "new name must be a bare Thrift identifier")
	}

	result := lexer.Lex([]byte(newName))
	if len(result.Diagnostics) != 0 || len(result.Tokens) != 2 {
		return invalidRenameIdentifier(target, "new name must be a bare Thrift identifier")
	}
	if result.Tokens[0].Kind != lexer.TokenIdentifier || len(result.Tokens[0].Leading) != 0 {
		return invalidRenameIdentifier(target, "new name must be a non-keyword Thrift identifier")
	}
	if result.Tokens[0].Span.Start != 0 || int(result.Tokens[0].Span.End) != len(newName) {
		return invalidRenameIdentifier(target, "new name must not contain qualification or extra tokens")
	}
	if result.Tokens[1].Kind != lexer.TokenEOF {
		return invalidRenameIdentifier(target, "new name must not contain qualification or extra tokens")
	}
	return nil
}

func renameCollisionBlockers(target Symbol, targetDoc *DocumentSummary, references []ReferenceSite, newName string) []IndexDiagnostic {
	if targetDoc == nil {
		return nil
	}

	postRename := make(map[string][]Symbol, len(targetDoc.Declarations))
	for _, symbol := range targetDoc.Declarations {
		updated := symbol
		if symbol.ID == target.ID {
			updated.Name = newName
			updated.QName = QualifiedName{DeclaringURI: updated.URI, Name: newName}
		}
		postRename[updated.Name] = append(postRename[updated.Name], updated)
	}

	blockers := make([]IndexDiagnostic, 0)
	for _, ref := range references {
		binding := bindAgainstSymbols(postRename[newName], ref.ExpectedKinds)
		if binding.Status == BindingStatusBound && binding.Target == target.ID {
			continue
		}
		message := "rename would change this reference binding"
		switch binding.Status {
		case BindingStatusAmbiguous:
			message = "rename would make this reference ambiguous"
		case BindingStatusUnresolved:
			message = "rename would leave this reference unresolved"
		case BindingStatusBound:
			message = "rename would rebind this reference to a different declaration"
		case BindingStatusUnknown, BindingStatusTainted, BindingStatusUnsupported:
			message = "rename would leave this reference in an invalid state"
		}
		blockers = append(blockers, renameBlocker(ref.URI, DiagnosticRenameCollision, message, renameReferenceSpan(ref)))
	}
	return blockers
}

func referencesForTarget(snapshot *WorkspaceSnapshot, id SymbolID) []ReferenceSite {
	if snapshot == nil {
		return nil
	}

	refIDs := snapshot.RefsByTarget[id]
	out := make([]ReferenceSite, 0, len(refIDs))
	for _, refID := range refIDs {
		ref, ok := referenceByID(snapshot, refID)
		if !ok {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func queryDocumentSummary(snapshot *WorkspaceSnapshot, uri string) (*DocumentSummary, bool) {
	if snapshot == nil {
		return nil, false
	}
	_, key, err := CanonicalizeDocumentURI(uri)
	if err != nil {
		return nil, false
	}
	doc := snapshot.Documents[key]
	return doc, doc != nil
}

func (m *Manager) validateRenameDocument(doc *DocumentSummary) error {
	if doc == nil {
		return ErrContentModified
	}

	m.mu.Lock()
	slot := m.slots[doc.Key]
	var openSummary *DocumentSummary
	if slot != nil && slot.open != nil {
		openSummary = slot.open.summary
	}
	m.mu.Unlock()

	if doc.Version >= 0 {
		if openSummary == nil || openSummary.Version != doc.Version || openSummary.Generation != doc.Generation {
			return ErrContentModified
		}
		return nil
	}
	if openSummary != nil {
		return ErrContentModified
	}

	path, err := filePathFromDocumentURI(doc.URI)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return ErrContentModified
	}
	if sha256.Sum256(src) != doc.ContentHash {
		return ErrContentModified
	}
	return nil
}

func declarationAtOffset(doc *DocumentSummary, offset text.ByteOffset) (Symbol, bool) {
	if doc == nil {
		return Symbol{}, false
	}
	for _, symbol := range doc.Declarations {
		if symbol.NameSpan.Contains(offset) {
			return symbol, true
		}
	}
	return Symbol{}, false
}

func referenceAtOffset(doc *DocumentSummary, offset text.ByteOffset) (ReferenceSite, bool) {
	if doc == nil {
		return ReferenceSite{}, false
	}
	for _, ref := range doc.References {
		if ref.Span.Contains(offset) {
			return ref, true
		}
	}
	return ReferenceSite{}, false
}

func referenceByID(snapshot *WorkspaceSnapshot, id ReferenceSiteID) (ReferenceSite, bool) {
	if snapshot == nil {
		return ReferenceSite{}, false
	}
	for _, key := range sortedDocumentKeys(snapshot.Documents) {
		doc := snapshot.Documents[key]
		if doc == nil {
			continue
		}
		for _, ref := range doc.References {
			if ref.ID == id {
				return ref, true
			}
		}
	}
	return ReferenceSite{}, false
}

func renameReferenceSpan(ref ReferenceSite) text.Span {
	if ref.Qualifier == "" || ref.Name == "" || !ref.Span.IsValid() {
		return ref.Span
	}
	nameLen := text.ByteOffset(len(ref.Name))
	if nameLen > ref.Span.Len() {
		return ref.Span
	}
	return text.Span{Start: ref.Span.End - nameLen, End: ref.Span.End}
}

func renameBlocker(uri, code, message string, span text.Span) IndexDiagnostic {
	return newDiagnostic(uri, code, message, syntax.SeverityError, span)
}

func invalidRenameIdentifier(target Symbol, message string) *IndexDiagnostic {
	diag := renameBlocker(target.URI, DiagnosticRenameInvalidName, message, target.NameSpan)
	return &diag
}

func sortLocations(locations []Location) {
	slices.SortFunc(locations, func(a, b Location) int {
		if a.URI < b.URI {
			return -1
		}
		if a.URI > b.URI {
			return 1
		}
		if a.Span.Start < b.Span.Start {
			return -1
		}
		if a.Span.Start > b.Span.Start {
			return 1
		}
		if a.Span.End < b.Span.End {
			return -1
		}
		if a.Span.End > b.Span.End {
			return 1
		}
		return 0
	})
}

func sortByteEditsDescending(edits []text.ByteEdit) {
	slices.SortFunc(edits, func(a, b text.ByteEdit) int {
		if a.Span.Start > b.Span.Start {
			return -1
		}
		if a.Span.Start < b.Span.Start {
			return 1
		}
		if a.Span.End > b.Span.End {
			return -1
		}
		if a.Span.End < b.Span.End {
			return 1
		}
		return 0
	})
}

func compareWorkspaceSymbols(a, b WorkspaceSymbol) int {
	if a.Name < b.Name {
		return -1
	}
	if a.Name > b.Name {
		return 1
	}
	if a.URI < b.URI {
		return -1
	}
	if a.URI > b.URI {
		return 1
	}
	if a.Span.Start < b.Span.Start {
		return -1
	}
	if a.Span.Start > b.Span.Start {
		return 1
	}
	return 0
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (m *Manager) observeQuery(method string, start time.Time, meta QueryMeta) {
	m.emit(Event{
		Kind:                EventKindQuery,
		Method:              method,
		Duration:            time.Since(start),
		WorkspaceGeneration: meta.WorkspaceGeneration,
	})
}

func (m *Manager) observeRenameBlockers(blockers []IndexDiagnostic) {
	if len(blockers) == 0 {
		return
	}

	counts := make(map[string]int, len(blockers))
	for _, blocker := range blockers {
		counts[blocker.Code]++
	}
	m.emit(Event{
		Kind:           EventKindRenameBlockers,
		RenameBlockers: counts,
	})
}
