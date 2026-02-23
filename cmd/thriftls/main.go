// Package main provides the thriftls CLI entry point.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kpumuk/thrift-weaver/internal/lsp"
)

func main() {
	if err := lsp.NewServer().RunStdio(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "thriftls:", err)
		os.Exit(1)
	}
}
