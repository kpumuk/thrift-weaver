package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

const (
	wasmInputEditSize    = 36
	wasmChangedRangeSize = 24
	wasmNodeInfoSize     = 20
	wasmFlatNodeSize     = 28
)

const (
	wasmNodeFlagNamed uint32 = 1 << iota
	wasmNodeFlagError
	wasmNodeFlagMissing
	wasmNodeFlagExtra
	wasmNodeFlagHasError
)

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
	"tw_tree_edit",
	"tw_tree_changed_ranges",
	"tw_tree_export_nodes",
	"tw_tree_root_node",
	"tw_node_inspect",
	"tw_node_children",
	"tw_node_type",
}

// Point is a UTF-8 byte-based tree-sitter point.
type Point struct {
	Row    int
	Column int
}

// InputEdit describes an in-place source edit for ts_tree_edit.
type InputEdit struct {
	StartByte   int
	OldEndByte  int
	NewEndByte  int
	StartPoint  Point
	OldEndPoint Point
	NewEndPoint Point
}

// ChangedRange describes a changed byte/point span between two tree versions.
type ChangedRange struct {
	StartByte  int
	EndByte    int
	StartPoint Point
	EndPoint   Point
}

// Parser wraps an in-process tree-sitter parser backed by a wasm runtime.
type Parser struct {
	module api.Module

	malloc api.Function
	free   api.Function
	strlen api.Function

	parserDelete      api.Function
	parserSetLanguage api.Function
	parserParseString api.Function

	treeDelete        api.Function
	treeEdit          api.Function
	treeChangedRanges api.Function
	treeExportNodes   api.Function
	treeRootNode      api.Function

	nodeInspect  api.Function
	nodeChildren api.Function
	nodeType     api.Function

	parserPtr uint64

	flatNodesBufPtr      uint64
	flatNodesBufCapBytes uint64
	flatNodesScratch     []FlatNode
	symbolTypeScratch    map[uint16]uint32
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

		treeDelete:        mustExportedFunction(mod, "tw_tree_delete"),
		treeEdit:          mustExportedFunction(mod, "tw_tree_edit"),
		treeChangedRanges: mustExportedFunction(mod, "tw_tree_changed_ranges"),
		treeExportNodes:   mustExportedFunction(mod, "tw_tree_export_nodes"),
		treeRootNode:      mustExportedFunction(mod, "tw_tree_root_node"),

		nodeInspect:  mustExportedFunction(mod, "tw_node_inspect"),
		nodeChildren: mustExportedFunction(mod, "tw_node_children"),
		nodeType:     mustExportedFunction(mod, "tw_node_type"),

		symbolTypeScratch: make(map[uint16]uint32, 64),
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
	p.releaseFlatNodesBuffer()
	p.flatNodesScratch = nil
	p.symbolTypeScratch = nil
	if p.module != nil {
		_ = p.module.Close(ctx)
		p.module = nil
	}
}

// Tree wraps a parsed tree.
type Tree struct {
	root    *RawNode
	owner   *Parser
	treePtr uint64
}

// FlatNode is a compact node record emitted by Tree.Flatten.
type FlatNode struct {
	KindID     uint16
	StartByte  int
	EndByte    int
	ChildCount uint32
	IsNamed    bool
	IsError    bool
	IsMissing  bool
	IsExtra    bool
	HasError   bool
	Parent     int
}

type nodeInfo struct {
	Symbol     uint16
	StartByte  int
	EndByte    int
	ChildCount uint32
	Flags      uint32
}

func (n nodeInfo) has(flag uint32) bool {
	return n.Flags&flag != 0
}

// Close releases tree resources.
func (t *Tree) Close() {
	if t == nil {
		return
	}
	if t.owner != nil && t.owner.treeDelete != nil && t.treePtr != 0 {
		_, _ = t.owner.treeDelete.Call(context.Background(), t.treePtr)
	}
	t.root = nil
	t.owner = nil
	t.treePtr = 0
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
	if t.root == nil && t.owner != nil && t.treePtr != 0 {
		root, err := t.owner.buildTreeFromWASM(context.Background(), t.treePtr)
		if err == nil {
			t.root = root
		}
	}
	return wrapNode(t.root)
}

// Flatten exports the tree as a pre-order flat node stream with parent indices.
func (t *Tree) Flatten(ctx context.Context) ([]FlatNode, error) {
	flat, err := t.FlattenInto(ctx, nil)
	if err != nil {
		return nil, err
	}
	return append([]FlatNode(nil), flat...), nil
}

// FlattenInto exports the tree as a pre-order flat node stream with parent indices into dst.
// The returned slice aliases parser-owned scratch memory when dst is nil.
func (t *Tree) FlattenInto(ctx context.Context, dst []FlatNode) ([]FlatNode, error) {
	if t == nil || t.owner == nil || t.treePtr == 0 {
		return nil, errors.New("nil tree")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return t.owner.flattenTreeFromWASM(ctx, t.treePtr, dst)
}

// ApplyEdit applies an incremental input edit to this tree.
func (t *Tree) ApplyEdit(ctx context.Context, edit InputEdit) error {
	if t == nil || t.owner == nil || t.treePtr == 0 {
		return errors.New("nil tree")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	editPtr, err := t.owner.allocInputEdit(ctx, edit)
	if err != nil {
		return err
	}
	defer t.owner.freePtr(editPtr)
	if _, err := t.owner.treeEdit.Call(ctx, t.treePtr, editPtr); err != nil {
		return fmt.Errorf("apply tree edit: %w", err)
	}
	return nil
}

// ChangedRanges returns changed ranges between the receiver and next tree.
func (t *Tree) ChangedRanges(ctx context.Context, next *Tree) ([]ChangedRange, error) {
	if t == nil || next == nil {
		return nil, nil
	}
	if t.owner == nil || next.owner == nil || t.treePtr == 0 || next.treePtr == 0 {
		return nil, errors.New("nil tree")
	}
	if t.owner != next.owner {
		return nil, errors.New("changed-ranges requires trees from the same parser")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	total, err := t.owner.callU32(ctx, t.owner.treeChangedRanges, t.treePtr, next.treePtr, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("query changed range count: %w", err)
	}
	if total == 0 {
		return nil, nil
	}

	totalU64 := uint64(total)
	size := totalU64 * wasmChangedRangeSize
	ptr, err := t.owner.callU64(ctx, t.owner.malloc, size)
	if err != nil {
		return nil, fmt.Errorf("alloc changed ranges buffer: %w", err)
	}
	defer t.owner.freePtr(ptr)

	written, err := t.owner.callU32(ctx, t.owner.treeChangedRanges, t.treePtr, next.treePtr, ptr, uint64(total))
	if err != nil {
		return nil, fmt.Errorf("read changed ranges: %w", err)
	}
	if written > total {
		return nil, fmt.Errorf("changed ranges overflow: wrote=%d total=%d", written, total)
	}

	ptr32, err := uint32FromU64(ptr)
	if err != nil {
		return nil, err
	}
	byteCount := uint64(written) * wasmChangedRangeSize
	byteCount32, err := uint32FromU64(byteCount)
	if err != nil {
		return nil, err
	}
	mem := t.owner.module.Memory()
	if mem == nil {
		return nil, errors.New("missing wasm memory")
	}
	buf, ok := mem.Read(ptr32, byteCount32)
	if !ok {
		return nil, fmt.Errorf("read changed ranges buffer: ptr=%d size=%d", ptr, byteCount)
	}

	ranges := make([]ChangedRange, 0, written)
	for i := range written {
		base := int(i * wasmChangedRangeSize)
		ranges = append(ranges, ChangedRange{
			StartByte: int(binary.LittleEndian.Uint32(buf[base : base+4])),
			EndByte:   int(binary.LittleEndian.Uint32(buf[base+4 : base+8])),
			StartPoint: Point{
				Row:    int(binary.LittleEndian.Uint32(buf[base+8 : base+12])),
				Column: int(binary.LittleEndian.Uint32(buf[base+12 : base+16])),
			},
			EndPoint: Point{
				Row:    int(binary.LittleEndian.Uint32(buf[base+16 : base+20])),
				Column: int(binary.LittleEndian.Uint32(buf[base+20 : base+24])),
			},
		})
	}
	return ranges, nil
}

// Parse parses src and returns a tree wrapper. old may be nil.
func (p *Parser) Parse(ctx context.Context, src []byte, old *Tree) (*Tree, error) {
	if p == nil || p.module == nil || p.parserPtr == 0 {
		return nil, errors.New("nil parser")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	srcPtr, err := p.allocBytes(ctx, src)
	if err != nil {
		return nil, err
	}
	defer p.freePtr(srcPtr)

	oldPtr := uint64(0)
	if old != nil {
		if old.owner != p {
			return nil, errors.New("old tree belongs to a different parser instance")
		}
		oldPtr = old.treePtr
	}

	treeRes, err := p.parserParseString.Call(ctx, p.parserPtr, oldPtr, srcPtr, uint64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("parse string: %w", err)
	}
	if len(treeRes) == 0 || treeRes[0] == 0 {
		return nil, errors.New("tree-sitter parse returned nil tree")
	}
	treePtr := treeRes[0]

	return &Tree{
		owner:   p,
		treePtr: treePtr,
	}, nil
}

func (p *Parser) flattenTreeFromWASM(ctx context.Context, treePtr uint64, dst []FlatNode) ([]FlatNode, error) {
	total, err := p.callU32(ctx, p.treeExportNodes, treePtr, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("count flat nodes: %w", err)
	}
	if total == 0 {
		return nil, nil
	}

	byteSize := uint64(total) * wasmFlatNodeSize
	nodesPtr, err := p.ensureFlatNodesBuffer(ctx, byteSize)
	if err != nil {
		return nil, fmt.Errorf("alloc flat nodes buffer: %w", err)
	}

	written, err := p.callU32(ctx, p.treeExportNodes, treePtr, nodesPtr, uint64(total))
	if err != nil {
		return nil, fmt.Errorf("read flat nodes: %w", err)
	}
	if written > total {
		return nil, fmt.Errorf("flat node overflow: wrote=%d total=%d", written, total)
	}
	if written < total {
		return nil, fmt.Errorf("flat node underflow: wrote=%d total=%d", written, total)
	}

	nodesPtr32, err := uint32FromU64(nodesPtr)
	if err != nil {
		return nil, err
	}
	byteSize32, err := uint32FromU64(byteSize)
	if err != nil {
		return nil, err
	}
	mem := p.module.Memory()
	if mem == nil {
		return nil, errors.New("missing wasm memory")
	}
	buf, ok := mem.Read(nodesPtr32, byteSize32)
	if !ok {
		return nil, fmt.Errorf("read flat nodes buffer: ptr=%d size=%d", nodesPtr, byteSize)
	}

	out := p.acquireFlatNodesScratch(dst, written)
	p.flatNodesScratch = out

	clear(p.symbolTypeScratch)
	for i := range written {
		base := int(i * wasmFlatNodeSize)
		symbol32 := binary.LittleEndian.Uint32(buf[base : base+4])
		symbol, ok := uint16FromU32(symbol32)
		if !ok {
			return nil, fmt.Errorf("flat node symbol out of range: %d", symbol32)
		}
		flags := binary.LittleEndian.Uint32(buf[base+16 : base+20])
		typePtr := binary.LittleEndian.Uint32(buf[base+20 : base+24])
		parentPlusOne := binary.LittleEndian.Uint32(buf[base+24 : base+28])
		if _, ok := p.symbolTypeScratch[symbol]; !ok {
			p.symbolTypeScratch[symbol] = typePtr
		}

		parent := -1
		if parentPlusOne > 0 {
			parent = int(parentPlusOne - 1)
		}

		out[i] = FlatNode{
			KindID:     symbol,
			StartByte:  int(binary.LittleEndian.Uint32(buf[base+4 : base+8])),
			EndByte:    int(binary.LittleEndian.Uint32(buf[base+8 : base+12])),
			ChildCount: binary.LittleEndian.Uint32(buf[base+12 : base+16]),
			IsNamed:    flags&wasmNodeFlagNamed != 0,
			IsError:    flags&wasmNodeFlagError != 0,
			IsMissing:  flags&wasmNodeFlagMissing != 0,
			IsExtra:    flags&wasmNodeFlagExtra != 0,
			HasError:   flags&wasmNodeFlagHasError != 0,
			Parent:     parent,
		}
	}
	for symbol, typePtr := range p.symbolTypeScratch {
		if _, cached := lookupNodeKind(symbol); cached {
			continue
		}
		kind, err := p.readCString(ctx, uint64(typePtr))
		if err != nil {
			return nil, fmt.Errorf("read node kind for symbol %d: %w", symbol, err)
		}
		rememberNodeKind(symbol, kind)
	}
	return out, nil
}

func (p *Parser) ensureFlatNodesBuffer(ctx context.Context, byteSize uint64) (uint64, error) {
	if byteSize == 0 {
		return 0, nil
	}
	if p.flatNodesBufPtr != 0 && p.flatNodesBufCapBytes >= byteSize {
		return p.flatNodesBufPtr, nil
	}
	p.releaseFlatNodesBuffer()
	ptr, err := p.callU64(ctx, p.malloc, byteSize)
	if err != nil {
		return 0, err
	}
	p.flatNodesBufPtr = ptr
	p.flatNodesBufCapBytes = byteSize
	return ptr, nil
}

func (p *Parser) releaseFlatNodesBuffer() {
	if p.flatNodesBufPtr == 0 {
		return
	}
	p.freePtr(p.flatNodesBufPtr)
	p.flatNodesBufPtr = 0
	p.flatNodesBufCapBytes = 0
}

func (p *Parser) acquireFlatNodesScratch(dst []FlatNode, count uint32) []FlatNode {
	if dst == nil {
		dst = p.flatNodesScratch
	}
	if cap(dst) < int(count) {
		return make([]FlatNode, count)
	}
	return dst[:count]
}

func (p *Parser) buildTreeFromWASM(ctx context.Context, treePtr uint64) (*RawNode, error) {
	nodePtr, err := p.allocNode(ctx)
	if err != nil {
		return nil, err
	}
	defer p.freePtr(nodePtr)

	infoPtr, err := p.callU64(ctx, p.malloc, wasmNodeInfoSize)
	if err != nil {
		return nil, fmt.Errorf("alloc node info: %w", err)
	}
	defer p.freePtr(infoPtr)

	if _, err := p.treeRootNode.Call(ctx, treePtr, nodePtr); err != nil {
		return nil, fmt.Errorf("get root node: %w", err)
	}
	return p.buildRawNode(ctx, nodePtr, infoPtr)
}

func (p *Parser) buildRawNode(ctx context.Context, nodePtr uint64, infoPtr uint64) (*RawNode, error) {
	info, err := p.inspectNode(ctx, nodePtr, infoPtr)
	if err != nil {
		return nil, err
	}

	kind, err := p.nodeKind(ctx, nodePtr, info.Symbol)
	if err != nil {
		return nil, err
	}

	children := make([]*RawNode, 0, info.ChildCount)
	if info.ChildCount > 0 {
		childrenBufSize := uint64(info.ChildCount) * uint64(wasmNodeSize)
		childrenBufPtr, allocErr := p.callU64(ctx, p.malloc, childrenBufSize)
		if allocErr != nil {
			return nil, fmt.Errorf("alloc children buffer: %w", allocErr)
		}
		defer p.freePtr(childrenBufPtr)

		total, err := p.callU32(ctx, p.nodeChildren, nodePtr, childrenBufPtr, uint64(info.ChildCount))
		if err != nil {
			return nil, fmt.Errorf("get children: %w", err)
		}
		if total < info.ChildCount {
			return nil, fmt.Errorf("children underflow: got=%d want=%d", total, info.ChildCount)
		}

		for i := range info.ChildCount {
			childPtr := childrenBufPtr + uint64(i)*uint64(wasmNodeSize)
			child, err := p.buildRawNode(ctx, childPtr, infoPtr)
			if err != nil {
				return nil, err
			}
			children = append(children, child)
		}
	}

	return &RawNode{
		Kind:      kind,
		KindID:    info.Symbol,
		StartByte: info.StartByte,
		EndByte:   info.EndByte,
		IsNamed:   info.has(wasmNodeFlagNamed),
		IsError:   info.has(wasmNodeFlagError),
		IsMissing: info.has(wasmNodeFlagMissing),
		IsExtra:   info.has(wasmNodeFlagExtra),
		HasError:  info.has(wasmNodeFlagHasError),
		Children:  children,
	}, nil
}

func (p *Parser) nodeKind(ctx context.Context, nodePtr uint64, symbol uint16) (string, error) {
	if kind, ok := lookupNodeKind(symbol); ok {
		return kind, nil
	}

	ptr, err := p.callU64(ctx, p.nodeType, nodePtr)
	if err != nil {
		return "", err
	}
	kind, err := p.readCString(ctx, ptr)
	if err != nil {
		return "", err
	}
	rememberNodeKind(symbol, kind)
	return kind, nil
}

func (p *Parser) inspectNode(ctx context.Context, nodePtr uint64, infoPtr uint64) (nodeInfo, error) {
	if _, err := p.nodeInspect.Call(ctx, nodePtr, infoPtr); err != nil {
		return nodeInfo{}, fmt.Errorf("inspect node: %w", err)
	}

	infoPtr32, err := uint32FromU64(infoPtr)
	if err != nil {
		return nodeInfo{}, err
	}
	mem := p.module.Memory()
	if mem == nil {
		return nodeInfo{}, errors.New("missing wasm memory")
	}
	buf, ok := mem.Read(infoPtr32, wasmNodeInfoSize)
	if !ok {
		return nodeInfo{}, fmt.Errorf("read node info: ptr=%d size=%d", infoPtr, wasmNodeInfoSize)
	}

	symbol32 := binary.LittleEndian.Uint32(buf[0:4])
	symbol, ok := uint16FromU32(symbol32)
	if !ok {
		return nodeInfo{}, fmt.Errorf("node symbol out of range: %d", symbol32)
	}

	return nodeInfo{
		Symbol:     symbol,
		StartByte:  int(binary.LittleEndian.Uint32(buf[4:8])),
		EndByte:    int(binary.LittleEndian.Uint32(buf[8:12])),
		ChildCount: binary.LittleEndian.Uint32(buf[12:16]),
		Flags:      binary.LittleEndian.Uint32(buf[16:20]),
	}, nil
}

func (p *Parser) allocNode(ctx context.Context) (uint64, error) {
	ptr, err := p.callU64(ctx, p.malloc, wasmNodeSize)
	if err != nil {
		return 0, fmt.Errorf("alloc node: %w", err)
	}
	return ptr, nil
}

func (p *Parser) allocInputEdit(ctx context.Context, edit InputEdit) (uint64, error) {
	startByte, err := uint32FromInt(edit.StartByte)
	if err != nil {
		return 0, err
	}
	oldEndByte, err := uint32FromInt(edit.OldEndByte)
	if err != nil {
		return 0, err
	}
	newEndByte, err := uint32FromInt(edit.NewEndByte)
	if err != nil {
		return 0, err
	}
	startRow, err := uint32FromInt(edit.StartPoint.Row)
	if err != nil {
		return 0, err
	}
	startCol, err := uint32FromInt(edit.StartPoint.Column)
	if err != nil {
		return 0, err
	}
	oldEndRow, err := uint32FromInt(edit.OldEndPoint.Row)
	if err != nil {
		return 0, err
	}
	oldEndCol, err := uint32FromInt(edit.OldEndPoint.Column)
	if err != nil {
		return 0, err
	}
	newEndRow, err := uint32FromInt(edit.NewEndPoint.Row)
	if err != nil {
		return 0, err
	}
	newEndCol, err := uint32FromInt(edit.NewEndPoint.Column)
	if err != nil {
		return 0, err
	}

	ptr, err := p.callU64(ctx, p.malloc, wasmInputEditSize)
	if err != nil {
		return 0, fmt.Errorf("alloc input edit: %w", err)
	}
	ptr32, err := uint32FromU64(ptr)
	if err != nil {
		p.freePtr(ptr)
		return 0, err
	}

	var buf [wasmInputEditSize]byte
	binary.LittleEndian.PutUint32(buf[0:4], startByte)
	binary.LittleEndian.PutUint32(buf[4:8], oldEndByte)
	binary.LittleEndian.PutUint32(buf[8:12], newEndByte)
	binary.LittleEndian.PutUint32(buf[12:16], startRow)
	binary.LittleEndian.PutUint32(buf[16:20], startCol)
	binary.LittleEndian.PutUint32(buf[20:24], oldEndRow)
	binary.LittleEndian.PutUint32(buf[24:28], oldEndCol)
	binary.LittleEndian.PutUint32(buf[28:32], newEndRow)
	binary.LittleEndian.PutUint32(buf[32:36], newEndCol)

	mem := p.module.Memory()
	if mem == nil {
		p.freePtr(ptr)
		return 0, errors.New("missing wasm memory")
	}
	if !mem.Write(ptr32, buf[:]) {
		p.freePtr(ptr)
		return 0, errors.New("write input edit into wasm memory")
	}
	return ptr, nil
}

func (p *Parser) allocBytes(ctx context.Context, bytes []byte) (uint64, error) {
	size := uint64(len(bytes))
	if size == 0 {
		size = 1
	}
	ptr, err := p.callU64(ctx, p.malloc, size)
	if err != nil {
		return 0, fmt.Errorf("alloc source bytes: %w", err)
	}
	ptr32, err := uint32FromU64(ptr)
	if err != nil {
		p.freePtr(ptr)
		return 0, err
	}
	mem := p.module.Memory()
	if mem == nil {
		p.freePtr(ptr)
		return 0, errors.New("missing wasm memory")
	}
	if len(bytes) > 0 && !mem.Write(ptr32, bytes) {
		p.freePtr(ptr)
		return 0, errors.New("write source bytes into wasm memory")
	}
	return ptr, nil
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

func uint32FromInt(v int) (uint32, error) {
	if v < 0 || uint64(v) > math.MaxUint32 {
		return 0, fmt.Errorf("value overflows uint32: %d", v)
	}
	return uint32(v), nil
}
