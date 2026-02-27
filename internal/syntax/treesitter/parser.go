package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	thriftwasm "github.com/kpumuk/thrift-weaver/internal/grammars/thrift"
)

const wasmNodeSize = 24

var (
	// ErrWASMChecksumMismatch indicates artifact integrity mismatch.
	ErrWASMChecksumMismatch = errors.New("wasm checksum mismatch")
	// ErrWASMABIMismatch indicates an incompatible wasm export/import surface.
	ErrWASMABIMismatch = errors.New("wasm abi mismatch")

	runtimeInitOnce sync.Once
	runtimeInitErr  error
	runtimeState    runtimeModuleState
	parserModuleSeq uint64

	// Test hook: overridden in parser tests to validate startup failure paths.
	loadWASMArtifactFunc = loadWASMArtifact
)

type runtimeModuleState struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

var requiredWASMExports = []string{
	"malloc",
	"free",
	"strlen",
	"tree_sitter_thrift",
	"tw_parser_new",
	"tw_parser_delete",
	"tw_parser_set_language",
	"tw_parser_parse_string",
	"tw_tree_delete",
	"tw_tree_root_node",
	"tw_node_child_count",
	"tw_node_child",
	"tw_node_named_child_count",
	"tw_node_named_child",
	"tw_node_type",
	"tw_node_symbol",
	"tw_node_start_byte",
	"tw_node_end_byte",
	"tw_node_is_error",
	"tw_node_is_missing",
	"tw_node_is_named",
	"tw_node_is_extra",
	"tw_node_has_error",
}

// Parser wraps a no-cgo parser backed by in-process tree-sitter wasm runtime.
type Parser struct {
	module api.Module

	malloc api.Function
	free   api.Function
	strlen api.Function

	parserDelete      api.Function
	parserSetLanguage api.Function
	parserParseString api.Function

	treeDelete   api.Function
	treeRootNode api.Function

	nodeChildCount      api.Function
	nodeChild           api.Function
	nodeNamedChildCount api.Function
	nodeNamedChild      api.Function
	nodeType            api.Function
	nodeSymbol          api.Function
	nodeStartByte       api.Function
	nodeEndByte         api.Function
	nodeIsError         api.Function
	nodeIsMissing       api.Function
	nodeIsNamed         api.Function
	nodeIsExtra         api.Function
	nodeHasError        api.Function

	parserPtr uint64
}

// NewParser creates a parser and validates the wasm module ABI/checksum.
func NewParser() (*Parser, error) {
	runtimeInitOnce.Do(func() {
		runtimeInitErr = initRuntimeModule(context.Background())
	})
	if runtimeInitErr != nil {
		return nil, runtimeInitErr
	}

	ctx := context.Background()
	moduleName := fmt.Sprintf("thrift-parser-%d", atomic.AddUint64(&parserModuleSeq, 1))
	mod, err := runtimeState.runtime.InstantiateModule(
		ctx,
		runtimeState.compiled,
		wazero.NewModuleConfig().WithName(moduleName),
	)
	if err != nil {
		return nil, fmt.Errorf("instantiate parser module: %w", err)
	}

	p := &Parser{
		module: mod,

		malloc: mustExportedFunction(mod, "malloc"),
		free:   mustExportedFunction(mod, "free"),
		strlen: mustExportedFunction(mod, "strlen"),

		parserDelete:      mustExportedFunction(mod, "tw_parser_delete"),
		parserSetLanguage: mustExportedFunction(mod, "tw_parser_set_language"),
		parserParseString: mustExportedFunction(mod, "tw_parser_parse_string"),

		treeDelete:   mustExportedFunction(mod, "tw_tree_delete"),
		treeRootNode: mustExportedFunction(mod, "tw_tree_root_node"),

		nodeChildCount:      mustExportedFunction(mod, "tw_node_child_count"),
		nodeChild:           mustExportedFunction(mod, "tw_node_child"),
		nodeNamedChildCount: mustExportedFunction(mod, "tw_node_named_child_count"),
		nodeNamedChild:      mustExportedFunction(mod, "tw_node_named_child"),
		nodeType:            mustExportedFunction(mod, "tw_node_type"),
		nodeSymbol:          mustExportedFunction(mod, "tw_node_symbol"),
		nodeStartByte:       mustExportedFunction(mod, "tw_node_start_byte"),
		nodeEndByte:         mustExportedFunction(mod, "tw_node_end_byte"),
		nodeIsError:         mustExportedFunction(mod, "tw_node_is_error"),
		nodeIsMissing:       mustExportedFunction(mod, "tw_node_is_missing"),
		nodeIsNamed:         mustExportedFunction(mod, "tw_node_is_named"),
		nodeIsExtra:         mustExportedFunction(mod, "tw_node_is_extra"),
		nodeHasError:        mustExportedFunction(mod, "tw_node_has_error"),
	}

	parserNew := mustExportedFunction(mod, "tw_parser_new")
	ptr, err := parserNew.Call(ctx)
	if err != nil || len(ptr) == 0 || ptr[0] == 0 {
		_ = mod.Close(ctx)
		if err != nil {
			return nil, fmt.Errorf("create parser: %w", err)
		}
		return nil, errors.New("create parser: returned null parser pointer")
	}
	p.parserPtr = ptr[0]

	ok, err := p.parserSetLanguage.Call(ctx, p.parserPtr)
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("set parser language: %w", err)
	}
	if len(ok) == 0 || ok[0] == 0 {
		p.Close()
		return nil, fmt.Errorf("%w: parser rejected tree_sitter_thrift language", ErrWASMABIMismatch)
	}

	return p, nil
}

func mustExportedFunction(mod api.Module, name string) api.Function {
	fn := mod.ExportedFunction(name)
	if fn == nil {
		panic("missing required wasm function export: " + name)
	}
	return fn
}

// Close releases parser resources.
func (p *Parser) Close() {
	if p == nil {
		return
	}
	ctx := context.Background()
	if p.parserPtr != 0 && p.parserDelete != nil {
		_, _ = p.parserDelete.Call(ctx, p.parserPtr)
		p.parserPtr = 0
	}
	if p.module != nil {
		_ = p.module.Close(ctx)
		p.module = nil
	}
}

// Tree wraps a parsed tree.
type Tree struct {
	root *RawNode
}

// Close releases tree resources.
func (t *Tree) Close() {
	if t == nil {
		return
	}
	t.root = nil
}

// Inner returns the wrapped tree pointer.
func (t *Tree) Inner() *Tree {
	return t
}

// Root returns the wrapped root node.
func (t *Tree) Root() Node {
	if t == nil {
		return Node{}
	}
	return wrapNode(t.root)
}

// Parse parses src and returns a raw tree wrapper. old may be nil.
func (p *Parser) Parse(ctx context.Context, src []byte, old *Tree) (*Tree, error) {
	_ = old // Parse currently runs in full-parse mode and ignores the previous tree.
	if p == nil || p.module == nil || p.parserPtr == 0 {
		return nil, errors.New("nil parser")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	srcPtr, freeSrc, err := p.allocBytes(ctx, src)
	if err != nil {
		return nil, err
	}
	defer freeSrc()

	treeRes, err := p.parserParseString.Call(ctx, p.parserPtr, 0, srcPtr, uint64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("parse string: %w", err)
	}
	if len(treeRes) == 0 || treeRes[0] == 0 {
		return nil, errors.New("tree-sitter parse returned nil tree")
	}
	treePtr := treeRes[0]
	defer func() {
		_, _ = p.treeDelete.Call(context.Background(), treePtr)
	}()

	root, err := p.buildTreeFromWASM(ctx, treePtr)
	if err != nil {
		return nil, err
	}

	return &Tree{root: root}, nil
}

func (p *Parser) buildTreeFromWASM(ctx context.Context, treePtr uint64) (*RawNode, error) {
	nodePtr, freeNode, err := p.allocNode(ctx)
	if err != nil {
		return nil, err
	}
	defer freeNode()

	if _, err := p.treeRootNode.Call(ctx, treePtr, nodePtr); err != nil {
		return nil, fmt.Errorf("get root node: %w", err)
	}
	return p.buildRawNode(ctx, nodePtr)
}

func (p *Parser) buildRawNode(ctx context.Context, nodePtr uint64) (*RawNode, error) {
	kind, err := p.nodeKind(ctx, nodePtr)
	if err != nil {
		return nil, err
	}

	symbolID, err := p.callU32(ctx, p.nodeSymbol, nodePtr)
	if err != nil {
		return nil, err
	}
	symbol, ok := uint16FromU32(symbolID)
	if !ok {
		return nil, fmt.Errorf("node symbol out of range: %d", symbolID)
	}
	rememberNodeKind(symbol, kind)
	startByte, err := p.callU32(ctx, p.nodeStartByte, nodePtr)
	if err != nil {
		return nil, err
	}
	endByte, err := p.callU32(ctx, p.nodeEndByte, nodePtr)
	if err != nil {
		return nil, err
	}

	isNamed, err := p.callBool(ctx, p.nodeIsNamed, nodePtr)
	if err != nil {
		return nil, err
	}
	isError, err := p.callBool(ctx, p.nodeIsError, nodePtr)
	if err != nil {
		return nil, err
	}
	isMissing, err := p.callBool(ctx, p.nodeIsMissing, nodePtr)
	if err != nil {
		return nil, err
	}
	isExtra, err := p.callBool(ctx, p.nodeIsExtra, nodePtr)
	if err != nil {
		return nil, err
	}
	hasError, err := p.callBool(ctx, p.nodeHasError, nodePtr)
	if err != nil {
		return nil, err
	}

	childCount, err := p.callU32(ctx, p.nodeChildCount, nodePtr)
	if err != nil {
		return nil, err
	}
	children := make([]*RawNode, 0, childCount)
	for i := range childCount {
		childPtr, freeChild, err := p.allocNode(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := p.nodeChild.Call(ctx, nodePtr, uint64(i), childPtr); err != nil {
			freeChild()
			return nil, fmt.Errorf("get child[%d]: %w", i, err)
		}

		child, err := p.buildRawNode(ctx, childPtr)
		freeChild()
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}

	return &RawNode{
		Kind:      kind,
		KindID:    symbol,
		StartByte: int(startByte),
		EndByte:   int(endByte),
		IsNamed:   isNamed,
		IsError:   isError,
		IsMissing: isMissing,
		IsExtra:   isExtra,
		HasError:  hasError,
		Children:  children,
	}, nil
}

func (p *Parser) nodeKind(ctx context.Context, nodePtr uint64) (string, error) {
	ptr, err := p.callU64(ctx, p.nodeType, nodePtr)
	if err != nil {
		return "", err
	}
	return p.readCString(ctx, ptr)
}

func (p *Parser) allocNode(ctx context.Context) (uint64, func(), error) {
	ptr, err := p.callU64(ctx, p.malloc, wasmNodeSize)
	if err != nil {
		return 0, nil, fmt.Errorf("alloc node: %w", err)
	}
	return ptr, func() {
		p.freePtr(ptr)
	}, nil
}

func (p *Parser) allocBytes(ctx context.Context, bytes []byte) (uint64, func(), error) {
	size := uint64(len(bytes))
	if size == 0 {
		size = 1
	}
	ptr, err := p.callU64(ctx, p.malloc, size)
	if err != nil {
		return 0, nil, fmt.Errorf("alloc source bytes: %w", err)
	}
	ptr32, err := uint32FromU64(ptr)
	if err != nil {
		p.freePtr(ptr)
		return 0, nil, err
	}
	mem := p.module.Memory()
	if mem == nil {
		p.freePtr(ptr)
		return 0, nil, errors.New("missing wasm memory")
	}
	if len(bytes) > 0 && !mem.Write(ptr32, bytes) {
		p.freePtr(ptr)
		return 0, nil, errors.New("write source bytes into wasm memory")
	}
	return ptr, func() {
		p.freePtr(ptr)
	}, nil
}

func (p *Parser) readCString(ctx context.Context, ptr uint64) (string, error) {
	if ptr == 0 {
		return "", nil
	}
	size, err := p.callU64(ctx, p.strlen, ptr)
	if err != nil {
		return "", fmt.Errorf("strlen(%d): %w", ptr, err)
	}
	if size == 0 {
		return "", nil
	}
	ptr32, err := uint32FromU64(ptr)
	if err != nil {
		return "", err
	}
	size32, err := uint32FromU64(size)
	if err != nil {
		return "", err
	}
	mem := p.module.Memory()
	if mem == nil {
		return "", errors.New("missing wasm memory")
	}
	bytes, ok := mem.Read(ptr32, size32)
	if !ok {
		return "", fmt.Errorf("read C string ptr=%d size=%d", ptr, size)
	}
	return string(bytes), nil
}

func (p *Parser) freePtr(ptr uint64) {
	if p == nil || p.free == nil || ptr == 0 {
		return
	}
	_, _ = p.free.Call(context.Background(), ptr)
}

func (p *Parser) callBool(ctx context.Context, fn api.Function, args ...uint64) (bool, error) {
	v, err := p.callU64(ctx, fn, args...)
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

func (p *Parser) callU32(ctx context.Context, fn api.Function, args ...uint64) (uint32, error) {
	v, err := p.callU64(ctx, fn, args...)
	if err != nil {
		return 0, err
	}
	return uint32FromU64(v)
}

func (p *Parser) callU64(ctx context.Context, fn api.Function, args ...uint64) (uint64, error) {
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

func initRuntimeModule(ctx context.Context) error {
	wasmBytes, expected := loadWASMArtifactFunc()
	if expected == "" {
		return errors.New("empty wasm checksum")
	}
	actualArr := sha256.Sum256(wasmBytes)
	actual := hex.EncodeToString(actualArr[:])
	if actual != expected {
		return fmt.Errorf("%w: expected=%s actual=%s", ErrWASMChecksumMismatch, expected, actual)
	}

	r := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = r.Close(ctx)
		return fmt.Errorf("compile wasm module: %w", err)
	}

	exports := compiled.ExportedFunctions()
	for _, name := range requiredWASMExports {
		if _, ok := exports[name]; !ok {
			_ = compiled.Close(ctx)
			_ = r.Close(ctx)
			return fmt.Errorf("%w: missing %s export", ErrWASMABIMismatch, name)
		}
	}

	runtimeState = runtimeModuleState{
		runtime:  r,
		compiled: compiled,
	}
	return nil
}

func loadWASMArtifact() ([]byte, string) {
	return thriftwasm.WASM(), thriftwasm.WASMChecksum()
}

func uint32FromU64(v uint64) (uint32, error) {
	if v > math.MaxUint32 {
		return 0, fmt.Errorf("value overflows uint32: %d", v)
	}
	return uint32(v), nil
}

func uint16FromU32(v uint32) (uint16, bool) {
	if v > math.MaxUint16 {
		return 0, false
	}
	return uint16(v), true
}
