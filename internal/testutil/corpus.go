// Package testutil provides shared helpers for repository tests.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// CorpusFiles returns sorted .thrift files under testdata/corpus/<setName>.
func CorpusFiles(setName string) ([]string, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	setDir := filepath.Join(root, "testdata", "corpus", setName)
	entries, err := os.ReadDir(setDir)
	if err != nil {
		return nil, fmt.Errorf("read corpus set %q: %w", setName, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".thrift" {
			continue
		}
		out = append(out, filepath.Join(setDir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
