package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
