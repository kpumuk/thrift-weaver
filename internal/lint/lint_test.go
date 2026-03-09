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

func TestFieldIDUniqueRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  1: string name,
  1: string title,
  2: string ok,
}

service API {
  void ping(1: string left, 1: string right),
  void pong(1: string left, 2: string right),
}
`)

	diags, err := FieldIDUniqueRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 4 {
		t.Fatalf("diagnostic count=%d, want 4", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticFieldIDDuplicate {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
		if d.Severity != syntax.SeverityError {
			t.Fatalf("diagnostic severity=%v, want %v", d.Severity, syntax.SeverityError)
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

func TestDeprecatedXSDAllRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct Legacy xsd_all {
  1: string value,
}

union Choice xsd_all {
  1: string name,
}
`)

	diags, err := DeprecatedXSDAllRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count=%d, want 2", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticDeprecatedXSDAll {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
	}
}

func TestUnionFieldRequirednessRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
union Choice {
  1: required string name,
  2: optional string title,
  3: string nickname,
}
`)

	diags, err := UnionFieldRequirednessRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostic count=%d, want 1", len(diags))
	}
	if diags[0].Code != DiagnosticUnionFieldRequired {
		t.Fatalf("unexpected diagnostic code: %+v", diags[0])
	}
}

func TestNegativeEnumValueRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
enum Result {
  OK = 1,
  BAD = -1,
}
`)

	diags, err := NegativeEnumValueRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostic count=%d, want 1", len(diags))
	}
	if diags[0].Code != DiagnosticNegativeEnumValue {
		t.Fatalf("unexpected diagnostic code: %+v", diags[0])
	}
}

func TestDefaultRunnerIncludesSourceAndAggregatesRules(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  string name xsd_optional,
  1: string first,
  1: string second,
}

union Choice xsd_all {
  1: required string value,
}

enum Result {
  BAD = -1,
}
`)

	runner := NewDefaultRunner()
	diags, err := runner.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 7 {
		t.Fatalf("diagnostic count=%d, want 7", len(diags))
	}
	if !hasCode(diags, DiagnosticFieldIDRequired) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldIDRequired, diags)
	}
	if !hasCode(diags, DiagnosticFieldIDDuplicate) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldIDDuplicate, diags)
	}
	if !hasCode(diags, DiagnosticDeprecatedFieldXSDOptional) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedFieldXSDOptional, diags)
	}
	if !hasCode(diags, DiagnosticDeprecatedXSDAll) {
		t.Fatalf("missing %s in %+v", DiagnosticDeprecatedXSDAll, diags)
	}
	if !hasCode(diags, DiagnosticUnionFieldRequired) {
		t.Fatalf("missing %s in %+v", DiagnosticUnionFieldRequired, diags)
	}
	if !hasCode(diags, DiagnosticNegativeEnumValue) {
		t.Fatalf("missing %s in %+v", DiagnosticNegativeEnumValue, diags)
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
