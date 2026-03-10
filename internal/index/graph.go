package index

import "slices"

func buildIncludeGraph(docs map[DocumentKey]*DocumentSummary) IncludeGraph {
	forward := make(map[DocumentKey][]DocumentKey, len(docs))
	reverse := make(map[DocumentKey][]DocumentKey, len(docs))
	for _, key := range sortedDocumentKeys(docs) {
		doc := docs[key]
		if doc == nil {
			continue
		}
		seen := make(map[DocumentKey]struct{})
		for _, inc := range doc.Includes {
			if inc.ResolvedKey == "" {
				continue
			}
			if _, ok := docs[inc.ResolvedKey]; !ok {
				continue
			}
			if _, ok := seen[inc.ResolvedKey]; ok {
				continue
			}
			seen[inc.ResolvedKey] = struct{}{}
			forward[key] = append(forward[key], inc.ResolvedKey)
			reverse[inc.ResolvedKey] = append(reverse[inc.ResolvedKey], key)
		}
		slices.Sort(forward[key])
	}
	for _, key := range sortedDocumentKeys(docs) {
		slices.Sort(reverse[key])
	}

	return IncludeGraph{
		Forward:    forward,
		Reverse:    reverse,
		Components: stronglyConnectedComponents(sortedDocumentKeys(docs), forward),
	}
}

func stronglyConnectedComponents(keys []DocumentKey, forward map[DocumentKey][]DocumentKey) [][]DocumentKey {
	var (
		index      int
		stack      []DocumentKey
		onStack    = make(map[DocumentKey]bool)
		indexByKey = make(map[DocumentKey]int)
		lowLink    = make(map[DocumentKey]int)
		out        [][]DocumentKey
	)

	var visit func(DocumentKey)
	visit = func(v DocumentKey) {
		indexByKey[v] = index
		lowLink[v] = index
		index++

		stack = append(stack, v)
		onStack[v] = true

		for _, w := range forward[v] {
			if _, ok := indexByKey[w]; !ok {
				visit(w)
				if lowLink[w] < lowLink[v] {
					lowLink[v] = lowLink[w]
				}
				continue
			}
			if onStack[w] && indexByKey[w] < lowLink[v] {
				lowLink[v] = indexByKey[w]
			}
		}

		if lowLink[v] != indexByKey[v] {
			return
		}

		component := make([]DocumentKey, 0, 1)
		for {
			last := len(stack) - 1
			w := stack[last]
			stack = stack[:last]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		slices.Sort(component)
		out = append(out, component)
	}

	for _, key := range keys {
		if _, ok := indexByKey[key]; ok {
			continue
		}
		visit(key)
	}

	slices.SortFunc(out, func(a, b []DocumentKey) int {
		if len(a) == 0 || len(b) == 0 {
			return len(a) - len(b)
		}
		switch {
		case a[0] < b[0]:
			return -1
		case a[0] > b[0]:
			return 1
		default:
			return 0
		}
	})
	return out
}

func sortedDocumentKeys(docs map[DocumentKey]*DocumentSummary) []DocumentKey {
	keys := make([]DocumentKey, 0, len(docs))
	for key := range docs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
