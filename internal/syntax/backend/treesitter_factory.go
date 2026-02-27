package backend

import ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"

const treeSitterFactoryName = "treesitter-cgo"

type treeSitterFactory struct{}

// NewTreeSitterFactory returns the default parser backend factory.
func NewTreeSitterFactory() Factory {
	return treeSitterFactory{}
}

func (treeSitterFactory) Name() string {
	return treeSitterFactoryName
}

func (treeSitterFactory) NewParser() (Parser, error) {
	return ts.NewParser()
}
