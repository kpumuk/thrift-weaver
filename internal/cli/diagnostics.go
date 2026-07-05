// Package cli contains shared helpers for command-line frontends.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// DiagnosticMessageFunc maps a diagnostic to its displayed message and optional detail lines.
type DiagnosticMessageFunc func(syntax.Diagnostic) (string, []string)

// WriteDiagnostics writes human-readable diagnostics with source snippets and carets.
func WriteDiagnostics(w io.Writer, toolName string, tree *syntax.Tree, diags []syntax.Diagnostic, messageFor DiagnosticMessageFunc) {
	if len(diags) == 0 {
		return
	}
	li := lineIndexOrBuild(tree)
	prefix := diagnosticPrefix(toolName, tree)
	for i, d := range diags {
		if i > 0 {
			writeln(w)
		}
		writeDiagnosticHeader(w, prefix, li, d, messageFor)
		writeDiagnosticSnippet(w, tree, li, d)
	}
}

// DiagnosticPoints converts a diagnostic byte span to source points after clamping to the source.
func DiagnosticPoints(li *text.LineIndex, sp text.Span) (text.Point, text.Point, error) {
	if li == nil {
		return text.Point{}, text.Point{}, errors.New("nil line index")
	}
	clamped := clampSpanToSource(sp, li.SourceLen())
	start, err := li.OffsetToPoint(clamped.Start)
	if err != nil {
		return text.Point{}, text.Point{}, err
	}
	end, err := li.OffsetToPoint(clamped.End)
	if err != nil {
		return text.Point{}, text.Point{}, err
	}
	return start, end, nil
}

// SeverityName returns the stable JSON name for a syntax diagnostic severity.
func SeverityName(s syntax.Severity) string {
	switch s {
	case syntax.SeverityError:
		return "error"
	case syntax.SeverityWarning:
		return "warning"
	case syntax.SeverityInfo:
		return "info"
	default:
		return "error"
	}
}

// LineIndexOrBuild returns the tree line index, building one from source if needed.
func LineIndexOrBuild(tree *syntax.Tree) *text.LineIndex {
	return lineIndexOrBuild(tree)
}

// DefaultDiagnosticMessage returns the raw diagnostic message.
func DefaultDiagnosticMessage(d syntax.Diagnostic) (string, []string) {
	return d.Message, nil
}

func diagnosticPrefix(toolName string, tree *syntax.Tree) string {
	if tree != nil && tree.URI != "" {
		return tree.URI
	}
	return toolName
}

func writeDiagnosticHeader(w io.Writer, prefix string, li *text.LineIndex, d syntax.Diagnostic, messageFor DiagnosticMessageFunc) {
	loc := d.Span.String()
	if li != nil && d.Span.Start.IsValid() {
		if p, err := li.OffsetToPoint(d.Span.Start); err == nil {
			loc = fmt.Sprintf("%d:%d", p.Line+1, p.Column+1)
		}
	}
	if messageFor == nil {
		messageFor = DefaultDiagnosticMessage
	}
	msg, detailLines := messageFor(d)
	writef(w, "%s:%s: %s: %s/%s: %s\n", prefix, loc, severityLetter(d.Severity), d.Source, d.Code, msg)
	for _, line := range detailLines {
		writef(w, "  %s\n", line)
	}
}

func writeDiagnosticSnippet(w io.Writer, tree *syntax.Tree, li *text.LineIndex, d syntax.Diagnostic) {
	if tree == nil || li == nil || !d.Span.Start.IsValid() {
		return
	}

	startPoint, err := li.OffsetToPoint(d.Span.Start)
	if err != nil {
		return
	}
	lineStart, lineText, ok := sourceLineAt(tree.Source, d.Span.Start)
	if !ok {
		return
	}

	startCol := min(max(int(d.Span.Start-lineStart), 0), len(lineText))
	caretWidth := diagnosticCaretWidth(li, d, startPoint.Line, len(lineText), lineStart)
	caretPrefix := caretPrefixForLine(lineText, startCol)

	writeln(w, string(lineText))
	writeString(w, caretPrefix)
	writeString(w, strings.Repeat("^", caretWidth))
	writeln(w)
}

func diagnosticCaretWidth(li *text.LineIndex, d syntax.Diagnostic, startLine int, lineLen int, lineStart text.ByteOffset) int {
	if lineLen == 0 {
		return 1
	}
	if !d.Span.End.IsValid() || d.Span.End <= d.Span.Start {
		return 1
	}

	end := min(d.Span.End, li.SourceLen())
	endPoint, err := li.OffsetToPoint(end)
	if err != nil {
		return 1
	}

	startCol := min(max(int(d.Span.Start-lineStart), 0), lineLen)
	if endPoint.Line != startLine {
		if startCol >= lineLen {
			return 1
		}
		return lineLen - startCol
	}

	endCol := endPoint.Column
	if endCol < startCol {
		return 1
	}
	if endCol > lineLen {
		endCol = lineLen
	}
	if endCol == startCol {
		return 1
	}
	return endCol - startCol
}

func sourceLineAt(src []byte, off text.ByteOffset) (text.ByteOffset, []byte, bool) {
	if !off.IsValid() {
		return 0, nil, false
	}
	i := int(off)
	if i < 0 || i > len(src) {
		return 0, nil, false
	}

	start := i
	for start > 0 && src[start-1] != '\n' {
		start--
	}

	end := i
	for end < len(src) && src[end] != '\n' {
		end++
	}
	if end > start && src[end-1] == '\r' {
		end--
	}

	return text.ByteOffset(start), src[start:end], true
}

func caretPrefixForLine(line []byte, col int) string {
	if col <= 0 {
		return ""
	}
	if col > len(line) {
		col = len(line)
	}

	var b strings.Builder
	b.Grow(col)
	for _, ch := range line[:col] {
		if ch == '\t' {
			b.WriteByte('\t')
			continue
		}
		b.WriteByte(' ')
	}
	return b.String()
}

func lineIndexOrBuild(tree *syntax.Tree) *text.LineIndex {
	if tree == nil {
		return nil
	}
	if tree.LineIndex != nil {
		return tree.LineIndex
	}
	return text.NewLineIndex(tree.Source)
}

func clampSpanToSource(sp text.Span, srcLen text.ByteOffset) text.Span {
	if !sp.Start.IsValid() {
		sp.Start = 0
	}
	if !sp.End.IsValid() {
		sp.End = sp.Start
	}
	if sp.Start > srcLen {
		sp.Start = srcLen
	}
	if sp.End > srcLen {
		sp.End = srcLen
	}
	if sp.End < sp.Start {
		sp.End = sp.Start
	}
	return sp
}

func severityLetter(s syntax.Severity) string {
	switch s {
	case syntax.SeverityError:
		return "E"
	case syntax.SeverityWarning:
		return "W"
	case syntax.SeverityInfo:
		return "I"
	default:
		return "E"
	}
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = io.WriteString(w, fmt.Sprintf(format, args...))
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func writeString(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}
