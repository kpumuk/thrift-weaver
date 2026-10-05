package main

import "testing"

func TestChangelogRequiredForExtensionUserVisibleChanges(t *testing.T) {
	required, reasons, err := changelogRequired(config{}, []string{
		"editors/vscode/src/extension.ts",
		"internal/lsp/server.go",
	})
	if err != nil {
		t.Fatalf("changelogRequired error: %v", err)
	}
	if !required {
		t.Fatal("expected changelog to be required")
	}
	if len(reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %d", len(reasons))
	}
}

func TestChangelogNotRequiredWhenChangelogChanged(t *testing.T) {
	required, _, err := changelogRequired(config{}, []string{
		"editors/vscode/src/extension.ts",
		"editors/vscode/CHANGELOG.md",
	})
	if err != nil {
		t.Fatalf("changelogRequired error: %v", err)
	}
	if required {
		t.Fatal("expected changelog requirement to be satisfied")
	}
}

func TestChangelogNotRequiredForIgnoredFiles(t *testing.T) {
	required, reasons, err := changelogRequired(config{}, []string{
		"editors/vscode/scripts/test-extension-smoke.mjs",
		"editors/vscode/package-lock.json",
		"internal/lsp/server_test.go",
	})
	if err != nil {
		t.Fatalf("changelogRequired error: %v", err)
	}
	if required {
		t.Fatalf("expected no changelog requirement, got reasons: %v", reasons)
	}
}

func TestManifestChangeRequiresChangelogForNonDependencyChange(t *testing.T) {
	before := []byte(`{"name":"x","engines":{"vscode":"^1.90.0"},"devDependencies":{"@types/vscode":"^1.90.0"}}`)
	after := []byte(`{"name":"x","engines":{"vscode":"^1.91.0"},"devDependencies":{"@types/vscode":"^1.91.0"}}`)
	required, err := manifestChangeRequiresChangelog(before, after)
	if err != nil {
		t.Fatalf("manifestChangeRequiresChangelog error: %v", err)
	}
	if !required {
		t.Fatal("expected changelog to be required for engine change")
	}
}

func TestManifestChangeDoesNotRequireChangelogForDependencyOnlyChange(t *testing.T) {
	before := []byte(`{"name":"x","engines":{"vscode":"^1.90.0"},"devDependencies":{"@types/vscode":"^1.90.0","esbuild":"^0.27.3"}}`)
	after := []byte(`{"name":"x","engines":{"vscode":"^1.90.0"},"devDependencies":{"@types/vscode":"^1.90.0","esbuild":"^0.27.4"}}`)
	required, err := manifestChangeRequiresChangelog(before, after)
	if err != nil {
		t.Fatalf("manifestChangeRequiresChangelog error: %v", err)
	}
	if required {
		t.Fatal("expected dependency-only change to skip changelog requirement")
	}
}

func TestManifestChangeDoesNotRequireChangelogForReleaseMetadataChange(t *testing.T) {
	before := []byte(`{"name":"x","version":"0.3.4","devDependencies":{"tsx":"^4.23.15"}}`)
	for _, tc := range []struct {
		name  string
		after string
	}{
		{
			name:  "version only",
			after: `{"name":"x","version":"0.3.5","devDependencies":{"tsx":"^4.23.15"}}`,
		},
		{
			name:  "version and dependency",
			after: `{"name":"x","version":"0.3.5","devDependencies":{"tsx":"^4.23.13"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			required, err := manifestChangeRequiresChangelog(before, []byte(tc.after))
			if err != nil {
				t.Fatalf("manifestChangeRequiresChangelog error: %v", err)
			}
			if required {
				t.Fatal("expected release metadata change to skip changelog requirement")
			}
		})
	}
}

func TestManifestChangeRequiresChangelogForUserVisibleChangeWithVersionBump(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
		after  string
	}{
		{
			name:   "engine",
			before: `{"version":"0.3.4","engines":{"vscode":"^1.90.0"}}`,
			after:  `{"version":"0.3.5","engines":{"vscode":"^1.91.0"}}`,
		},
		{
			name:   "command",
			before: `{"version":"0.3.4","contributes":{"commands":[{"command":"thrift.restart","title":"Restart"}]}}`,
			after:  `{"version":"0.3.5","contributes":{"commands":[{"command":"thrift.restart","title":"Restart Server"}]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			required, err := manifestChangeRequiresChangelog([]byte(tc.before), []byte(tc.after))
			if err != nil {
				t.Fatalf("manifestChangeRequiresChangelog error: %v", err)
			}
			if !required {
				t.Fatal("expected user-visible change to require a changelog despite version bump")
			}
		})
	}
}
