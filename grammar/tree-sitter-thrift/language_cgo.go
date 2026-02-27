//go:build cgo && thriftweaver_cgo

// Package treesitterthrift exposes the generated tree-sitter grammar and queries for Thrift IDL.
package treesitterthrift

/*
#cgo CFLAGS: -std=c11 -fPIC -Isrc
#include "src/parser.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Language returns the tree-sitter language for the Thrift grammar.
func Language() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_thrift()))
}
