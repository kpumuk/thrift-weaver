package wasm

import (
	"errors"
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
	if parser != nil {
		t.Fatal("expected nil parser from placeholder wasm backend")
	}
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("NewParser() error = %v, want %v", err, ErrNotReady)
	}
}
