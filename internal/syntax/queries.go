package syntax

// ChildNodeIDs returns direct child node ids (excluding token child refs) in source order.
func (t *Tree) ChildNodeIDs(id NodeID) []NodeID {
	n := t.NodeByID(id)
	if n == nil {
		return nil
	}
	out := make([]NodeID, 0, len(n.Children))
	for _, child := range n.Children {
		if child.IsToken {
			continue
		}
		out = append(out, NodeID(child.Index))
	}
	return out
}

// TopLevelDeclarationIDs returns direct top-level declaration nodes.
func (t *Tree) TopLevelDeclarationIDs() []NodeID {
	if t == nil {
		return nil
	}
	return t.ChildNodeIDs(t.Root)
}

// MemberNodeIDs returns member declarations for container nodes like structs, enums, and services.
func (t *Tree) MemberNodeIDs(container NodeID) []NodeID {
	n := t.NodeByID(container)
	if n == nil {
		return nil
	}

	switch KindName(n.Kind) {
	case "struct_definition", "union_definition", "exception_definition":
		if body, ok := t.firstChildByKind(container, "field_block"); ok {
			return t.namedChildrenOnly(body)
		}
	case "service_definition":
		if body, ok := t.firstChildByKind(container, "function_block"); ok {
			return t.namedChildrenOnly(body)
		}
	case "enum_definition":
		if body, ok := t.firstChildByKind(container, "enum_block"); ok {
			return t.namedChildrenOnly(body)
		}
	case "senum_definition":
		return t.namedChildrenOnly(container)
	}

	return nil
}

func (t *Tree) firstChildByKind(parent NodeID, want string) (NodeID, bool) {
	for _, id := range t.ChildNodeIDs(parent) {
		n := t.NodeByID(id)
		if n != nil && KindName(n.Kind) == want {
			return id, true
		}
	}
	return NoNode, false
}

func (t *Tree) namedChildrenOnly(parent NodeID) []NodeID {
	children := t.ChildNodeIDs(parent)
	out := make([]NodeID, 0, len(children))
	for _, id := range children {
		n := t.NodeByID(id)
		if n == nil {
			continue
		}
		if !n.Flags.Has(NodeFlagNamed) {
			continue
		}
		out = append(out, id)
	}
	return out
}
