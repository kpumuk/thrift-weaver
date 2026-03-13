package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExtractLinuxSnippet(t *testing.T) {
	doc := `# Linux Policy

Suggested release-note snippet:

> Linux managed-install binaries target glibc >= 2.28.
> Alpine users should use thrift.server.path.

## Future`

	got, err := extractLinuxSnippet(doc)
	if err != nil {
		t.Fatalf("extractLinuxSnippet error: %v", err)
	}
	want := "> Linux managed-install binaries target glibc >= 2.28.\n> Alpine users should use thrift.server.path."
	if got != want {
		t.Fatalf("snippet=%q want %q", got, want)
	}
}

func TestRenderPerformanceSummary(t *testing.T) {
	src := []byte(`{
  "go_version": "go1.26.1",
  "goos": "linux",
  "goarch": "amd64",
  "cpus": 8,
  "parse_bench": [{"set":"typical","stats":{"p50_ms":12.34,"p95_ms":45.67}}],
  "format_bench": [{"set":"typical","stats":{"p50_ms":22.22,"p95_ms":88.88}}],
  "memory": {"heap_alloc_growth": 1024, "heap_inuse_growth": 2048, "unbounded_growth_hint": false}
}`)
	got, err := renderPerformanceSummary(src)
	if err != nil {
		t.Fatalf("renderPerformanceSummary error: %v", err)
	}
	if !strings.Contains(got, "Parse + diagnostics (typical): p50 12.34 ms, p95 45.67 ms") {
		t.Fatalf("missing parse summary: %s", got)
	}
	if !strings.Contains(got, "heap_alloc delta 1024 bytes") {
		t.Fatalf("missing memory summary: %s", got)
	}
}

func TestRenderPerformanceSummaryFallsBackWhenBenchmarksAreMissing(t *testing.T) {
	src := []byte(`{
  "go_version": "go1.26.1",
  "goos": "linux",
  "goarch": "amd64",
  "cpus": 8,
  "parse_bench": [],
  "format_bench": [{"set":"typical","stats":{"p50_ms":22.22,"p95_ms":88.88}}],
  "memory": {"heap_alloc_growth": 1024, "heap_inuse_growth": 2048, "unbounded_growth_hint": false}
}`)
	got, err := renderPerformanceSummary(src)
	if err != nil {
		t.Fatalf("renderPerformanceSummary error: %v", err)
	}
	if !strings.Contains(got, "Parse + diagnostics (typical): unavailable") {
		t.Fatalf("missing parse fallback: %s", got)
	}
	if !strings.Contains(got, "Full document format (typical): p50 22.22 ms, p95 88.88 ms") {
		t.Fatalf("missing format summary: %s", got)
	}
}

func TestFetchReleasePRBody(t *testing.T) {
	mergedAt := time.Now().UTC().Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/kpumuk/thrift-weaver/commits/abc123/pulls" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"number":1,"title":"fix: not the release","body":"ignore","merged_at":"` + mergedAt + `"},{"number":2,"title":"chore: release 0.1.2","body":"generated release body","merged_at":"` + mergedAt + `"}]`))
	}))
	defer server.Close()

	client := githubClient{
		baseURL: server.URL,
		token:   "test-token",
		client:  server.Client(),
	}
	got, err := client.fetchReleasePRBody(context.Background(), "kpumuk/thrift-weaver", "abc123")
	if err != nil {
		t.Fatalf("fetchReleasePRBody error: %v", err)
	}
	if got != "generated release body" {
		t.Fatalf("body=%q", got)
	}
}

func TestComposeReleaseNotes(t *testing.T) {
	perfJSON, err := os.ReadFile("testdata/perf.json")
	if err != nil {
		t.Fatalf("read perf fixture: %v", err)
	}
	linuxDoc := `Suggested release-note snippet:

> Linux managed-install binaries target glibc >= 2.28.
> Alpine users should use thrift.server.path.
`
	got, err := composeReleaseNotes("## Features\n\n- Added something.", linuxDoc, perfJSON)
	if err != nil {
		t.Fatalf("composeReleaseNotes error: %v", err)
	}
	if !strings.Contains(got, "## Linux compatibility") {
		t.Fatalf("missing linux section: %s", got)
	}
	if !strings.Contains(got, "## Features") {
		t.Fatalf("missing base notes: %s", got)
	}
}
