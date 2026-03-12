package index

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalizeDocumentURICleansAndPercentEncodes(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(dir, "demo.thrift")
	if err := os.WriteFile(target, []byte("struct Demo {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	raw := dir + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(dir) + string(filepath.Separator) + "demo.thrift"
	displayURI, key, err := CanonicalizeDocumentURI(raw)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI: %v", err)
	}

	wantDisplay, _, err := CanonicalizeDocumentURI(target)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(target): %v", err)
	}
	if displayURI != wantDisplay {
		t.Fatalf("displayURI=%q, want %q", displayURI, wantDisplay)
	}
	if !strings.Contains(displayURI, "dir%20with%20space") {
		t.Fatalf("displayURI=%q, want percent-encoded space", displayURI)
	}
	wantKey := documentKeyForDisplayURIWithCase(wantDisplay, filesystemCaseInsensitive(runtime.GOOS))
	if key != wantKey {
		t.Fatalf("key=%q, want %q", key, wantKey)
	}
}

func TestCanonicalizeDocumentURIResolvesSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "real.thrift")
	if err := os.WriteFile(target, []byte("struct Real {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "link.thrift")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	displayURI, _, err := CanonicalizeDocumentURI(link)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI: %v", err)
	}
	wantDisplay, _, err := CanonicalizeDocumentURI(target)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(target): %v", err)
	}
	if displayURI != wantDisplay {
		t.Fatalf("displayURI=%q, want %q", displayURI, wantDisplay)
	}
}

func TestCanonicalizeDocumentURIWindowsDriveHandlingAndCaseFold(t *testing.T) {
	t.Parallel()

	displayURI, key, err := canonicalizeDocumentURIWithOptions(
		"file:///c:/Users/Test/Thrift%20Files/demo.thrift",
		canonicalizeOptions{goos: "windows", cwd: `C:\workspace`, caseInsensitive: true},
	)
	if err != nil {
		t.Fatalf("canonicalizeDocumentURIWithOptions: %v", err)
	}

	wantDisplay := "file:///C:/Users/Test/Thrift%20Files/demo.thrift"
	if displayURI != wantDisplay {
		t.Fatalf("displayURI=%q, want %q", displayURI, wantDisplay)
	}
	wantKey := documentKeyForDisplayURIWithCase(wantDisplay, true)
	if key != wantKey {
		t.Fatalf("key=%q, want %q", key, wantKey)
	}
}

func TestCanonicalizeDocumentURIFromPercentEncodedFileURI(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(dir, "demo.thrift")
	if err := os.WriteFile(target, []byte("struct Demo {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rawURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(target)}).String()
	displayURI, _, err := CanonicalizeDocumentURI(rawURI)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI: %v", err)
	}
	wantDisplay, _, err := CanonicalizeDocumentURI(target)
	if err != nil {
		t.Fatalf("CanonicalizeDocumentURI(target): %v", err)
	}
	if displayURI != wantDisplay {
		t.Fatalf("displayURI=%q, want %q", displayURI, wantDisplay)
	}
}
