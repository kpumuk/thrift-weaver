package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kpumuk/thrift-weaver/internal/index"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

type workspaceLocationQuery func(context.Context, *index.Manager, index.QueryDocument, itext.UTF16Position) ([]index.Location, error)

// Definition handles textDocument/definition.
func (s *Server) Definition(ctx context.Context, p DefinitionParams) ([]Location, error) {
	return s.queryWorkspaceLocations(ctx, p.TextDocument.URI, p.Position, func(ctx context.Context, manager *index.Manager, doc index.QueryDocument, pos itext.UTF16Position) ([]index.Location, error) {
		locations, _, err := manager.Definition(ctx, doc, pos)
		return locations, err
	})
}

// References handles textDocument/references.
func (s *Server) References(ctx context.Context, p ReferenceParams) ([]Location, error) {
	return s.queryWorkspaceLocations(ctx, p.TextDocument.URI, p.Position, func(ctx context.Context, manager *index.Manager, doc index.QueryDocument, pos itext.UTF16Position) ([]index.Location, error) {
		locations, _, err := manager.References(ctx, doc, pos, p.Context.IncludeDeclaration)
		return locations, err
	})
}

// WorkspaceSymbols handles workspace/symbol.
func (s *Server) WorkspaceSymbols(ctx context.Context, p WorkspaceSymbolParams) ([]SymbolInformation, error) {
	manager := s.workspaceManager()
	if manager == nil {
		return nil, index.ErrWorkspaceClosed
	}

	symbols, _, err := manager.WorkspaceSymbols(ctx, p.Query)
	if err != nil {
		return nil, err
	}

	out := make([]SymbolInformation, 0, len(symbols))
	for _, symbol := range symbols {
		location, err := s.lspLocationFromIndexLocation(index.Location{URI: symbol.URI, Span: symbol.Span})
		if err != nil {
			return nil, fmt.Errorf("workspace symbol %s: %w", symbol.Name, err)
		}
		out = append(out, SymbolInformation{
			Name:          symbol.Name,
			Kind:          lspSymbolKindForIndexKind(symbol.Kind),
			Location:      location,
			ContainerName: symbol.ContainerName,
		})
	}
	return out, nil
}

func (s *Server) queryDocumentForWorkspaceRequest(ctx context.Context, uri string) (*index.Manager, index.QueryDocument, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, index.QueryDocument{}, err
	}

	manager, err := s.ensureWorkspaceManagerForURI(ctx, uri)
	if err != nil {
		return nil, index.QueryDocument{}, err
	}
	if manager == nil {
		return nil, index.QueryDocument{}, index.ErrWorkspaceClosed
	}

	snap, err := s.formattingSnapshot(uri, nil)
	if err != nil {
		return nil, index.QueryDocument{}, err
	}
	return manager, index.QueryDocument{
		URI:        snap.URI,
		Version:    snap.Version,
		Generation: snap.Generation,
	}, nil
}

func (s *Server) queryWorkspaceLocations(ctx context.Context, uri string, pos Position, query workspaceLocationQuery) ([]Location, error) {
	manager, doc, err := s.queryDocumentForWorkspaceRequest(ctx, uri)
	if err != nil {
		return nil, err
	}

	locations, err := query(ctx, manager, doc, utf16PositionFromLSP(pos))
	if err != nil {
		return nil, err
	}
	return s.lspLocationsFromIndexLocations(locations)
}

func (s *Server) lspLocationsFromIndexLocations(locations []index.Location) ([]Location, error) {
	out := make([]Location, 0, len(locations))
	for _, location := range locations {
		mapped, err := s.lspLocationFromIndexLocation(location)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return out, nil
}

func (s *Server) lspLocationFromIndexLocation(location index.Location) (Location, error) {
	li, err := s.lineIndexForDocumentURI(location.URI)
	if err != nil {
		return Location{}, err
	}
	rng, err := lspRangeFromSpan(li, location.Span)
	if err != nil {
		return Location{}, err
	}
	return Location{URI: location.URI, Range: rng}, nil
}

func (s *Server) lineIndexForDocumentURI(uri string) (*itext.LineIndex, error) {
	if s != nil {
		snap, err := s.latestSnapshot(uri)
		if err != nil && !errors.Is(err, ErrDocumentNotOpen) {
			return nil, err
		}
		if snap != nil && snap.Tree != nil && snap.Tree.LineIndex != nil {
			return snap.Tree.LineIndex, nil
		}
	}

	path, err := filePathFromDocumentURI(uri)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return itext.NewLineIndex(src), nil
}

func utf16PositionFromLSP(pos Position) itext.UTF16Position {
	return itext.UTF16Position{Line: pos.Line, Character: pos.Character}
}

func lspSymbolKindForIndexKind(kind index.SymbolKind) int {
	switch kind {
	case index.SymbolKindTypedef:
		return 26 // TypeParameter
	case index.SymbolKindConst:
		return 14 // Constant
	case index.SymbolKindEnum, index.SymbolKindSenum:
		return 10 // Enum
	case index.SymbolKindStruct, index.SymbolKindUnion, index.SymbolKindException:
		return 23 // Struct
	case index.SymbolKindService:
		return 11 // Interface
	default:
		return 13 // Variable
	}
}
