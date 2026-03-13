// Package main enforces when the VS Code changelog must be updated for a change.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

type config struct {
	FileList string
	Base     string
	Head     string
}

var commitSHAPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "check-vscode-changelog: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}
	files, err := readFiles(cfg.FileList)
	if err != nil {
		return err
	}
	required, reasons, err := changelogRequired(cfg, files)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	return fmt.Errorf("editors/vscode/CHANGELOG.md must be updated when these files change: %s", strings.Join(reasons, ", "))
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.FileList, "file-list", "", "Path to newline-delimited changed files list")
	flag.StringVar(&cfg.Base, "base", "", "Base git ref for pull request comparison")
	flag.StringVar(&cfg.Head, "head", "", "Head git ref for pull request comparison")
	flag.Parse()
	if strings.TrimSpace(cfg.FileList) == "" {
		return config{}, errors.New("required flag: --file-list")
	}
	return cfg, nil
}

func readFiles(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file list: %w", err)
	}
	defer func() { _ = f.Close() }()
	var files []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		file := strings.TrimSpace(s.Text())
		if file != "" {
			files = append(files, file)
		}
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("scan file list: %w", err)
	}
	return files, nil
}

func changelogRequired(cfg config, files []string) (bool, []string, error) {
	hasChangelog := false
	var reasons []string
	for _, file := range files {
		if file == "editors/vscode/CHANGELOG.md" {
			hasChangelog = true
			continue
		}
		if requiresChangelogForFile(file) {
			reasons = append(reasons, file)
		}
	}
	if hasChangelog {
		return false, nil, nil
	}
	filtered, err := filterDependencyOnlyPackageJSONReason(cfg, reasons)
	if err != nil {
		return false, nil, err
	}
	return len(filtered) > 0, filtered, nil
}

func filterDependencyOnlyPackageJSONReason(cfg config, reasons []string) ([]string, error) {
	if !contains(reasons, "editors/vscode/package.json") {
		return reasons, nil
	}
	if strings.TrimSpace(cfg.Base) == "" || strings.TrimSpace(cfg.Head) == "" {
		return reasons, nil
	}
	requires, err := vscodePackageChangeRequiresChangelog(cfg.Base, cfg.Head)
	if err != nil {
		return nil, err
	}
	if requires {
		return reasons, nil
	}
	return without(reasons, "editors/vscode/package.json"), nil
}

func requiresChangelogForFile(path string) bool {
	switch {
	case strings.HasPrefix(path, "editors/vscode/scripts/"):
		return false
	case strings.HasPrefix(path, "editors/vscode/test/"):
		return false
	case strings.HasSuffix(path, ".test.ts"):
		return false
	case path == "editors/vscode/package-lock.json":
		return false
	case path == "editors/vscode/README.md":
		return false
	case strings.HasPrefix(path, "editors/vscode/"):
		return true
	case strings.HasPrefix(path, "cmd/thriftls/"):
		return true
	case strings.HasPrefix(path, "internal/lsp/"):
		return !strings.HasSuffix(path, "_test.go")
	case strings.HasPrefix(path, "internal/index/"):
		return !strings.HasSuffix(path, "_test.go")
	case strings.HasPrefix(path, "internal/lint/"):
		return !strings.HasSuffix(path, "_test.go")
	default:
		return false
	}
}

func vscodePackageChangeRequiresChangelog(base, head string) (bool, error) {
	if !commitSHAPattern.MatchString(base) {
		return false, fmt.Errorf("base ref must be a full commit SHA, got %q", base)
	}
	if !commitSHAPattern.MatchString(head) {
		return false, fmt.Errorf("head ref must be a full commit SHA, got %q", head)
	}
	before, err := gitShow(base, "editors/vscode/package.json")
	if err != nil {
		return false, fmt.Errorf("read editors/vscode/package.json at %s: %w", base, err)
	}
	after, err := gitShow(head, "editors/vscode/package.json")
	if err != nil {
		return false, fmt.Errorf("read editors/vscode/package.json at %s: %w", head, err)
	}
	return manifestChangeRequiresChangelog(before, after)
}

func gitShow(ref, path string) ([]byte, error) {
	// #nosec G204 -- ref is validated as a full commit SHA and path is a constant repo path.
	cmd := exec.CommandContext(context.Background(), "git", "show", ref+":"+path)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func manifestChangeRequiresChangelog(before, after []byte) (bool, error) {
	beforeManifest, err := manifestWithoutDependencies(before)
	if err != nil {
		return false, err
	}
	afterManifest, err := manifestWithoutDependencies(after)
	if err != nil {
		return false, err
	}
	return !reflect.DeepEqual(beforeManifest, afterManifest), nil
}

func manifestWithoutDependencies(src []byte) (map[string]any, error) {
	var manifest map[string]any
	if err := json.Unmarshal(src, &manifest); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	delete(manifest, "dependencies")
	delete(manifest, "devDependencies")
	return manifest, nil
}

func contains(items []string, needle string) bool {
	return slices.Contains(items, needle)
}

func without(items []string, needle string) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if item != needle {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
