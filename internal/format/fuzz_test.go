package format

import (
	"context"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

func FuzzDocumentAndRange(f *testing.F) {
	addFormatSeeds(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		t.Helper()
		if len(src) > 512*1024 {
			t.Skip()
		}

		tree, err := syntax.Parse(context.Background(), src, syntax.ParseOptions{URI: "fuzz.thrift"})
		if err != nil {
			t.Fatalf("Parse error: %v", err)
		}

		_, err = Document(context.Background(), tree, Options{})
		if err != nil && !IsErrUnsafeToFormat(err) {
			t.Fatalf("Document unexpected error: %v", err)
		}

		if len(src) == 0 {
			return
		}
		r := fuzzSpan(src)
		_, err = Range(context.Background(), tree, r, Options{})
		if err != nil && !IsErrUnsafeToFormat(err) {
			t.Fatalf("Range unexpected error: %v", err)
		}
	})
}

func addFormatSeeds(f *testing.F) {
	f.Helper()

	for _, s := range [][]byte{
		nil,
		[]byte(""),
		[]byte("struct S {\n  1: string a\n}\n"),
		[]byte("service Demo {\n  void ping(1: i32 id)\n}\n"),
		[]byte("const string X = 'unterminated\n"), // unsafe refusal expected
		[]byte("/* unterminated block comment"),    // unsafe refusal expected
		[]byte("struct T {\n  1: string a\n\n# comment\n  2: string b\n}\n"),
		{0xff, 0xfe, 0xfd}, // invalid UTF-8 -> unsafe refusal expected
	} {
		f.Add(s)
	}

	if cases, err := testutil.FormatGoldenCases(); err == nil {
		for _, c := range cases {
			f.Add(testutil.ReadFile(f, c.InputPath))
		}
	}
}

func fuzzSpan(src []byte) text.Span {
	if len(src) == 0 {
		return text.Span{}
	}
	start := 0
	end := len(src)
	if len(src) >= 1 {
		start = int(src[0]) % len(src)
	}
	if len(src) >= 2 {
		end = int(src[1]) % (len(src) + 1)
	}
	if end < start {
		start, end = end, start
	}
	return text.Span{Start: text.ByteOffset(start), End: text.ByteOffset(end)}
}
