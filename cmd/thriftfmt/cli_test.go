package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidFlagCombos(t *testing.T) {
	t.Parallel()

	var out, err bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &err, []string{"--stdin", "--write"})
	if code != exitInternal {
		t.Fatalf("exit code = %d, want %d", code, exitInternal)
	}
	if !strings.Contains(err.String(), "--write and --stdin") {
		t.Fatalf("stderr missing conflict message: %q", err.String())
	}
}

func TestRunCheckExitCodeWhenChangesNeeded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "x.thrift")
	src := "struct Foo{1:required i32 id;}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out, errb bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &errb, []string{"--check", path})
	if code != exitCheck {
		t.Fatalf("exit code = %d, want %d", code, exitCheck)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout in --check: %q", out.String())
	}
}

func TestRunCheckExitCodeWhenNoChangesNeededForRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "x.thrift")
	src := "const i32 X = 1;\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out, errb bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &errb, []string{"--check", "--range", "10:11", path})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
}

func TestRunReturnsUnsafeExitCodeAndDiagnostics(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	code := run(
		context.Background(),
		strings.NewReader("const string X = 'unterminated\n"),
		&out,
		&errb,
		[]string{"--stdin", "--assume-filename", "stdin.thrift"},
	)
	if code != exitUnsafe {
		t.Fatalf("exit code = %d, want %d", code, exitUnsafe)
	}
	if !strings.Contains(errb.String(), "unterminated string literal") {
		t.Fatalf("stderr missing diagnostic: %q", errb.String())
	}
}

func TestRunWriteUpdatesFileInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "x.thrift")
	src := "service S{async void ping(1:i32 id);}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out, errb bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &errb, []string{"--write", path})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, exitOK, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout for --write: %q", out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "service S {\n  async void ping(1: i32 id);\n}\n" {
		t.Fatalf("formatted file mismatch: %q", got)
	}
}

func TestRunRangeFormatsSelectedAncestorAndPrintsToStdout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "x.thrift")
	src := "struct Foo{1:required i32 id;2: optional string name(ann='x');}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	start := strings.Index(src, "ann")
	if start < 0 {
		t.Fatal("failed to find ann")
	}
	rangeArg := fmt.Sprintf("%d:%d", start, start+3)

	var out, errb bytes.Buffer
	code := run(context.Background(), strings.NewReader(""), &out, &errb, []string{"--range", rangeArg, path})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, exitOK, errb.String())
	}
	if !strings.Contains(out.String(), "ann = 'x'") {
		t.Fatalf("stdout missing ranged formatting change: %q", out.String())
	}
}

func TestRunDebugFlagsProduceOutput(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	code := run(
		context.Background(),
		strings.NewReader("const i32 X=1\n"),
		&out,
		&errb,
		[]string{"--stdin", "--debug-tokens", "--debug-cst"},
	)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, exitOK, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "TOKENS") {
		t.Fatalf("debug tokens output missing: %q", got)
	}
	if !strings.Contains(got, "CST root=") {
		t.Fatalf("debug cst output missing: %q", got)
	}
}

func TestParseRangeFlag(t *testing.T) {
	t.Parallel()

	got, err := parseRangeFlag("12:34")
	if err != nil {
		t.Fatalf("parseRangeFlag: %v", err)
	}
	if got.Start != 12 || got.End != 34 {
		t.Fatalf("range = %s, want [12,34)", got)
	}

	if _, err := parseRangeFlag("bad"); err == nil {
		t.Fatal("expected error")
	}
}
