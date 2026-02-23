package syntax

import "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"

func kindName(kind NodeKind) string {
	lang := treesitter.Language()
	if lang == nil {
		return ""
	}
	return lang.NodeKindForId(uint16(kind))
}
