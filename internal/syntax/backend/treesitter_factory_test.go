package backend

import "testing"

func TestNewTreeSitterFactoryWiring(t *testing.T) {
	t.Parallel()

	factory := NewTreeSitterFactory()
	if factory == nil {
		t.Fatal("factory is nil")
	}
	if factory.Name() != treeSitterFactoryName {
		t.Fatalf("factory.Name() = %q, want %q", factory.Name(), treeSitterFactoryName)
	}

	parser, err := factory.NewParser()
	if err != nil {
		t.Fatalf("NewParser() error = %v", err)
	}
	if parser == nil {
		t.Fatal("parser is nil")
	}
	parser.Close()
}
