package main

import "testing"

func TestChangelogRequiredForExtensionUserVisibleChanges(t *testing.T) {
	required, reasons := changelogRequired([]string{
		"editors/vscode/src/extension.ts",
		"internal/lsp/server.go",
	})
	if !required {
		t.Fatal("expected changelog to be required")
	}
	if len(reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %d", len(reasons))
	}
}

func TestChangelogNotRequiredWhenChangelogChanged(t *testing.T) {
	required, _ := changelogRequired([]string{
		"editors/vscode/src/extension.ts",
		"editors/vscode/CHANGELOG.md",
	})
	if required {
		t.Fatal("expected changelog requirement to be satisfied")
	}
}

func TestChangelogNotRequiredForIgnoredFiles(t *testing.T) {
	required, reasons := changelogRequired([]string{
		"editors/vscode/scripts/test-extension-smoke.mjs",
		"editors/vscode/package-lock.json",
		"internal/lsp/server_test.go",
	})
	if required {
		t.Fatalf("expected no changelog requirement, got reasons: %v", reasons)
	}
}
