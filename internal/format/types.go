// Package format provides formatter core APIs and primitives for Thrift source formatting.
package format

import (
	"errors"
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const (
	defaultLineWidth     = 100
	defaultIndent        = "  "
	defaultMaxBlankLines = 2
)

// Options configure formatter behavior.
type Options struct {
	LineWidth           int
	Indent              string
	MaxBlankLines       int
	PreserveCommentCols bool // v2, experimental
}

// Result is the full-document formatting result.
type Result struct {
	Output      []byte
	Changed     bool
	Diagnostics []syntax.Diagnostic
}

// RangeResult is the range-formatting result.
type RangeResult struct {
	Edits       []text.ByteEdit
	Diagnostics []syntax.Diagnostic
}

// UnsafeReason identifies why a request was refused as unsafe.
type UnsafeReason string

const (
	// UnsafeReasonInvalidUTF8 indicates invalid UTF-8 bytes in the source input.
	UnsafeReasonInvalidUTF8 UnsafeReason = "invalid_utf8"
	// UnsafeReasonSyntaxErrors indicates fail-closed refusal due to parser/lexer error diagnostics.
	UnsafeReasonSyntaxErrors UnsafeReason = "syntax_errors"
)

// ErrUnsafeToFormat is returned when formatting is refused due to unsafe input state.
type ErrUnsafeToFormat struct {
	Reason  UnsafeReason
	Message string
}

func (e *ErrUnsafeToFormat) Error() string {
	if e == nil {
		return "unsafe to format"
	}
	if e.Message == "" {
		return fmt.Sprintf("unsafe to format (%s)", e.Reason)
	}
	return fmt.Sprintf("unsafe to format (%s): %s", e.Reason, e.Message)
}

// IsErrUnsafeToFormat reports whether err is a formatter safety refusal.
func IsErrUnsafeToFormat(err error) bool {
	var target *ErrUnsafeToFormat
	return AsUnsafeToFormat(err, &target)
}

// AsUnsafeToFormat reports whether err contains an ErrUnsafeToFormat.
func AsUnsafeToFormat(err error, target **ErrUnsafeToFormat) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.LineWidth < 0 {
		return Options{}, fmt.Errorf("invalid LineWidth %d", opts.LineWidth)
	}
	if opts.MaxBlankLines < 0 {
		return Options{}, fmt.Errorf("invalid MaxBlankLines %d", opts.MaxBlankLines)
	}
	if opts.LineWidth == 0 {
		opts.LineWidth = defaultLineWidth
	}
	if opts.Indent == "" {
		opts.Indent = defaultIndent
	}
	if opts.MaxBlankLines == 0 {
		opts.MaxBlankLines = defaultMaxBlankLines
	}
	return opts, nil
}
