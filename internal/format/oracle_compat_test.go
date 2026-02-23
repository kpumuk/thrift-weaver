package format

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kpumuk/thrift-weaver/internal/testutil"
)

func TestFormattedOutputParsesWithOfficialThriftOracle(t *testing.T) {
	t.Parallel()

	oracle := testutil.RequireThriftOracle(t)
	cases, err := testutil.FormatGoldenCases()
	if err != nil {
		t.Fatalf("FormatGoldenCases: %v", err)
	}

	var ran int
	for _, tc := range cases {
		input := testutil.ReadFile(t, tc.InputPath)
		if !oracleFixtureSupported(input) {
			continue
		}
		ran++

		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			res, err := Source(context.Background(), input, filepath.Base(tc.InputPath), Options{})
			if err != nil {
				t.Fatalf("Source: %v", err)
			}

			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, filepath.Base(tc.InputPath))
			if err := os.WriteFile(outPath, res.Output, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			if err := oracle.ValidateFile(context.Background(), outPath); err != nil {
				t.Fatalf("oracle validation failed: %v\noutput:\n%s", err, res.Output)
			}
		})
	}

	if ran == 0 {
		t.Fatal("oracle test did not run any fixture; corpus subset selection is too strict")
	}
}

func oracleFixtureSupported(src []byte) bool {
	s := string(src)
	if strings.Contains(s, "include ") || strings.Contains(s, "cpp_include ") {
		return false
	}
	// The oracle subset intentionally excludes fixtures that rely on newer syntax support
	// than the pinned compiler may provide (for example, `const uuid`).
	if strings.Contains(s, "const uuid ") {
		return false
	}
	return true
}
