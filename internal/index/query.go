package index

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

type queryContext struct {
	snapshot *WorkspaceSnapshot
	view     *DocumentView
	meta     QueryMeta
	offset   text.ByteOffset
}

// Definition resolves the declaration location for the symbol or reference under pos.
func (m *Manager) Definition(ctx context.Context, doc QueryDocument, pos text.UTF16Position) ([]Location, QueryMeta, error) {
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
func (m *Manager) References(ctx context.Context, doc QueryDocument, pos text.UTF16Position, includeDecl bool) ([]Location, QueryMeta, error) {
	qctx, symbol, err := m.queryTargetSymbol(ctx, doc, pos)
	if err != nil {
		return nil, qctx.meta, err
	}
	if symbol == nil {
		return []Location{}, qctx.meta, nil
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
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) ([]WorkspaceSymbol, QueryMeta, error) {
	if err := contextOrBackground(ctx).Err(); err != nil {
		return nil, QueryMeta{}, err
	}

	snapshot, ok := m.Snapshot()
	if !ok || snapshot == nil {
		return nil, QueryMeta{}, ErrWorkspaceClosed
	}
	meta := QueryMeta{WorkspaceGeneration: snapshot.Generation}
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
