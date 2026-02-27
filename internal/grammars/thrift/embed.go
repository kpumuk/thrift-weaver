// Package thriftwasm embeds the Thrift tree-sitter wasm artifact in the binary.
package thriftwasm

import (
	_ "embed"
	"strings"
)

var (
	//go:embed thrift.wasm
	embeddedWASM []byte

	//go:embed thrift.wasm.sha256
	embeddedWASMChecksum string
)

// WASM returns a copy of the embedded wasm artifact bytes.
func WASM() []byte {
	return append([]byte(nil), embeddedWASM...)
}

// WASMChecksum returns the embedded artifact checksum.
func WASMChecksum() string {
	return strings.TrimSpace(embeddedWASMChecksum)
}
