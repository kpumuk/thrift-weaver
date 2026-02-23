package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/format"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const (
	exitOK       = 0
	exitCheck    = 1
	exitUnsafe   = 2
	exitInternal = 3
)

type cliOptions struct {
	write          bool
	check          bool
	stdin          bool
	stdout         bool
	assumeFilename string
	lineWidth      int
	rangeSpec      string
	debugTokens    bool
	debugCST       bool
	path           string
}

func run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	opts, usage, err := parseArgs(args)
	if err != nil {
		writef(stderr, "thriftfmt: %v\n\n%s", err, usage)
		return exitInternal
	}

	src, pathURI, err := readInput(stdin, opts)
	if err != nil {
		writef(stderr, "thriftfmt: %v\n", err)
		return exitInternal
	}

	tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: pathURI})
	if err != nil {
		writef(stderr, "thriftfmt: parse failed: %v\n", err)
		return exitInternal
	}

	if opts.debugTokens {
		dumpTokens(stdout, tree)
	}
	if opts.debugCST {
		dumpCST(stdout, tree)
	}

	var rangeSpan *text.Span
	if opts.rangeSpec != "" {
		parsed, err := parseRangeFlag(opts.rangeSpec)
		if err != nil {
			writef(stderr, "thriftfmt: invalid --range: %v\n", err)
			return exitInternal
		}
		rangeSpan = &parsed
	}

	fopts := format.Options{LineWidth: opts.lineWidth}
	if rangeSpan == nil {
		res, err := format.Document(ctx, tree, fopts)
		if err != nil {
			return handleFormatError(stderr, tree, res.Diagnostics, err)
		}
		return handleDocumentResult(stdout, stderr, opts, src, res)
	}

	res, err := format.Range(ctx, tree, *rangeSpan, fopts)
	if err != nil {
		return handleFormatError(stderr, tree, res.Diagnostics, err)
	}
	return handleRangeResult(stdout, stderr, opts, src, res)
}

func parseArgs(args []string) (cliOptions, string, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("thriftfmt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.BoolVar(&opts.write, "write", false, "write result in-place")
	fs.BoolVar(&opts.write, "w", false, "write result in-place")
	fs.BoolVar(&opts.check, "check", false, "exit non-zero if formatting changes are needed")
	fs.BoolVar(&opts.stdin, "stdin", false, "read input from stdin")
	fs.BoolVar(&opts.stdout, "stdout", false, "write formatted output to stdout")
	fs.StringVar(&opts.assumeFilename, "assume-filename", "", "filename/URI used for parser context and diagnostics")
	fs.IntVar(&opts.lineWidth, "line-width", 0, "maximum line width")
	fs.StringVar(&opts.rangeSpec, "range", "", "optional byte range start:end (half-open)")
	fs.BoolVar(&opts.debugTokens, "debug-tokens", false, "dump lexer tokens")
	fs.BoolVar(&opts.debugCST, "debug-cst", false, "dump CST nodes")

	usage := cliUsage(fs)
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, usage, err
	}

	if opts.stdin && opts.write {
		return cliOptions{}, usage, errors.New("--write and --stdin may not be used together")
	}
	if opts.check && opts.write {
		return cliOptions{}, usage, errors.New("--check and --write may not be used together")
	}
	if opts.stdout && opts.write {
		return cliOptions{}, usage, errors.New("--stdout and --write may not be used together")
	}

	rest := fs.Args()
	switch {
	case opts.stdin && len(rest) > 0:
		return cliOptions{}, usage, errors.New("positional file path is not allowed with --stdin")
	case !opts.stdin && len(rest) == 0:
		return cliOptions{}, usage, errors.New("exactly one input file path is required (or use --stdin)")
	case !opts.stdin && len(rest) != 1:
		return cliOptions{}, usage, errors.New("formatting multiple files in one invocation is not supported")
	}
	if !opts.stdin {
		opts.path = rest[0]
	}
	return opts, usage, nil
}

func cliUsage(fs *flag.FlagSet) string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	b.WriteString("  thriftfmt [flags] path/to/file.thrift\n")
	b.WriteString("  thriftfmt --stdin [--assume-filename foo.thrift] [flags]\n\n")
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

func handleDocumentResult(stdout, stderr io.Writer, opts cliOptions, original []byte, res format.Result) int {
	if opts.check {
		return checkExitCode(res.Changed)
	}
	if opts.write {
		if !res.Changed {
			return exitOK
		}
		if err := writeOutputFile(opts.path, res.Output); err != nil {
			writef(stderr, "thriftfmt: write %s: %v\n", opts.path, err)
			return exitInternal
		}
		return exitOK
	}
	if !res.Changed && !opts.stdout {
		_, _ = stdout.Write(original)
		return exitOK
	}
	_, _ = stdout.Write(res.Output)
	return exitOK
}

func handleRangeResult(stdout, stderr io.Writer, opts cliOptions, original []byte, res format.RangeResult) int {
	out, err := text.ApplyEdits(original, res.Edits)
	if err != nil {
		writef(stderr, "thriftfmt: apply range edits: %v\n", err)
		return exitInternal
	}
	changed := len(res.Edits) > 0
	if opts.check {
		return checkExitCode(changed)
	}
	if opts.write {
		if !changed {
			return exitOK
		}
		if err := writeOutputFile(opts.path, out); err != nil {
			writef(stderr, "thriftfmt: write %s: %v\n", opts.path, err)
			return exitInternal
		}
		return exitOK
	}
	_, _ = stdout.Write(out)
	return exitOK
}

func handleFormatError(stderr io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic, err error) int {
	if format.IsErrUnsafeToFormat(err) {
		writeDiagnostics(stderr, tree, diags)
		writef(stderr, "thriftfmt: %v\n", err)
		return exitUnsafe
	}
	if len(diags) > 0 {
		writeDiagnostics(stderr, tree, diags)
	}
	writef(stderr, "thriftfmt: %v\n", err)
	return exitInternal
}

func writeDiagnostics(w io.Writer, tree *syntax.Tree, diags []syntax.Diagnostic) {
	if len(diags) == 0 {
		return
	}
	var li *text.LineIndex
	uri := ""
	if tree != nil {
		li = tree.LineIndex
		uri = tree.URI
	}
	for _, d := range diags {
		loc := d.Span.String()
		if li != nil && d.Span.Start.IsValid() {
			if p, err := li.OffsetToPoint(d.Span.Start); err == nil {
				loc = fmt.Sprintf("%d:%d", p.Line+1, p.Column+1)
			}
		}
		prefix := "thriftfmt"
		if uri != "" {
			prefix = uri
		}
		writef(w, "%s:%s: %s (%s/%s)\n", prefix, loc, d.Message, d.Source, d.Code)
	}
}

func parseRangeFlag(s string) (text.Span, error) {
	startS, endS, ok := strings.Cut(s, ":")
	if !ok {
		return text.Span{}, errors.New("expected start:end")
	}
	start, err := strconv.Atoi(startS)
	if err != nil {
		return text.Span{}, fmt.Errorf("invalid start %q", startS)
	}
	end, err := strconv.Atoi(endS)
	if err != nil {
		return text.Span{}, fmt.Errorf("invalid end %q", endS)
	}
	return text.NewSpan(text.ByteOffset(start), text.ByteOffset(end))
}

func dumpTokens(w io.Writer, tree *syntax.Tree) {
	writeln(w, "TOKENS")
	for i, tok := range tree.Tokens {
		writef(w, "[%d] kind=%s span=%s text=%q", i, tok.Kind, tok.Span, tok.Bytes(tree.Source))
		if len(tok.Leading) > 0 {
			writeString(w, " leading=[")
			for j, tr := range tok.Leading {
				if j > 0 {
					writeString(w, ", ")
				}
				writef(w, "%s@%s:%q", tr.Kind, tr.Span, tr.Bytes(tree.Source))
			}
			writeString(w, "]")
		}
		writeln(w)
	}
}

func dumpCST(w io.Writer, tree *syntax.Tree) {
	writef(w, "CST root=%d\n", tree.Root)
	for i := 1; i < len(tree.Nodes); i++ {
		n := tree.Nodes[i]
		writef(
			w,
			"[%d] kind=%s span=%s tokens=%d..%d parent=%d flags=%s children=%d\n",
			n.ID,
			syntax.KindName(n.Kind),
			n.Span,
			n.FirstToken,
			n.LastToken,
			n.Parent,
			formatNodeFlags(n.Flags),
			len(n.Children),
		)
	}
}

func writeOutputFile(path string, data []byte) error {
	mode := os.FileMode(0o600)
	//nolint:gosec // CLI reads metadata for a user-specified output path.
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
	}
	//nolint:gosec // CLI writes formatter output to a user-specified path.
	return os.WriteFile(path, data, mode)
}

func checkExitCode(changed bool) int {
	if changed {
		return exitCheck
	}
	return exitOK
}

func writef(w io.Writer, format string, args ...any) {
	//nolint:gosec // Terminal/debug output helper; format strings are internal callsite constants.
	_, _ = io.WriteString(w, fmt.Sprintf(format, args...))
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func writeString(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

func formatNodeFlags(f syntax.NodeFlags) string {
	var parts []string
	if f.Has(syntax.NodeFlagNamed) {
		parts = append(parts, "named")
	}
	if f.Has(syntax.NodeFlagError) {
		parts = append(parts, "error")
	}
	if f.Has(syntax.NodeFlagMissing) {
		parts = append(parts, "missing")
	}
	if f.Has(syntax.NodeFlagRecovered) {
		parts = append(parts, "recovered")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "|")
}
