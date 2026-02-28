// Package wasm contains the wasm parser backend wiring surface.
package wasm

import (
	"github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

const factoryName = "treesitter-wasm"

// Config defines wasm backend settings.
type Config struct{}

// Factory creates parser instances for the wasm backend.
type Factory struct{}

var _ backend.Factory = (*Factory)(nil)

// NewFactory constructs a wasm backend factory.
func NewFactory(config Config) *Factory {
	_ = config
	return &Factory{}
}

// Name returns the stable backend identifier.
func (f *Factory) Name() string {
	return factoryName
}

// NewParser creates a parser instance.
func (f *Factory) NewParser() (backend.Parser, error) {
	return ts.NewParser()
}
