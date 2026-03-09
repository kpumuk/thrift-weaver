package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChecksumLine(t *testing.T) {
	entry, err := parseChecksumLine("abc123  dist/file.tar.gz")
	if err != nil {
		t.Fatalf("parseChecksumLine: %v", err)
	}
	if entry.Sum != "abc123" || entry.Path != "dist/file.tar.gz" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestResolveChecksumPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolved, err := resolveChecksumPath("artifact.txt", []string{dir})
	if err != nil {
		t.Fatalf("resolveChecksumPath: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved=%q, want %q", resolved, path)
	}
}

func TestSha256File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	checksum, err := sha256File(path)
	if err != nil {
		t.Fatalf("sha256File: %v", err)
	}
	if !strings.EqualFold(checksum, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824") {
		t.Fatalf("checksum=%s", checksum)
	}
}
