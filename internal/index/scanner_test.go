package index

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

	result, err := scanWorkspace(context.Background(), []string{root}, nil, 10, 1<<20)
	if err != nil {
		t.Fatalf("scanWorkspace: %v", err)
	}
	files := result.files
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

func TestScanWorkspaceRespectsRecursiveGitIgnore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.thrift"), []byte("struct Main {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile main: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("vendor/\n"), 0o600); err != nil {
		t.Fatalf("WriteFile root .gitignore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o750); err != nil {
		t.Fatalf("MkdirAll vendor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "hidden.thrift"), []byte("struct Hidden {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile hidden: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", ".gitignore"), []byte("*.thrift\n!keep.thrift\n"), 0o600); err != nil {
		t.Fatalf("WriteFile nested .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "skip.thrift"), []byte("struct Skip {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile skip: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "keep.thrift"), []byte("struct Keep {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile keep: %v", err)
	}

	result, err := scanWorkspace(context.Background(), []string{root}, nil, 10, 1<<20)
	if err != nil {
		t.Fatalf("scanWorkspace: %v", err)
	}
	files := result.files

	normalizedRoot, err := normalizeRuntimePath(root)
	if err != nil {
		t.Fatalf("normalizeRuntimePath(%s): %v", root, err)
	}
	got := make([]string, 0, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(normalizedRoot, file.Path)
		if err != nil {
			t.Fatalf("filepath.Rel(%s, %s): %v", normalizedRoot, file.Path, err)
		}
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	want := []string{"main.thrift", "nested/keep.thrift"}
	if !slices.Equal(got, want) {
		t.Fatalf("scanned files=%v, want %v", got, want)
	}
	if result.gitIgnoreSkippedPaths != 2 {
		t.Fatalf("gitIgnoreSkippedPaths=%d, want 2", result.gitIgnoreSkippedPaths)
	}
}
