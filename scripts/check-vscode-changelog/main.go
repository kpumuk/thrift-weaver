// Package main enforces when the VS Code changelog must be updated for a change.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type config struct {
	FileList string
}

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
	required, reasons := changelogRequired(files)
	if !required {
		return nil
	}
	return fmt.Errorf("editors/vscode/CHANGELOG.md must be updated when these files change: %s", strings.Join(reasons, ", "))
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.FileList, "file-list", "", "Path to newline-delimited changed files list")
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

func changelogRequired(files []string) (bool, []string) {
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
	return len(reasons) > 0 && !hasChangelog, reasons
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
