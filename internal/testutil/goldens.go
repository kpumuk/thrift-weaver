// Package testutil provides shared helpers for repository tests.
package testutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// GoldenCase is an input/expected fixture pair.
type GoldenCase struct {
	Name         string
	InputPath    string
	ExpectedPath string
}

// RepoRoot returns the repository root by walking up from this source file.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}

// MustRepoRoot returns the repository root or fails the test.
func MustRepoRoot(t testing.TB) string {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	return root
}

// FormatGoldenCases returns sorted formatter fixture pairs from testdata/format.
func FormatGoldenCases() ([]GoldenCase, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	inputDir := filepath.Join(root, "testdata", "format", "input")
	expectedDir := filepath.Join(root, "testdata", "format", "expected")

	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("read input dir: %w", err)
	}

	var cases []GoldenCase
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".thrift" {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}

		expectedPath := filepath.Join(expectedDir, name)
		if _, err := os.Stat(expectedPath); err != nil {
			return nil, fmt.Errorf("missing expected fixture for %s", name)
		}

		cases = append(cases, GoldenCase{
			Name:         strings.TrimSuffix(name, ".thrift"),
			InputPath:    filepath.Join(inputDir, name),
			ExpectedPath: expectedPath,
		})
	}

	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, nil
}

// ReadFile reads a fixture file or fails the test.
func ReadFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return b
}
