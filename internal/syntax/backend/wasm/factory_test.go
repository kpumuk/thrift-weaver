package wasm

import (
	"testing"
)

func TestNewFactoryWiring(t *testing.T) {
	t.Parallel()

	factory := NewFactory(Config{})
	if factory == nil {
		t.Fatal("factory is nil")
	}
	if got := factory.Name(); got != factoryName {
		t.Fatalf("factory.Name() = %q, want %q", got, factoryName)
	}

	parser, err := factory.NewParser()
	if err != nil {
		t.Fatalf("NewParser() error = %v", err)
	}
	if parser == nil {
		t.Fatal("expected parser instance")
	}
	parser.Close()
}
