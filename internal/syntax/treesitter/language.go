// Package treesitter wraps parser primitives for Thrift syntax parsing.
package treesitter

import "sync"

// NodeKindRegistry is a minimal node-kind registry populated from wasm nodes.
type NodeKindRegistry struct{}

var (
	kindRegistryMu sync.RWMutex
	idToKind       = map[uint16]string{}

	languageInstance = &NodeKindRegistry{}
)

// Language returns the Thrift language instance.
func Language() *NodeKindRegistry {
	return languageInstance
}

// NodeKindForID resolves a node kind name by id.
func (l *NodeKindRegistry) NodeKindForID(id uint16) string {
	kindRegistryMu.RLock()
	name := idToKind[id]
	kindRegistryMu.RUnlock()
	return name
}

func rememberNodeKind(id uint16, name string) {
	kindRegistryMu.Lock()
	defer kindRegistryMu.Unlock()
	idToKind[id] = name
}
