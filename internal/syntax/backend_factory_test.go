package syntax

import (
	"context"
	"errors"
	"sync"
	"testing"

	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

type observingFactory struct {
	mu             sync.Mutex
	newParserCalls int
}

type failingFactory struct {
	err error
}

func (f *failingFactory) Name() string {
	return "failing-test-factory"
}

func (f *failingFactory) NewParser() (parserbackend.Parser, error) {
	return nil, f.err
}

type failSecondParseFactory struct{}

func (f *failSecondParseFactory) Name() string {
	return "fail-second-parse-factory"
}

func (f *failSecondParseFactory) NewParser() (parserbackend.Parser, error) {
	inner, err := ts.NewParser()
	if err != nil {
		return nil, err
	}
	return &failSecondParseParser{inner: inner}, nil
}

type failSecondParseParser struct {
	inner parserbackend.Parser
	calls int
}

func (p *failSecondParseParser) Parse(ctx context.Context, src []byte, old *ts.Tree) (*ts.Tree, error) {
	p.calls++
	if p.calls >= 2 {
		return nil, errors.New("injected parse failure")
	}
	return p.inner.Parse(ctx, src, old)
}

func (p *failSecondParseParser) Close() {
	if p.inner != nil {
		p.inner.Close()
		p.inner = nil
	}
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

func TestParseFailOpenWhenParserInitializationFails(t *testing.T) {
	restore := setParserFactoryForTesting(&failingFactory{err: errors.New("parser init unavailable")})
	defer restore()

	tree, err := Parse(context.Background(), []byte("struct S { 1: string a }\n"), ParseOptions{URI: "file:///fail-open.thrift"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if tree.Root != NoNode {
		t.Fatalf("expected degraded tree root=NoNode, got %d", tree.Root)
	}

	var sawInternalParse bool
	for _, d := range tree.Diagnostics {
		if d.Code == DiagnosticInternalParse && d.Source == "parser" {
			sawInternalParse = true
			if d.Recoverable {
				t.Fatalf("expected non-recoverable parse diagnostic, got %+v", d)
			}
			break
		}
	}
	if !sawInternalParse {
		t.Fatalf("expected INTERNAL_PARSE diagnostic, got %+v", tree.Diagnostics)
	}
}

func TestReparseFailOpenWhenExistingParserFails(t *testing.T) {
	restore := setParserFactoryForTesting(&failSecondParseFactory{})
	defer restore()

	src := []byte("struct S { 1: string name, }\n")
	oldTree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///reparse-fail-open.thrift", Version: 1})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	nextTree, err := Reparse(context.Background(), oldTree, src, ParseOptions{URI: "file:///reparse-fail-open.thrift", Version: 2})
	if err != nil {
		t.Fatalf("Reparse() error = %v", err)
	}
	if nextTree.Root != NoNode {
		t.Fatalf("expected degraded tree root=NoNode, got %d", nextTree.Root)
	}
	if !hasDiagnosticCode(nextTree.Diagnostics, DiagnosticInternalParse) {
		t.Fatalf("expected INTERNAL_PARSE diagnostic on reparse fallback, got %+v", nextTree.Diagnostics)
	}
}
