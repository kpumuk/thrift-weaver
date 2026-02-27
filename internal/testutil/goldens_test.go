package testutil

import (
	"os"
	"testing"
)

func TestFormatGoldenCasesDiscovered(t *testing.T) {
	cases, err := FormatGoldenCases()
	if err != nil {
		t.Fatalf("FormatGoldenCases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("expected at least one formatter golden case")
	}

	for _, c := range cases {
		if _, err := os.Stat(c.InputPath); err != nil {
			t.Fatalf("input fixture missing for %s: %v", c.Name, err)
		}
		if _, err := os.Stat(c.ExpectedPath); err != nil {
			t.Fatalf("expected fixture missing for %s: %v", c.Name, err)
		}
	}
}
