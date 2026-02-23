package lsp

import (
	"context"
	"os"
)

// RunStdio serves LSP over stdio using Content-Length framing.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.Run(ctx, os.Stdin, os.Stdout)
}
