// Package main provides the thriftls CLI entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kpumuk/thrift-weaver/internal/lsp"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "thriftls:", err)
		os.Exit(1)
	}
}

type config struct {
	workspaceIndexWorkers int
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	return lsp.NewServerWithOptions(lsp.Options{
		WorkspaceIndexWorkers: cfg.workspaceIndexWorkers,
	}).Run(ctx, stdin, stdout)
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	cfg := config{}
	fs := flag.NewFlagSet("thriftls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(
		&cfg.workspaceIndexWorkers,
		"workspace-index-workers",
		0,
		"number of parallel workspace index parse workers (0 = auto)",
	)
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.workspaceIndexWorkers < 0 {
		return config{}, errors.New("--workspace-index-workers must be >= 0")
	}
	return cfg, nil
}
