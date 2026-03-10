package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/index"
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

	crossFileOff        = "off"
	crossFileTransitive = "transitive"
	crossFileWorkspace  = "workspace"
)

type cliOptions struct {
	stdin          bool
	assumeFilename string
	format         string
	path           string
	crossFile      string
	workspaceRoots []string
	includeDirs    []string
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
	defer tree.Close()

	diags, err := collectDiagnosticsWithWorkspace(ctx, tree, src, uri, opts)
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
	fs.StringVar(&opts.crossFile, "cross-file", "", "cross-file analysis mode: off|transitive|workspace")
	fs.Var((*multiStringFlag)(&opts.workspaceRoots), "workspace-root", "workspace root used for cross-file analysis (repeatable)")
	fs.Var((*multiStringFlag)(&opts.includeDirs), "include-dir", "include directory used for cross-file analysis (repeatable)")

	usage := cliUsage(fs)
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, usage, err
	}

	if !isSupportedOutputFormat(opts.format) {
		return cliOptions{}, usage, errors.New("--format must be one of: text, json")
	}
	if opts.crossFile == "" {
		opts.crossFile = defaultCrossFileMode(opts.stdin)
	}
	if !isSupportedCrossFileMode(opts.crossFile) {
		return cliOptions{}, usage, errors.New("--cross-file must be one of: off, transitive, workspace")
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
	if opts.stdin && opts.crossFile != crossFileOff && opts.assumeFilename == "" {
		return cliOptions{}, usage, errors.New("--assume-filename is required when --stdin uses cross-file analysis")
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

func collectDiagnosticsWithWorkspace(ctx context.Context, tree *syntax.Tree, src []byte, uri string, opts cliOptions) ([]syntax.Diagnostic, error) {
	combined, err := collectDiagnostics(ctx, tree)
	if err != nil {
		return nil, err
	}
	if opts.crossFile == crossFileOff {
		return combined, nil
	}

	workspaceDiags, err := collectWorkspaceDiagnostics(ctx, src, uri, opts)
	if err != nil {
		return nil, err
	}
	combined = append(combined, workspaceDiags...)
	lint.SortDiagnostics(combined)
	return combined, nil
}

func collectWorkspaceDiagnostics(ctx context.Context, src []byte, uri string, opts cliOptions) ([]syntax.Diagnostic, error) {
	view, err := workspaceViewForInput(ctx, src, uri, opts)
	if err != nil || view == nil {
		return nil, err
	}
	return defaultLintRunner.RunWithWorkspace(ctx, view)
}

func workspaceViewForInput(ctx context.Context, src []byte, uri string, opts cliOptions) (*index.DocumentView, error) {
	roots, err := workspaceRootsForInput(opts, uri)
	if err != nil {
		return nil, err
	}

	manager := index.NewManager(index.Options{
		WorkspaceRoots: roots,
		IncludeDirs:    opts.includeDirs,
	})
	defer manager.Close()

	if err := manager.RescanWorkspace(ctx); err != nil {
		return nil, err
	}
	if err := manager.UpsertOpenDocument(ctx, index.DocumentInput{
		URI:        uri,
		Version:    -1,
		Generation: 0,
		Source:     src,
	}); err != nil {
		return nil, err
	}

	snapshot, ok := manager.Snapshot()
	if !ok {
		return nil, nil
	}
	view, ok, err := index.ViewForDocument(snapshot, uri)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return view, nil
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

func workspaceRootsForInput(opts cliOptions, uri string) ([]string, error) {
	roots := slices.Clone(opts.workspaceRoots)
	if len(roots) > 0 {
		return roots, nil
	}

	targetPath, err := filePathFromURI(uri)
	if err != nil {
		return nil, err
	}
	targetRoot := filepath.Dir(targetPath)

	switch opts.crossFile {
	case crossFileOff:
		return nil, nil
	case crossFileTransitive:
		return []string{targetRoot}, nil
	case crossFileWorkspace:
		if opts.stdin {
			return nil, errors.New("--workspace-root is required for --cross-file workspace with --stdin")
		}
		return []string{targetRoot}, nil
	default:
		return nil, fmt.Errorf("unsupported --cross-file %q", opts.crossFile)
	}
}

func filePathFromURI(raw string) (string, error) {
	displayURI, _, err := index.CanonicalizeDocumentURI(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(displayURI)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", u.Scheme)
	}
	return filepath.Clean(filepath.FromSlash(u.Path)), nil
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

func isSupportedCrossFileMode(v string) bool {
	switch v {
	case crossFileOff, crossFileTransitive, crossFileWorkspace:
		return true
	default:
		return false
	}
}

func defaultCrossFileMode(stdin bool) string {
	if stdin {
		return crossFileOff
	}
	return crossFileTransitive
}

type multiStringFlag []string

func (f *multiStringFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *multiStringFlag) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("value must not be empty")
	}
	*f = append(*f, v)
	return nil
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
