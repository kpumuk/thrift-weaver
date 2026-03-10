package index

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

type resolverConfig struct {
	roots       []string
	includeDirs []string
}

func resolveIncludesForSummary(summary *DocumentSummary, cfg resolverConfig, docs map[DocumentKey]*DocumentSummary) {
	if summary == nil {
		return
	}

	aliasCounts := make(map[string]int, len(summary.Includes))
	resolvedByKey := make(map[DocumentKey]string, len(summary.Includes))
	for i := range summary.Includes {
		edge := &summary.Includes[i]
		aliasCounts[edge.Alias]++

		resolvedURI, resolvedKey, ok := resolveIncludePath(summary.URI, edge.RawPath, cfg, docs)
		if !ok {
			edge.ResolvedURI = ""
			edge.ResolvedKey = ""
			edge.Status = IncludeStatusMissing
			summary.Diagnostics = append(summary.Diagnostics, newDiagnostic(
				summary.URI,
				DiagnosticIncludeMissing,
				fmt.Sprintf("include %q could not be resolved", unquoteMaybe(edge.RawPath)),
				syntax.SeverityError,
				edge.Span,
			))
			continue
		}

		edge.ResolvedURI = resolvedURI
		edge.ResolvedKey = resolvedKey
		edge.Status = IncludeStatusResolved
		if prevRaw, ok := resolvedByKey[resolvedKey]; ok && prevRaw != edge.RawPath {
			summary.Diagnostics = append(summary.Diagnostics, newDiagnostic(
				summary.URI,
				DiagnosticIncludeDuplicatePath,
				fmt.Sprintf("include %q resolves to the same target as %q", unquoteMaybe(edge.RawPath), unquoteMaybe(prevRaw)),
				syntax.SeverityWarning,
				edge.Span,
			))
			continue
		}
		resolvedByKey[resolvedKey] = edge.RawPath
	}

	for i := range summary.Includes {
		edge := &summary.Includes[i]
		if aliasCounts[edge.Alias] < 2 {
			continue
		}
		edge.Status = IncludeStatusAliasConflict
		summary.Diagnostics = append(summary.Diagnostics, newDiagnostic(
			summary.URI,
			DiagnosticIncludeAliasConflict,
			fmt.Sprintf("include alias %q is duplicated within the same document", edge.Alias),
			syntax.SeverityError,
			edge.Span,
		))
	}
}

func resolveIncludePath(uri string, rawPath string, cfg resolverConfig, docs map[DocumentKey]*DocumentSummary) (string, DocumentKey, bool) {
	includePath := unquoteMaybe(rawPath)
	if strings.TrimSpace(includePath) == "" {
		return "", "", false
	}

	docPath, err := filePathFromDocumentURI(uri)
	if err != nil {
		return "", "", false
	}
	candidates := includeSearchCandidates(filepath.Dir(docPath), cfg.roots, cfg.includeDirs)
	for _, base := range candidates {
		candidate := filepath.Join(base, filepath.FromSlash(includePath))
		displayURI, key, err := CanonicalizeDocumentURI(candidate)
		if err != nil {
			continue
		}
		if _, ok := docs[key]; ok {
			return displayURI, key, true
		}
	}
	return "", "", false
}

func includeSearchCandidates(docDir string, roots []string, includeDirs []string) []string {
	out := make([]string, 0, 1+len(includeDirs)*max(len(roots), 1))
	seen := make(map[string]struct{})
	addDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}

	addDir(docDir)
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

func buildSymbolIndexes(docs map[DocumentKey]*DocumentSummary) (map[SymbolID]Symbol, map[QualifiedName][]SymbolID, map[DocumentKey]map[string][]Symbol) {
	symbolsByID := make(map[SymbolID]Symbol)
	symbolsByQName := make(map[QualifiedName][]SymbolID)
	byDocument := make(map[DocumentKey]map[string][]Symbol)

	for _, key := range sortedDocumentKeys(docs) {
		doc := docs[key]
		if doc == nil {
			continue
		}
		names := make(map[string][]Symbol)
		for _, sym := range doc.Declarations {
			symbolsByID[sym.ID] = sym
			symbolsByQName[sym.QName] = append(symbolsByQName[sym.QName], sym.ID)
			names[sym.Name] = append(names[sym.Name], sym)
		}
		byDocument[key] = names
	}

	for _, ids := range symbolsByQName {
		slices.Sort(ids)
	}
	return symbolsByID, symbolsByQName, byDocument
}

func bindReferencesForSummary(summary *DocumentSummary, byDocument map[DocumentKey]map[string][]Symbol) {
	if summary == nil {
		return
	}

	aliases := make(map[string][]DocumentKey)
	for _, inc := range summary.Includes {
		if inc.ResolvedKey == "" || inc.Alias == "" {
			continue
		}
		aliases[inc.Alias] = append(aliases[inc.Alias], inc.ResolvedKey)
	}

	for i := range summary.References {
		ref := &summary.References[i]
		if ref.Tainted {
			ref.Binding = BindingResult{Status: BindingStatusTainted, Reason: "reference site is tainted by parser recovery"}
			continue
		}

		if ref.Qualifier == "" {
			ref.Binding = bindAgainstSymbols(byDocument[summary.Key][ref.Name], ref.ExpectedKinds)
			continue
		}

		targets := aliases[ref.Qualifier]
		switch len(targets) {
		case 0:
			ref.Binding = BindingResult{Status: BindingStatusUnresolved, Reason: "include alias is unresolved"}
		case 1:
			ref.Binding = bindAgainstSymbols(byDocument[targets[0]][ref.Name], ref.ExpectedKinds)
		default:
			ref.Binding = BindingResult{Status: BindingStatusAmbiguous, Reason: "include alias resolves to multiple targets"}
		}
	}
}

func bindAgainstSymbols(candidates []Symbol, expected []SymbolKind) BindingResult {
	if len(candidates) == 0 {
		return BindingResult{Status: BindingStatusUnresolved, Reason: "no matching declaration"}
	}

	filtered := make([]Symbol, 0, len(candidates))
	for _, sym := range candidates {
		if kindAllowed(sym.Kind, expected) {
			filtered = append(filtered, sym)
		}
	}
	switch len(filtered) {
	case 0:
		return BindingResult{Status: BindingStatusUnresolved, Reason: "declaration kind does not match expected context"}
	case 1:
		return BindingResult{Status: BindingStatusBound, Target: filtered[0].ID}
	default:
		return BindingResult{Status: BindingStatusAmbiguous, Reason: "multiple declarations match the same name"}
	}
}

func kindAllowed(kind SymbolKind, expected []SymbolKind) bool {
	if len(expected) == 0 {
		return true
	}
	return slices.Contains(expected, kind)
}

func unquoteMaybe(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if v, err := strconv.Unquote(raw); err == nil {
		return v
	}
	return strings.Trim(raw, `"'`)
}
