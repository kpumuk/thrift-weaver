package lsp

// DefaultServerCapabilities returns the normative v1 capability set.
func DefaultServerCapabilities() ServerCapabilities {
	return ServerCapabilities{
		TextDocumentSync: TextDocumentSyncOptions{
			OpenClose: true,
			Change:    TextDocumentSyncKindIncremental,
			Save:      &SaveOptions{IncludeText: false},
		},
		DefinitionProvider:              true,
		ReferencesProvider:              true,
		RenameProvider:                  true,
		DocumentFormattingProvider:      true,
		DocumentRangeFormattingProvider: true,
		DocumentSymbolProvider:          true,
		FoldingRangeProvider:            true,
		SelectionRangeProvider:          true,
		WorkspaceSymbolProvider:         true,
		SemanticTokensProvider: &SemanticTokensOptions{
			Legend: SemanticTokensLegend{
				TokenTypes:     semanticTokenLegendTypes(),
				TokenModifiers: semanticTokenLegendModifiers(),
			},
			Full:  true,
			Range: false,
		},
	}
}
