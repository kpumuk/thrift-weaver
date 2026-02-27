package syntax

import (
	"context"
	"sync"
	"testing"

	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

type observingFactory struct {
	mu             sync.Mutex
	newParserCalls int
}

func (f *observingFactory) Name() string {
	return "observing-test-factory"
}

func (f *observingFactory) NewParser() (parserbackend.Parser, error) {
	f.mu.Lock()
	f.newParserCalls++
	f.mu.Unlock()
	return ts.NewParser()
}

func (f *observingFactory) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newParserCalls
}

func TestParseUsesParserFactoryWiring(t *testing.T) {
	factory := &observingFactory{}
	restore := setParserFactoryForTesting(factory)
	defer restore()

	src := []byte("struct Wiring { 1: string name, }\n")
	if _, err := Parse(context.Background(), src, ParseOptions{URI: "file:///wiring.thrift"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := factory.calls(); got != 1 {
		t.Fatalf("NewParser() calls = %d, want 1", got)
	}
}
