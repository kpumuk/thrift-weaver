// Package main provides the thriftfmt CLI entry point.
package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(run(context.Background(), os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}
