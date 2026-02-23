package format

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestFormatterGoldenCorpus(t *testing.T) {
	t.Parallel()

	cases, err := testutil.FormatGoldenCases()
	if err != nil {
		t.Fatalf("FormatGoldenCases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("expected formatter golden fixtures")
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			input := testutil.ReadFile(t, tc.InputPath)
			expected := testutil.ReadFile(t, tc.ExpectedPath)

			res, err := Source(context.Background(), input, filepath.Base(tc.InputPath), Options{})
			if err != nil {
				t.Fatalf("Source: %v", err)
			}
			if string(res.Output) != string(expected) {
				t.Fatalf("formatted output mismatch\n--- got ---\n%s\n--- want ---\n%s", res.Output, expected)
			}

			// Idempotence: formatting an already formatted file is stable.
			res2, err := Source(context.Background(), res.Output, filepath.Base(tc.ExpectedPath), Options{})
			if err != nil {
				t.Fatalf("Source(idempotence): %v", err)
			}
			if string(res2.Output) != string(expected) {
				t.Fatalf("idempotence mismatch\n--- got ---\n%s\n--- want ---\n%s", res2.Output, expected)
			}
		})
	}
}

func TestCommentPreservationFixtures(t *testing.T) {
	t.Parallel()

	cases, err := testutil.FormatGoldenCases()
	if err != nil {
		t.Fatalf("FormatGoldenCases: %v", err)
	}

	for _, tc := range cases {
		if !strings.Contains(tc.Name, "comment") {
			continue
		}

		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			input := testutil.ReadFile(t, tc.InputPath)
			res, err := Source(context.Background(), input, filepath.Base(tc.InputPath), Options{})
			if err != nil {
				t.Fatalf("Source: %v", err)
			}

			commentLexemes := extractCommentLexemes(input)
			if len(commentLexemes) == 0 {
				t.Fatal("expected comments in comment preservation fixture")
			}

			out := string(res.Output)
			searchFrom := 0
			for _, c := range commentLexemes {
				pos := strings.Index(out[searchFrom:], c)
				if pos < 0 {
					t.Fatalf("formatted output missing comment %q\noutput:\n%s", c, out)
				}
				searchFrom += pos + len(c)
			}
		})
	}
}

func extractCommentLexemes(src []byte) []string {
	lexed := lexer.Lex(src)
	var out []string
	for _, tok := range lexed.Tokens {
		for _, tr := range tok.Leading {
			switch tr.Kind {
			case lexer.TriviaLineComment, lexer.TriviaHashComment, lexer.TriviaBlockComment, lexer.TriviaDocComment:
				raw := tr.Bytes(src)
				if raw != nil {
					out = append(out, string(raw))
				}
			case lexer.TriviaWhitespace, lexer.TriviaNewline:
				// Ignore non-comment trivia.
			default:
				// Ignore unknown trivia kinds conservatively.
			}
		}
	}
	return out
}
