package syntax

import (
	"sync"

	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	"github.com/kpumuk/thrift-weaver/internal/syntax/backend/wasm"
)

var (
	parserFactoryMu sync.RWMutex
	parserFactory   parserbackend.Factory = wasm.NewFactory(wasm.Config{})
)

func currentParserFactory() parserbackend.Factory {
	parserFactoryMu.RLock()
	factory := parserFactory
	parserFactoryMu.RUnlock()
	return factory
}

func setParserFactoryForTesting(factory parserbackend.Factory) func() {
	parserFactoryMu.Lock()
	prev := parserFactory
	parserFactory = factory
	parserFactoryMu.Unlock()

	return func() {
		parserFactoryMu.Lock()
		parserFactory = prev
		parserFactoryMu.Unlock()
	}
}
