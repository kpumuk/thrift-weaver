// Package wasm contains the wasm parser backend wiring surface.
package wasm

import (
	"errors"

	"github.com/kpumuk/thrift-weaver/internal/syntax/backend"
)

// ErrNotReady is returned until the wasm runtime integration is implemented.
var ErrNotReady = errors.New("wasm parser backend is not wired yet")

const factoryName = "treesitter-wasm"

// Config is a placeholder for upcoming wasm backend runtime settings.
type Config struct{}

// Factory is the future wasm parser backend factory.
//
// M1 intentionally provides constructor wiring only.
type Factory struct {
	config Config
}

var _ backend.Factory = (*Factory)(nil)

// NewFactory constructs a wasm backend factory.
func NewFactory(config Config) *Factory {
	return &Factory{config: config}
}

// Name returns the stable backend identifier.
func (f *Factory) Name() string {
	return factoryName
}

// NewParser creates a parser instance.
//
// M1 intentionally returns ErrNotReady until wasm runtime integration lands.
func (f *Factory) NewParser() (backend.Parser, error) {
	_ = f.config
	return nil, ErrNotReady
}
