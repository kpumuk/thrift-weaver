package format

import (
	"context"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

func TestSourceFormatsTopLevelDeclarationsAndMembers(t *testing.T) {
	t.Parallel()

	src := []byte(`include  "a.thrift";namespace go   foo.bar
typedef  map < string ,list < i32 > >  Alias
enum Color{RED=1, GREEN =2;}
struct Foo{1:required i32 id;2: optional string name(ann='x'),3: byte flag = 1;}
service S { async void ping(1:i32 id,2: string name) ;}
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected formatting to change source")
	}

	got := string(res.Output)
	want := strings.Join([]string{
		`include "a.thrift";`,
		``,
		`namespace go foo.bar`,
		``,
		`typedef map<string, list<i32>> Alias`,
		``,
		`enum Color {`,
		`  RED = 1,`,
		`  GREEN = 2;`,
		`}`,
		``,
		`struct Foo {`,
		`  1: required i32 id;`,
		`  2: optional string name(ann = 'x'),`,
		`  3: byte flag = 1;`,
		`}`,
		``,
		`service S {`,
		`  async void ping(1: i32 id, 2: string name);`,
		`}`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	if _, err := syntax.Parse(context.Background(), res.Output, syntax.ParseOptions{URI: "formatted.thrift"}); err != nil {
		t.Fatalf("formatted output failed to parse: %v", err)
	}
}

func TestSourcePreservesDeprecatedSpellingsSeparatorsAndLiteralLexemes(t *testing.T) {
	t.Parallel()

	src := []byte(`const uuid GEN_UUID='00000000-4444-CCCC-ffff-0123456789ab'
const uuid GEN_GUID = '{00112233-4455-6677-8899-aaBBccDDeeFF}'
const i32 HEX=0x0A
const map<string,list<i32>> DATA={ 'a' : [1,2,3,], 'b':[],}
struct Holder{-2: optional byte legacy = 1,2: optional map<string,list<i32>> data = {'x':[1,2],};}
service API{async void go(1:byte x,2:uuid id);}
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}

	out := string(res.Output)
	for _, want := range []string{
		"byte",
		"async void go",
		"'00000000-4444-CCCC-ffff-0123456789ab'",
		"'{00112233-4455-6677-8899-aaBBccDDeeFF}'",
		"0x0A",
		"[1, 2, 3,]",
		"{'x': [1, 2],}",
		"-2: optional byte legacy = 1,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted output missing %q\noutput:\n%s", want, out)
		}
	}

	// Deprecated spellings should remain intact and canonical aliases should not be introduced.
	if strings.Contains(out, "oneway") {
		t.Fatalf("unexpected spelling rewrite to oneway\noutput:\n%s", out)
	}
	if strings.Contains(out, "i8 ") {
		t.Fatalf("unexpected spelling rewrite to i8\noutput:\n%s", out)
	}
}

func TestSourceGroupsAdjacentIncludesAndNamespacesWithoutBlankLines(t *testing.T) {
	t.Parallel()

	src := []byte(`include "a.thrift"
cpp_include "b.h"
namespace go foo.bar
namespace rb foo.bar
typedef i32 ID
typedef string Name
struct X {}
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}

	got := string(res.Output)
	want := strings.Join([]string{
		`include "a.thrift"`,
		`cpp_include "b.h"`,
		``,
		`namespace go foo.bar`,
		`namespace rb foo.bar`,
		``,
		`typedef i32 ID`,
		`typedef string Name`,
		``,
		`struct X {}`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSourceServiceFunctionRespectsLineWidth(t *testing.T) {
	t.Parallel()

	src := []byte(`service UpsertIdentityConnection {
  UpsertIdentityConnectionResponse upsert_identity_connection(1: UpsertIdentityConnectionRequest request) throws(1: UpsertIdentityConnectionError error)
}
`)

	t.Run("fits", func(t *testing.T) {
		t.Parallel()

		res, err := Source(context.Background(), src, "test.thrift", Options{LineWidth: 200})
		if err != nil {
			t.Fatalf("Source: %v", err)
		}

		got := string(res.Output)
		want := strings.Join([]string{
			`service UpsertIdentityConnection {`,
			`  UpsertIdentityConnectionResponse upsert_identity_connection(1: UpsertIdentityConnectionRequest request) throws(1: UpsertIdentityConnectionError error)`,
			`}`,
			``,
		}, "\n")
		if got != want {
			t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
	})

	t.Run("wraps", func(t *testing.T) {
		t.Parallel()

		res, err := Source(context.Background(), src, "test.thrift", Options{LineWidth: 80})
		if err != nil {
			t.Fatalf("Source: %v", err)
		}

		got := string(res.Output)
		want := strings.Join([]string{
			`service UpsertIdentityConnection {`,
			`  UpsertIdentityConnectionResponse upsert_identity_connection(`,
			`    1: UpsertIdentityConnectionRequest request`,
			`  ) throws(`,
			`    1: UpsertIdentityConnectionError error`,
			`  )`,
			`}`,
			``,
		}, "\n")
		if got != want {
			t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
	})
}

func TestSourceServiceFunctionWithLeadingDocCommentStillWrapsByLineWidth(t *testing.T) {
	t.Parallel()

	src := []byte(`service Demo {
  /** docs */
  ColumnOrSuperColumn get(1: binary key, 2: ColumnPath column_path, 3: ConsistencyLevel consistency_level = ConsistencyLevel.ONE) throws(1: InvalidRequestException ire, 2: NotFoundException nfe, 3: UnavailableException ue, 4: TimedOutException te),
}
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{LineWidth: 100})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}

	got := string(res.Output)
	want := strings.Join([]string{
		`service Demo {`,
		`  /** docs */`,
		`  ColumnOrSuperColumn get(`,
		`    1: binary key,`,
		`    2: ColumnPath column_path,`,
		`    3: ConsistencyLevel consistency_level = ConsistencyLevel.ONE`,
		`  ) throws(`,
		`    1: InvalidRequestException ire,`,
		`    2: NotFoundException nfe,`,
		`    3: UnavailableException ue,`,
		`    4: TimedOutException te`,
		`  ),`,
		`}`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSourceDoesNotInsertExtraBlankLineBeforeFieldCommentBlock(t *testing.T) {
	t.Parallel()

	src := []byte(`struct Tenant {
    3: required string primary_value
    # Optional metadata captured during provisioning.
    # Persisted in hashed form after initial write.
    4: optional string secondary_value
}
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}

	got := string(res.Output)
	want := strings.Join([]string{
		`struct Tenant {`,
		`  3: required string primary_value`,
		`  # Optional metadata captured during provisioning.`,
		`  # Persisted in hashed form after initial write.`,
		`  4: optional string secondary_value`,
		`}`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSourceDoesNotInsertExtraBlankLineBeforeTopLevelCommentBlock(t *testing.T) {
	t.Parallel()

	src := []byte(`include "health_check_event.thrift"

# Buyer » Doormate
include "buyer_app/doormate/buyer_company.thrift"
`)

	res, err := Source(context.Background(), src, "test.thrift", Options{})
	if err != nil {
		t.Fatalf("Source: %v", err)
	}

	got := string(res.Output)
	want := strings.Join([]string{
		`include "health_check_event.thrift"`,
		``,
		`# Buyer » Doormate`,
		`include "buyer_app/doormate/buyer_company.thrift"`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
