// Package main verifies sha256 checksums for release artifacts.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	checksums string
	roots     stringList
}

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

type checksumEntry struct {
	Sum  string
	Path string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "verify-checksums: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.checksums, "checksums", "", "path to checksums.txt")
	flag.Var(&cfg.roots, "root", "search root for files referenced by checksums.txt; repeatable")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if cfg.checksums == "" {
		return errors.New("--checksums is required")
	}
	entries, err := loadChecksumEntries(cfg.checksums)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("checksums file is empty")
	}

	roots := cfg.roots
	if len(roots) == 0 {
		roots = append(roots, filepath.Dir(cfg.checksums))
	}

	for _, entry := range entries {
		path, err := resolveChecksumPath(entry.Path, roots)
		if err != nil {
			return err
		}
		actual, err := sha256File(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, entry.Sum) {
			return fmt.Errorf("checksum mismatch for %s: got %s want %s", entry.Path, actual, entry.Sum)
		}
	}

	fmt.Printf("verified %d checksums\n", len(entries))
	return nil
}

func loadChecksumEntries(path string) ([]checksumEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	var entries []checksumEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, err := parseChecksumLine(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func parseChecksumLine(line string) (checksumEntry, error) {
	separator := "  "
	if sum, path, ok := strings.Cut(line, separator); ok {
		return checksumEntry{Sum: strings.TrimSpace(sum), Path: strings.TrimSpace(path)}, nil
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return checksumEntry{}, fmt.Errorf("invalid checksum line %q", line)
	}
	return checksumEntry{Sum: fields[0], Path: fields[len(fields)-1]}, nil
}

func resolveChecksumPath(name string, roots []string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	for _, root := range roots {
		candidate := filepath.Join(root, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("artifact %q not found under roots %v", name, roots)
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
