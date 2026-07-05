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

	cliutil "github.com/kpumuk/thrift-weaver/internal/cli"
	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/lint"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
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
		cliutil.WriteDiagnostics(stderr, "thriftlint", tree, diags, cliutil.DefaultDiagnosticMessage)
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

func writeJSONDiagnostics(w io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic) error {
	li := cliutil.LineIndexOrBuild(tree)
	uri := ""
	if tree != nil {
		uri = tree.URI
	}
	payload := make([]diagnosticJSON, 0, len(diags))
	for _, d := range diags {
		start, end, err := cliutil.DiagnosticPoints(li, d.Span)
		if err != nil {
			return err
		}
		payload = append(payload, diagnosticJSON{
			URI:       uri,
			Source:    d.Source,
			Code:      string(d.Code),
			Severity:  cliutil.SeverityName(d.Severity),
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

func writef(w io.Writer, format string, args ...any) {
	//nolint:gosec // Terminal/debug output helper; format strings are internal callsite constants.
	_, _ = io.WriteString(w, fmt.Sprintf(format, args...))
}
