package lint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
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

func TestFieldNameUniqueRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct S {
  1: string name,
  2: string name,
  3: string ok,
}

service API {
  void ping(1: string left, 2: string left),
  void pong(1: string left, 2: string right),
}
`)

	diags, err := FieldNameUniqueRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 4 {
		t.Fatalf("diagnostic count=%d, want 4", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticFieldNameDuplicate {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
		if d.Severity != syntax.SeverityError {
			t.Fatalf("diagnostic severity=%v, want %v", d.Severity, syntax.SeverityError)
		}
	}
}

func TestUnknownTypeRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct Known {}
typedef Known Alias

const Missing BAD = 1

struct S {
  1: Missing one,
  2: list<Missing> many,
  3: map<string, Missing> dict,
  4: shared.Remote remote,
  5: Alias ok,
}

service API {
  Known ping(1: Missing req, 2: shared.Remote remote),
}
`)

	diags, err := UnknownTypeRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 5 {
		t.Fatalf("diagnostic count=%d, want 5", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticTypeUnknown {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
		if d.Severity != syntax.SeverityError {
			t.Fatalf("diagnostic severity=%v, want %v", d.Severity, syntax.SeverityError)
		}
	}
}

func TestTypedefUnknownBaseRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct Known {}

typedef Missing MissingAlias
typedef list<Missing> MissingList
typedef shared.Remote RemoteAlias
typedef Known KnownAlias
`)

	diags, err := TypedefUnknownBaseRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count=%d, want 2", len(diags))
	}
	for _, d := range diags {
		if d.Code != DiagnosticTypedefUnknownBase {
			t.Fatalf("unexpected diagnostic code: %+v", d)
		}
		if d.Severity != syntax.SeverityError {
			t.Fatalf("diagnostic severity=%v, want %v", d.Severity, syntax.SeverityError)
		}
	}
}

func TestServiceSemanticsRule(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
struct NotService {}
exception Boom {}
service Base {}

service API extends Missing {
  oneway i32 ping(1: string req) throws (1: Missing first, 2: NotService second, 3: Boom ok, 4: shared.Remote remote),
}

service Child extends NotService {}
service Remote extends shared.Base {}
`)

	diags, err := ServiceSemanticsRule{}.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(diags) != 6 {
		t.Fatalf("diagnostic count=%d, want 6", len(diags))
	}
	if !hasCode(diags, DiagnosticServiceExtendsUnknown) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceExtendsUnknown, diags)
	}
	if !hasCode(diags, DiagnosticServiceOnewayReturnNotVoid) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceOnewayReturnNotVoid, diags)
	}
	if !hasCode(diags, DiagnosticServiceOnewayHasThrows) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceOnewayHasThrows, diags)
	}
	if !hasCode(diags, DiagnosticServiceThrowsUnknown) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceThrowsUnknown, diags)
	}
	if !hasCode(diags, DiagnosticServiceThrowsNotException) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceThrowsNotException, diags)
	}
	if !hasCode(diags, DiagnosticServiceExtendsNotService) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceExtendsNotService, diags)
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
  1: string dup,
  1: string second,
  2: string dup,
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
	if len(diags) != 9 {
		t.Fatalf("diagnostic count=%d, want 9", len(diags))
	}
	if !hasCode(diags, DiagnosticFieldIDRequired) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldIDRequired, diags)
	}
	if !hasCode(diags, DiagnosticFieldIDDuplicate) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldIDDuplicate, diags)
	}
	if !hasCode(diags, DiagnosticFieldNameDuplicate) {
		t.Fatalf("missing %s in %+v", DiagnosticFieldNameDuplicate, diags)
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

func TestDefaultRunnerIncludesSemanticRules(t *testing.T) {
	t.Parallel()

	tree := mustParseTree(t, `
typedef Missing Alias

service API extends MissingService {
  oneway i32 ping(1: Missing req) throws (1: string msg),
}
`)

	runner := NewDefaultRunner()
	diags, err := runner.Run(context.Background(), tree)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCode(diags, DiagnosticTypedefUnknownBase) {
		t.Fatalf("missing %s in %+v", DiagnosticTypedefUnknownBase, diags)
	}
	if !hasCode(diags, DiagnosticTypeUnknown) {
		t.Fatalf("missing %s in %+v", DiagnosticTypeUnknown, diags)
	}
	if !hasCode(diags, DiagnosticServiceExtendsUnknown) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceExtendsUnknown, diags)
	}
	if !hasCode(diags, DiagnosticServiceOnewayReturnNotVoid) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceOnewayReturnNotVoid, diags)
	}
	if !hasCode(diags, DiagnosticServiceOnewayHasThrows) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceOnewayHasThrows, diags)
	}
	if !hasCode(diags, DiagnosticServiceThrowsNotException) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceThrowsNotException, diags)
	}
	for _, d := range diags {
		if d.Source != DiagnosticSource {
			t.Fatalf("diagnostic source=%q, want %q", d.Source, DiagnosticSource)
		}
	}
}

func TestRunnerRunWithWorkspaceSurfacesIncludeAndQualifiedReferenceDiagnostics(t *testing.T) {
	t.Parallel()

	runner := NewDefaultRunner()

	t.Run("missing include", func(t *testing.T) {
		t.Parallel()

		view := mustWorkspaceView(t, "missing_include", "main.thrift")
		diags, err := runner.RunWithWorkspace(context.Background(), view)
		if err != nil {
			t.Fatalf("RunWithWorkspace: %v", err)
		}
		if !hasCode(diags, DiagnosticIncludeTargetUnknown) {
			t.Fatalf("missing %s in %+v", DiagnosticIncludeTargetUnknown, diags)
		}
		if !hasCode(diags, DiagnosticQualifiedReferenceUnknown) {
			t.Fatalf("missing %s in %+v", DiagnosticQualifiedReferenceUnknown, diags)
		}
		for _, diag := range diags {
			if diag.Source != DiagnosticSourceWorkspace {
				t.Fatalf("diagnostic source=%q, want %q", diag.Source, DiagnosticSourceWorkspace)
			}
		}
	})

	t.Run("duplicate alias", func(t *testing.T) {
		t.Parallel()

		view := mustWorkspaceView(t, "duplicate_alias", "main.thrift")
		diags, err := runner.RunWithWorkspace(context.Background(), view)
		if err != nil {
			t.Fatalf("RunWithWorkspace: %v", err)
		}
		if !hasCode(diags, DiagnosticIncludeAliasCollision) {
			t.Fatalf("missing %s in %+v", DiagnosticIncludeAliasCollision, diags)
		}
		if !hasCode(diags, DiagnosticQualifiedReferenceAmbiguous) {
			t.Fatalf("missing %s in %+v", DiagnosticQualifiedReferenceAmbiguous, diags)
		}
	})
}

func TestRunnerRunWithWorkspaceValidatesQualifiedServiceSemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "types.thrift"), "struct NotService {}\nstruct NotException {}\n")
	writeFile(t, filepath.Join(root, "main.thrift"), "include \"types.thrift\"\n\nservice API extends types.NotService {\n  void ping() throws (1: types.NotException boom)\n}\n")

	view := mustWorkspaceViewAtPath(t, root, "main.thrift")
	diags, err := NewDefaultRunner().RunWithWorkspace(context.Background(), view)
	if err != nil {
		t.Fatalf("RunWithWorkspace: %v", err)
	}
	if !hasCode(diags, DiagnosticServiceExtendsNotService) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceExtendsNotService, diags)
	}
	if !hasCode(diags, DiagnosticServiceThrowsNotException) {
		t.Fatalf("missing %s in %+v", DiagnosticServiceThrowsNotException, diags)
	}
}

func TestRunnerRunWithWorkspaceResolvedNavigationHasNoWorkspaceDiagnostics(t *testing.T) {
	t.Parallel()

	view := mustWorkspaceView(t, "navigation", "main.thrift")
	diags, err := NewDefaultRunner().RunWithWorkspace(context.Background(), view)
	if err != nil {
		t.Fatalf("RunWithWorkspace: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diagnostic count=%d, want 0 (%+v)", len(diags), diags)
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

func mustWorkspaceView(t *testing.T, fixtureName, relativePath string) *index.DocumentView {
	t.Helper()
	root := testutil.CopyWorkspaceFixture(t, fixtureName)
	return mustWorkspaceViewAtPath(t, root, relativePath)
}

func mustWorkspaceViewAtPath(t *testing.T, root, relativePath string) *index.DocumentView {
	t.Helper()
	manager := index.NewManager(index.Options{WorkspaceRoots: []string{root}})
	t.Cleanup(manager.Close)
	if err := manager.RescanWorkspace(context.Background()); err != nil {
		t.Fatalf("RescanWorkspace: %v", err)
	}
	snapshot, ok := manager.Snapshot()
	if !ok {
		t.Fatal("expected workspace snapshot")
	}
	view, ok, err := index.ViewForDocument(snapshot, filepath.Join(root, relativePath))
	if err != nil {
		t.Fatalf("ViewForDocument: %v", err)
	}
	if !ok {
		t.Fatalf("missing workspace document for %s", relativePath)
	}
	return view
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
