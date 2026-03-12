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

type resolvedIncludeTarget struct {
	uri string
	key DocumentKey
}

type includeLookupResult struct {
	target resolvedIncludeTarget
	ok     bool
}

type includeResolver struct {
	cfg                 resolverConfig
	docs                map[DocumentKey]*DocumentSummary
	docDirsByURI        map[string]string
	docTargetsByPath    map[string]resolvedIncludeTarget
	candidatesByDocDir  map[string][]string
	canonicalizedLookup map[string]includeLookupResult
}

func newIncludeResolver(cfg resolverConfig, docs map[DocumentKey]*DocumentSummary) *includeResolver {
	resolver := &includeResolver{
		cfg:                 cfg,
		docs:                docs,
		docDirsByURI:        make(map[string]string, len(docs)),
		docTargetsByPath:    make(map[string]resolvedIncludeTarget, len(docs)),
		candidatesByDocDir:  make(map[string][]string),
		canonicalizedLookup: make(map[string]includeLookupResult),
	}
	for key, doc := range docs {
		if doc == nil {
			continue
		}
		path, err := filePathFromDocumentURI(doc.URI)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		resolver.docDirsByURI[doc.URI] = filepath.Dir(path)
		resolver.docTargetsByPath[path] = resolvedIncludeTarget{uri: doc.URI, key: key}
	}
	return resolver
}

func resolveIncludesForSummary(summary *DocumentSummary, resolver *includeResolver) {
	if summary == nil {
		return
	}

	resolvedByKey := make(map[DocumentKey]string, len(summary.Includes))
	for i := range summary.Includes {
		edge := &summary.Includes[i]

		resolvedURI, resolvedKey, ok := resolver.resolveIncludePath(summary.URI, edge.RawPath)
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
}

func (r *includeResolver) resolveIncludePath(uri string, rawPath string) (string, DocumentKey, bool) {
	if r == nil {
		return "", "", false
	}
	includePath := unquoteMaybe(rawPath)
	if strings.TrimSpace(includePath) == "" {
		return "", "", false
	}

	docDir, ok := r.documentDir(uri)
	if !ok {
		return "", "", false
	}
	for _, base := range r.includeSearchCandidates(docDir) {
		candidate := filepath.Join(base, filepath.FromSlash(includePath))
		if target, ok := r.resolveCandidatePath(candidate); ok {
			return target.uri, target.key, true
		}
	}
	return "", "", false
}

func (r *includeResolver) documentDir(uri string) (string, bool) {
	if r == nil {
		return "", false
	}
	if docDir, ok := r.docDirsByURI[uri]; ok {
		return docDir, true
	}
	path, err := filePathFromDocumentURI(uri)
	if err != nil {
		return "", false
	}
	docDir := filepath.Dir(filepath.Clean(path))
	r.docDirsByURI[uri] = docDir
	return docDir, true
}

func (r *includeResolver) includeSearchCandidates(docDir string) []string {
	if r == nil {
		return nil
	}
	docDir = filepath.Clean(docDir)
	if candidates, ok := r.candidatesByDocDir[docDir]; ok {
		return candidates
	}
	candidates := includeSearchCandidates(docDir, r.cfg.roots, r.cfg.includeDirs)
	r.candidatesByDocDir[docDir] = candidates
	return candidates
}

func (r *includeResolver) resolveCandidatePath(candidate string) (resolvedIncludeTarget, bool) {
	if r == nil {
		return resolvedIncludeTarget{}, false
	}
	candidate = filepath.Clean(candidate)
	if target, ok := r.docTargetsByPath[candidate]; ok {
		return target, true
	}
	if result, ok := r.canonicalizedLookup[candidate]; ok {
		return result.target, result.ok
	}

	_, key, err := CanonicalizeDocumentURI(candidate)
	if err != nil {
		r.canonicalizedLookup[candidate] = includeLookupResult{}
		return resolvedIncludeTarget{}, false
	}
	doc, ok := r.docs[key]
	if !ok || doc == nil {
		r.canonicalizedLookup[candidate] = includeLookupResult{}
		return resolvedIncludeTarget{}, false
	}

	target := resolvedIncludeTarget{uri: doc.URI}
	if doc.Key != "" {
		target.key = doc.Key
	} else {
		target.key = key
	}
	r.docTargetsByPath[candidate] = target
	r.canonicalizedLookup[candidate] = includeLookupResult{target: target, ok: true}
	return target, true
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
