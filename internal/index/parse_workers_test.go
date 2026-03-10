package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

type countingParserFactory struct {
	mu    sync.Mutex
	calls int
}

func (f *countingParserFactory) Name() string {
	return "counting-parser-factory"
}

func (f *countingParserFactory) NewParser() (parserbackend.Parser, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return ts.NewParser()
}

func (f *countingParserFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestManagerRescanWorkspaceReusesParsersWithinWorkerLimit(t *testing.T) {
	root := t.TempDir()
	for i := range 6 {
		path := filepath.Join(root, fmt.Sprintf("file-%02d.thrift", i))
		src := fmt.Sprintf("struct Type%02d {\n  1: string name,\n}\n", i)
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}

	factory := &countingParserFactory{}
	restoreFactory := syntax.SetParserFactoryForTesting(factory)
	defer restoreFactory()

	m := NewManager(Options{
		WorkspaceRoots: []string{root},
		ParseWorkers:   2,
	})
	defer m.Close()

	if err := m.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}

	snap, ok := m.Snapshot()
	if !ok || snap == nil {
		t.Fatal("expected workspace snapshot")
	}
	if len(snap.Documents) != 6 {
		t.Fatalf("documents=%d, want 6", len(snap.Documents))
	}
	if got := factory.callCount(); got < 1 || got > 2 {
		t.Fatalf("NewParser() calls = %d, want between 1 and 2", got)
	}
}
