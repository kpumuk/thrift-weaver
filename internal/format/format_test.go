package format

import (
	"bytes"
	"context"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

func TestNormalizeOptionsDefaultsAndValidation(t *testing.T) {
	t.Parallel()

	got, err := normalizeOptions(Options{})
	if err != nil {
		t.Fatalf("normalizeOptions default: %v", err)
	}
	if got.LineWidth != defaultLineWidth {
		t.Fatalf("LineWidth = %d, want %d", got.LineWidth, defaultLineWidth)
	}
	if got.Indent != defaultIndent {
		t.Fatalf("Indent = %q, want %q", got.Indent, defaultIndent)
	}
	if got.MaxBlankLines != defaultMaxBlankLines {
		t.Fatalf("MaxBlankLines = %d, want %d", got.MaxBlankLines, defaultMaxBlankLines)
	}

	if _, err := normalizeOptions(Options{LineWidth: -1}); err == nil {
		t.Fatal("expected error for negative LineWidth")
	}
	if _, err := normalizeOptions(Options{MaxBlankLines: -1}); err == nil {
		t.Fatal("expected error for negative MaxBlankLines")
	}
}

func TestDocumentPreservesBOMAndReportsMixedNewlines(t *testing.T) {
	t.Parallel()

	src := []byte("\xEF\xBB\xBFconst i32 x = 1\r\nconst i32 y = 2\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "test.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	res, err := Document(context.Background(), tree, Options{})
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if len(res.Output) == 0 {
		t.Fatal("expected formatted output")
	}
	if !bytes.HasPrefix(res.Output, []byte("\xEF\xBB\xBF")) {
		t.Fatalf("expected BOM preserved, got %q", res.Output)
	}

	var sawMixed bool
	for _, d := range res.Diagnostics {
		if d.Code == DiagnosticFormatterMixedNewlines {
			sawMixed = true
			break
		}
	}
	if !sawMixed {
		t.Fatal("expected mixed newline formatter diagnostic")
	}
}

func TestDocumentRefusesInvalidUTF8(t *testing.T) {
	t.Parallel()

	tree := &syntax.Tree{Source: []byte{0xff}}
	res, err := Document(context.Background(), tree, Options{})
	if err == nil {
		t.Fatal("expected ErrUnsafeToFormat")
	}
	if !IsErrUnsafeToFormat(err) {
		t.Fatalf("unexpected error type: %T %v", err, err)
	}

	var unsafe *ErrUnsafeToFormat
	if !AsUnsafeToFormat(err, &unsafe) {
		t.Fatal("AsUnsafeToFormat = false")
	}
	if unsafe.Reason != UnsafeReasonInvalidUTF8 {
		t.Fatalf("unsafe reason = %q, want %q", unsafe.Reason, UnsafeReasonInvalidUTF8)
	}

	var sawInvalidUTF8 bool
	for _, d := range res.Diagnostics {
		if d.Code == DiagnosticFormatterInvalidUTF8 {
			sawInvalidUTF8 = true
			break
		}
	}
	if !sawInvalidUTF8 {
		t.Fatal("expected invalid UTF-8 formatter diagnostic")
	}
}

func TestSourceRefusesUnsafeSyntaxAndReturnsDiagnostics(t *testing.T) {
	t.Parallel()

	res, err := Source(context.Background(), []byte("const string X = 'unterminated\n"), "test.thrift", Options{})
	if err == nil {
		t.Fatal("expected unsafe formatting refusal")
	}
	if !IsErrUnsafeToFormat(err) {
		t.Fatalf("expected ErrUnsafeToFormat, got %T %v", err, err)
	}

	var unsafe *ErrUnsafeToFormat
	if !AsUnsafeToFormat(err, &unsafe) {
		t.Fatal("AsUnsafeToFormat = false")
	}
	if unsafe.Reason != UnsafeReasonSyntaxErrors {
		t.Fatalf("unsafe reason = %q, want %q", unsafe.Reason, UnsafeReasonSyntaxErrors)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected parse diagnostics in result")
	}
}

func TestSourceAllowsRecoverableParserDiagnostics(t *testing.T) {
	t.Parallel()

	res, err := Source(context.Background(), []byte("struct X {"), "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source should allow recoverable parser diagnostics, got error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected parser diagnostics for incomplete input")
	}
}

func TestRangeReturnsNoEditsForSafeInputTrackA(t *testing.T) {
	t.Parallel()

	src := []byte("const i32 X = 1;\n")
	tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "test.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	start := bytes.Index(src, []byte("X"))
	r := text.Span{Start: text.ByteOffset(start), End: text.ByteOffset(start + 1)}

	res, err := Range(context.Background(), tree, r, Options{})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(res.Edits) != 0 {
		t.Fatalf("expected no edits for already-formatted declaration range, got %d", len(res.Edits))
	}
}
