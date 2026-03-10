package main

import (
	"bytes"
	"testing"
)

func TestParseConfigAcceptsWorkspaceIndexWorkers(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	cfg, err := parseConfig([]string{"--workspace-index-workers", "3"}, &stderr)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.workspaceIndexWorkers != 3 {
		t.Fatalf("workspaceIndexWorkers=%d, want 3", cfg.workspaceIndexWorkers)
	}
}

func TestParseConfigAcceptsStdioCompatibilityFlag(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	_, err := parseConfig([]string{"--stdio"}, &stderr)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
}

func TestParseConfigRejectsNegativeWorkspaceIndexWorkers(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	_, err := parseConfig([]string{"--workspace-index-workers", "-1"}, &stderr)
	if err == nil || err.Error() != "--workspace-index-workers must be >= 0" {
		t.Fatalf("parseConfig error=%v, want negative-value rejection", err)
	}
}
