package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/lint"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const (
	exitOK       = 0
	exitIssues   = 1
	exitInternal = 3

	outputFormatText = "text"
	outputFormatJSON = "json"
)

type cliOptions struct {
	stdin          bool
	assumeFilename string
	format         string
	path           string
}

type diagnosticJSON struct {
	URI       string `json:"uri"`
	Source    string `json:"source"`
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
}

var defaultLintRunner = lint.NewDefaultRunner()

func run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	opts, usage, err := parseArgs(args)
	if err != nil {
		writef(stderr, "thriftlint: %v\n\n%s", err, usage)
		return exitInternal
	}

	src, uri, err := readInput(stdin, opts)
	if err != nil {
		writef(stderr, "thriftlint: %v\n", err)
		return exitInternal
	}

	tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: uri})
	if err != nil {
		writef(stderr, "thriftlint: parse failed: %v\n", err)
		return exitInternal
	}

	diags, err := collectDiagnostics(ctx, tree)
	if err != nil {
		writef(stderr, "thriftlint: lint failed: %v\n", err)
		return exitInternal
	}
	if len(diags) == 0 {
		return exitOK
	}

	if err := writeDiagnosticsOutput(opts.format, stdout, stderr, tree, diags); err != nil {
		writef(stderr, "thriftlint: %v\n", err)
		return exitInternal
	}

	return exitIssues
}

func parseArgs(args []string) (cliOptions, string, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("thriftlint", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.BoolVar(&opts.stdin, "stdin", false, "read input from stdin")
	fs.StringVar(&opts.assumeFilename, "assume-filename", "", "filename/URI used for parser context and diagnostics")
	fs.StringVar(&opts.format, "format", outputFormatText, "diagnostic output format: text|json")

	usage := cliUsage(fs)
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, usage, err
	}

	if !isSupportedOutputFormat(opts.format) {
		return cliOptions{}, usage, errors.New("--format must be one of: text, json")
	}

	rest := fs.Args()
	switch {
	case opts.stdin && len(rest) > 0:
		return cliOptions{}, usage, errors.New("positional file path is not allowed with --stdin")
	case !opts.stdin && len(rest) == 0:
		return cliOptions{}, usage, errors.New("exactly one input file path is required (or use --stdin)")
	case !opts.stdin && len(rest) != 1:
		return cliOptions{}, usage, errors.New("linting multiple files in one invocation is not supported")
	}
	if !opts.stdin {
		opts.path = rest[0]
	}
	return opts, usage, nil
}

func cliUsage(fs *flag.FlagSet) string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	b.WriteString("  thriftlint [flags] path/to/file.thrift\n")
	b.WriteString("  thriftlint --stdin [--assume-filename foo.thrift] [flags]\n\n")
	b.WriteString("Flags:\n")
	fs.VisitAll(func(f *flag.Flag) {
		writef(&b, "  --%s\t%s\n", f.Name, f.Usage)
	})
	return b.String()
}

func readInput(stdin io.Reader, opts cliOptions) ([]byte, string, error) {
	if opts.stdin {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return nil, "", fmt.Errorf("read stdin: %w", err)
		}
		uri := opts.assumeFilename
		if uri == "" {
			uri = "stdin.thrift"
		}
		return src, uri, nil
	}
	//nolint:gosec // CLI intentionally reads user-provided file paths.
	src, err := os.ReadFile(opts.path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", opts.path, err)
	}
	return src, opts.path, nil
}

func collectDiagnostics(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	combined := slices.Clone(tree.Diagnostics)
	lintDiags, err := defaultLintRunner.Run(ctx, tree)
	if err != nil {
		return nil, err
	}
	combined = append(combined, lintDiags...)
	lint.SortDiagnostics(combined)
	return combined, nil
}

func isSupportedOutputFormat(v string) bool {
	switch v {
	case outputFormatText, outputFormatJSON:
		return true
	default:
		return false
	}
}

func writeDiagnosticsOutput(format string, stdout, stderr io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic) error {
	switch format {
	case outputFormatText:
		writeDiagnostics(stderr, tree, diags)
		return nil
	case outputFormatJSON:
		return writeJSONDiagnostics(stdout, tree, diags)
	default:
		return fmt.Errorf("unsupported --format %q", format)
	}
}

func writeDiagnostics(w io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic) {
	if len(diags) == 0 {
		return
	}
	li := lineIndexOrBuild(tree)
	uri := ""
	if tree != nil {
		uri = tree.URI
	}
	for i, d := range diags {
		if i > 0 {
			writeln(w)
		}
		prefix := "thriftlint"
		if uri != "" {
			prefix = uri
		}
		writeDiagnosticHeader(w, prefix, li, d)
		writeDiagnosticSnippet(w, tree, li, d)
	}
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

func writeDiagnosticHeader(w io.Writer, prefix string, li *text.LineIndex, d syntax.Diagnostic) {
	loc := d.Span.String()
	if li != nil && d.Span.Start.IsValid() {
		if p, err := li.OffsetToPoint(d.Span.Start); err == nil {
			loc = fmt.Sprintf("%d:%d", p.Line+1, p.Column+1)
		}
	}
	writef(
		w,
		"%s:%s: %s: %s/%s: %s\n",
		prefix,
		loc,
		diagnosticSeverityLetter(d.Severity),
		d.Source,
		d.Code,
		d.Message,
	)
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

func writeJSONDiagnostics(w io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic) error {
	li := lineIndexOrBuild(tree)
	uri := ""
	if tree != nil {
		uri = tree.URI
	}
	payload := make([]diagnosticJSON, 0, len(diags))
	for _, d := range diags {
		start, end, err := diagnosticPoints(li, d.Span)
		if err != nil {
			return err
		}
		payload = append(payload, diagnosticJSON{
			URI:       uri,
			Source:    d.Source,
			Code:      string(d.Code),
			Severity:  diagnosticSeverityName(d.Severity),
			Message:   d.Message,
			StartLine: start.Line + 1,
			StartCol:  start.Column + 1,
			EndLine:   end.Line + 1,
			EndCol:    end.Column + 1,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func diagnosticPoints(li *text.LineIndex, sp text.Span) (text.Point, text.Point, error) {
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

func diagnosticSeverityLetter(s syntax.Severity) string {
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

func diagnosticSeverityName(s syntax.Severity) string {
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

func writef(w io.Writer, format string, args ...any) {
	//nolint:gosec // Terminal/debug output helper; format strings are internal callsite constants.
	_, _ = io.WriteString(w, fmt.Sprintf(format, args...))
}

func writeln(w io.Writer, args ...any) {
	//nolint:gosec // Terminal/debug output helper.
	_, _ = fmt.Fprintln(w, args...)
}

func writeString(w io.Writer, s string) {
	//nolint:gosec // Terminal/debug output helper.
	_, _ = io.WriteString(w, s)
}
