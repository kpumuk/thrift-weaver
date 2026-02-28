package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
)

var parserRuntimeTestMu sync.Mutex

func TestParserParsesSimpleFixture(t *testing.T) {
	withIsolatedRuntimeState(t, func(t *testing.T) {
		p, err := NewParser()
		if err != nil {
			t.Fatalf("NewParser() error = %v", err)
		}
		defer p.Close()

		tree, err := p.Parse(context.Background(), []byte("struct User { 1: string name, }"), nil)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		defer tree.Close()

		root := tree.Root()
		if got := root.Kind(); got != "source_file" {
			t.Fatalf("root.Kind() = %q, want source_file", got)
		}
		if root.ChildCount() == 0 {
			t.Fatal("expected root to have children")
		}
	})
}

func TestNewParserFailsOnChecksumMismatch(t *testing.T) {
	assertNewParserInitError(t, ErrWASMChecksumMismatch, func() ([]byte, string) {
		return []byte("not-wasm"), "deadbeef"
	})
}

func TestNewParserFailsOnABIMismatch(t *testing.T) {
	emptyModule := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	sum := sha256.Sum256(emptyModule)
	assertNewParserInitError(t, ErrWASMABIMismatch, func() ([]byte, string) {
		return append([]byte(nil), emptyModule...), hex.EncodeToString(sum[:])
	})
}

func TestParserIncrementalParseReturnsChangedRanges(t *testing.T) {
	withIsolatedRuntimeState(t, func(t *testing.T) {
		p, err := NewParser()
		if err != nil {
			t.Fatalf("NewParser() error = %v", err)
		}
		defer p.Close()

		src := []byte("struct User {\n  1: string name,\n}\n")
		oldTree, err := p.Parse(context.Background(), src, nil)
		if err != nil {
			t.Fatalf("initial parse: %v", err)
		}
		defer oldTree.Close()

		idx := strings.Index(string(src), "name")
		if idx < 0 {
			t.Fatal("marker not found")
		}
		edit := InputEdit{
			StartByte:  idx,
			OldEndByte: idx + len("name"),
			NewEndByte: idx + len("xname"),
			StartPoint: Point{
				Row:    1,
				Column: 12,
			},
			OldEndPoint: Point{
				Row:    1,
				Column: 16,
			},
			NewEndPoint: Point{
				Row:    1,
				Column: 17,
			},
		}
		if err := oldTree.ApplyEdit(context.Background(), edit); err != nil {
			t.Fatalf("ApplyEdit: %v", err)
		}

		nextSrc := []byte("struct User {\n  1: string xname,\n}\n")
		nextTree, err := p.Parse(context.Background(), nextSrc, oldTree)
		if err != nil {
			t.Fatalf("incremental parse: %v", err)
		}
		defer nextTree.Close()

		changed, err := oldTree.ChangedRanges(context.Background(), nextTree)
		if err != nil {
			t.Fatalf("ChangedRanges: %v", err)
		}
		for i, r := range changed {
			if r.EndByte < r.StartByte {
				t.Fatalf("invalid changed range[%d]: %+v", i, r)
			}
		}
	})
}

func resetRuntimeForTesting(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	if runtimeState.compiled != nil {
		_ = runtimeState.compiled.Close(ctx)
	}
	if runtimeState.runtime != nil {
		_ = runtimeState.runtime.Close(ctx)
	}

	runtimeState = runtimeModuleState{}
	runtimeInitErr = nil
	runtimeInitOnce = sync.Once{}
	parserModuleSeq = 0
}

func withIsolatedRuntimeState(t *testing.T, fn func(t *testing.T)) {
	t.Helper()

	parserRuntimeTestMu.Lock()
	defer parserRuntimeTestMu.Unlock()
	resetRuntimeForTesting(t)
	defer resetRuntimeForTesting(t)

	fn(t)
}

func withWASMArtifactLoader(loader func() ([]byte, string)) func() {
	prev := loadWASMArtifactFunc
	loadWASMArtifactFunc = loader
	return func() {
		loadWASMArtifactFunc = prev
	}
}

func assertNewParserInitError(t *testing.T, expected error, loader func() ([]byte, string)) {
	t.Helper()

	withIsolatedRuntimeState(t, func(t *testing.T) {
		restoreLoader := withWASMArtifactLoader(loader)
		defer restoreLoader()

		_, err := NewParser()
		if !errors.Is(err, expected) {
			t.Fatalf("NewParser() error = %v, want %v", err, expected)
		}
	})
}
