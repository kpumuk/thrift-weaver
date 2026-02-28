package lint

import (
	"context"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

func TestFieldIDRequiredRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  string name,
  2: string title,
}

service API {
  void ping(string payload, 2: string id),
}
`)

	diags, err := FieldIDRequiredRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count=%d, want 2", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticFieldIDRequired {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
	}
}

func TestDeprecatedFieldModifiersRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  1: string a xsd_optional,
  2: string b xsd_nillable,
  3: string c xsd_attrs { 1: string include },
}
`)

	diags, err := DeprecatedFieldModifiersRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 3 {
		t.Fatalf("diagnostic count=%d, want 3", len(diags))
	}
	if !hasCode(diags, DiagnosticDeprecatedFieldXSDOptional) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedFieldXSDOptional, diags)
	}
	if !hasCode(diags, DiagnosticDeprecatedFieldXSDNillable) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedFieldXSDNillable, diags)
	}
	if !hasCode(diags, DiagnosticDeprecatedFieldXSDAttrs) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedFieldXSDAttrs, diags)
	}
}

func TestDefaultRunnerIncludesSourceAndAggregatesRules(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  string name xsd_optional,
}
`)

	runner := NewDefaultRunner()
	diags, err := runner.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count=%d, want 2", len(diags))
	}
	if !hasCode(diags, DiagnosticFieldIDRequired) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldIDRequired, diags)
	}
	if !hasCode(diags, DiagnosticDeprecatedFieldXSDOptional) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedFieldXSDOptional, diags)
	}
	for _, d := range diags {
		if d.Source != DiagnosticSource {
			t.Fatalf("diagnostic source=%q, want %q", d.Source, DiagnosticSource)
		}
	}
}

func mustParseTree(t *testing.T, src string) *syntax.Tree {
	t.Helper()

	tree, err := syntax.Parse(context.Background(), []byte(src), syntax.ParseOptions{URI: "file:///lint.thrift", Version: 1})
	if err != nil {
		t.Fatalf("syntax.Parse: %v", err)
	}
	return tree
}

func hasCode(diags []syntax.Diagnostic, code syntax.DiagnosticCode) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
