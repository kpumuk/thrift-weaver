package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidArgs(t *testing.T) {
	t.Parallel()

	var out, err bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &err, []string{"--stdin", "file.thrift"})
	if code != exitInternal {
		t.Fatalf("exit code = %d, want %d", code, exitInternal)
	}
	if !strings.Contains(err.String(), "positional file path is not allowed with --stdin") {
		t.Fatalf("stderr missing validation message: %q", err.String())
	}
}

func TestRunNoDiagnosticsExitOK(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "valid.thrift")
	if err := os.WriteFile(path, []byte("struct S {\n  1: string name,\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out, errb bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &errb, []string{path})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, exitOK, errb.String())
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("expected no output for clean file; stdout=%q stderr=%q", out.String(), errb.String())
	}
}

func TestRunIssuesExitAndTextDiagnostics(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	src := "struct S {\n  string name xsd_optional,\n}\n"
	code := run(context.Background(), strings.NewReader(src), &out, &errb, []string{"--stdin", "--assume-filename", "stdin.thrift"})
	if code != exitIssues {
		t.Fatalf("exit code = %d, want %d", code, exitIssues)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout for text diagnostics: %q", out.String())
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "LINT_FIELD_ID_REQUIRED") {
		t.Fatalf("missing field-id diagnostic in stderr: %q", stderr)
	}
	if !strings.Contains(stderr, "LINT_DEPRECATED_FIELD_XSD_OPTIONAL") {
		t.Fatalf("missing deprecated-field diagnostic in stderr: %q", stderr)
	}
}

func TestRunJSONDiagnostics(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	src := "struct S {\n  string name,\n}\n"
	code := run(context.Background(), strings.NewReader(src), &out, &errb, []string{"--stdin", "--format", "json"})
	if code != exitIssues {
		t.Fatalf("exit code = %d, want %d", code, exitIssues)
	}
	if errb.Len() != 0 {
		t.Fatalf("expected empty stderr for json mode, got %q", errb.String())
	}

	var payload []diagnosticJSON
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v; payload=%q", err, out.String())
	}
	if len(payload) == 0 {
		t.Fatalf("expected diagnostics in json payload: %q", out.String())
	}
	if payload[0].Code == "" || payload[0].Message == "" {
		t.Fatalf("unexpected diagnostic payload: %+v", payload[0])
	}
}
