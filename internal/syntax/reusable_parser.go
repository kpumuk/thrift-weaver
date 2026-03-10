package syntax

import (
	"context"
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
)

// ReusableParser keeps one backend parser instance alive across repeated full parses.
//
// It is intended for sequential use by one workspace-index worker. It is not safe
// for concurrent use.
type ReusableParser struct {
	parser parserbackend.Parser
}

// NewReusableParser constructs a reusable full-parse wrapper.
func NewReusableParser() *ReusableParser {
	return &ReusableParser{}
}

// Close releases the current backend parser instance, if any.
func (p *ReusableParser) Close() {
	if p == nil || p.parser == nil {
		return
	}
	p.parser.Close()
	p.parser = nil
}

// Parse tokenizes and parses src into a CST-oriented syntax tree without
// attaching incremental runtime state to the returned tree.
func (p *ReusableParser) Parse(ctx context.Context, src []byte, opts ParseOptions) (*Tree, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lexRes := lexer.Lex(src)

	attempt, err := beginBackendAttempt()
	if err != nil {
		return buildDegradedTreeForParserFailureWithLexResult(src, opts, lexRes, err), nil
	}

	parser, err := p.ensureParser()
	if err != nil {
		completeBackendAttemptFailure(attempt, err)
		return buildDegradedTreeForParserFailureWithLexResult(src, opts, lexRes, fmt.Errorf("init parser: %w", err)), nil
	}

	out, rawTree, err := parseFullTreeWithParser(ctx, parser, src, opts, lexRes)
	if err != nil {
		p.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			completeBackendAttemptFailure(attempt, ctxErr)
			return nil, ctxErr
		}
		completeBackendAttemptFailure(attempt, err)
		return buildDegradedTreeForParserFailureWithLexResult(src, opts, lexRes, fmt.Errorf("parse source: %w", err)), nil
	}
	rawTree.Close()
	completeBackendAttemptSuccess(attempt)
	return out, nil
}

func (p *ReusableParser) ensureParser() (parserbackend.Parser, error) {
	if p != nil && p.parser != nil {
		return p.parser, nil
	}
	parser, err := currentParserFactory().NewParser()
	if err != nil {
		return nil, err
	}
	if p != nil {
		p.parser = parser
	}
	return parser, nil
}
