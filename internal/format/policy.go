package format

import (
	"bytes"
	"unicode/utf8"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const utf8BOM = "\xEF\xBB\xBF"

const (
	// DiagnosticFormatterMixedNewlines reports mixed LF/CRLF line endings in input.
	DiagnosticFormatterMixedNewlines syntax.DiagnosticCode = "FMT_MIXED_NEWLINES"
	// DiagnosticFormatterInvalidUTF8 reports formatter refusal for invalid UTF-8 bytes.
	DiagnosticFormatterInvalidUTF8 syntax.DiagnosticCode = "FMT_INVALID_UTF8"
)

// SourcePolicy captures input-byte policy decisions used by the formatter core.
type SourcePolicy struct {
	HasBOM         bool
	Newline        string // "\n" or "\r\n"
	MixedNewlines  bool
	ValidUTF8      bool
	DominantLF     int
	DominantCRLF   int
	OriginalSource []byte
}

func analyzeSourcePolicy(src []byte) (SourcePolicy, []syntax.Diagnostic) {
	body := src
	policy := SourcePolicy{
		Newline:        "\n",
		ValidUTF8:      utf8.Valid(src),
		OriginalSource: src,
	}
	if bytes.HasPrefix(src, []byte(utf8BOM)) {
		policy.HasBOM = true
		body = src[len(utf8BOM):]
	}

	lf, crlf := countNewlines(body)
	policy.DominantLF = lf
	policy.DominantCRLF = crlf
	switch {
	case crlf > lf:
		policy.Newline = "\r\n"
	case lf > 0:
		policy.Newline = "\n"
	case crlf > 0:
		policy.Newline = "\r\n"
	}

	var diags []syntax.Diagnostic
	if !policy.ValidUTF8 {
		diags = append(diags, syntax.Diagnostic{
			Code:        DiagnosticFormatterInvalidUTF8,
			Message:     "formatter refuses invalid UTF-8 input",
			Severity:    syntax.SeverityError,
			Span:        sourceSpan(src),
			Source:      "formatter",
			Recoverable: false,
		})
	}
	if lf > 0 && crlf > 0 {
		policy.MixedNewlines = true
		diags = append(diags, syntax.Diagnostic{
			Code:        DiagnosticFormatterMixedNewlines,
			Message:     "mixed newline styles detected; formatter will normalize to dominant style",
			Severity:    syntax.SeverityInfo,
			Span:        sourceSpan(src),
			Source:      "formatter",
			Recoverable: true,
		})
	}

	return policy, diags
}

func countNewlines(src []byte) (lf, crlf int) {
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				crlf++
				i++
			}
		case '\n':
			lf++
		}
	}
	return lf, crlf
}

func sourceSpan(src []byte) text.Span {
	return text.Span{Start: 0, End: text.ByteOffset(len(src))}
}
