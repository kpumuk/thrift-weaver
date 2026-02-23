package treesitterthrift

import (
	"embed"
)

//go:embed queries/*.scm
var queryFS embed.FS

// QueryNames returns the built-in tree-sitter query file basenames supported by the grammar package.
func QueryNames() []string {
	return []string{"highlights", "folds", "symbols"}
}

// QuerySource returns the contents of a built-in tree-sitter query by basename (without .scm).
func QuerySource(name string) (string, error) {
	b, err := queryFS.ReadFile("queries/" + name + ".scm")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
