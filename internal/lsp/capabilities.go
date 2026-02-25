package lsp

// DefaultServerCapabilities returns the normative v1 capability set.
func DefaultServerCapabilities() ServerCapabilities {
	return ServerCapabilities{
		TextDocumentSync: TextDocumentSyncOptions{
			OpenClose: true,
			Change:    TextDocumentSyncKindIncremental,
		},
		DocumentFormattingProvider:      true,
		DocumentRangeFormattingProvider: true,
		DocumentSymbolProvider:          true,
		FoldingRangeProvider:            true,
		SelectionRangeProvider:          true,
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
