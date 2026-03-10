package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanWorkspaceIgnoresMetadataDirsAndRespectsLimits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "ignored.thrift"), []byte("struct Ignored {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile ignored: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "one.thrift"), []byte("struct One {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.thrift"), []byte("struct Two {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile two: %v", err)
	}

	files, err := scanWorkspace(context.Background(), []string{root}, nil, 10, 1<<20)
	if err != nil {
		t.Fatalf("scanWorkspace: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files)=%d, want 2", len(files))
	}
	for _, file := range files {
		if strings.Contains(file.Path, ".git") {
			t.Fatalf("scanned ignored path %q", file.Path)
		}
	}

	if _, err := scanWorkspace(context.Background(), []string{root}, nil, 1, 1<<20); err == nil || !strings.Contains(err.Error(), "maxFiles=1") {
		t.Fatalf("maxFiles error=%v, want limit failure", err)
	}

	if _, err := scanWorkspace(context.Background(), []string{root}, nil, 10, 8); err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("maxFileBytes error=%v, want size failure", err)
	}
}

func TestScanWorkspaceRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.thrift")
	if err := os.WriteFile(outside, []byte("struct Outside {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	link := filepath.Join(root, "escape.thrift")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := scanWorkspace(context.Background(), []string{root}, nil, 10, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("scanWorkspace error=%v, want symlink escape rejection", err)
	}
}
