package main

import (
	"strings"
	"testing"
)

func TestUpdateChangelogMovesUnreleasedIntoVersion(t *testing.T) {
	src := `# Changelog

## [Unreleased]

### Added

- Added a managed-install retry.

## [v0.1.1] - 2026-03-12

### Fixed

- Corrected branding copy.

[Unreleased]: https://example.invalid
[v0.1.1]: https://example.invalid
`

	got, err := updateChangelog(src, "v0.1.2", "2026-03-13")
	if err != nil {
		t.Fatalf("updateChangelog error: %v", err)
	}
	if !strings.Contains(got, "## [v0.1.2] - 2026-03-13\n\n### Added\n\n- Added a managed-install retry.") {
		t.Fatalf("expected new release section, got:\n%s", got)
	}
	if strings.Contains(got, "## [Unreleased]\n\n### Added") {
		t.Fatalf("expected unreleased to be emptied, got:\n%s", got)
	}
	if !strings.Contains(got, "[Unreleased]: https://github.com/kpumuk/thrift-weaver/compare/v0.1.2...HEAD") {
		t.Fatalf("expected unreleased compare link, got:\n%s", got)
	}
}

func TestUpdateChangelogCreatesNoOpReleaseEntryWhenUnreleasedIsEmpty(t *testing.T) {
	src := `# Changelog

## [Unreleased]

## [v0.1.1] - 2026-03-12

### Fixed

- Corrected branding copy.
`

	got, err := updateChangelog(src, "v0.1.2", "2026-03-13")
	if err != nil {
		t.Fatalf("updateChangelog error: %v", err)
	}
	if !strings.Contains(got, "- No extension-specific user-visible changes in this release.") {
		t.Fatalf("expected no-op line, got:\n%s", got)
	}
}

func TestUpdateChangelogIsIdempotent(t *testing.T) {
	src := `# Changelog

## [Unreleased]

## [v0.1.2] - 2026-03-13

- No extension-specific user-visible changes in this release.
`

	first, err := updateChangelog(src, "v0.1.2", "2026-03-13")
	if err != nil {
		t.Fatalf("first update error: %v", err)
	}
	second, err := updateChangelog(first, "v0.1.2", "2026-03-13")
	if err != nil {
		t.Fatalf("second update error: %v", err)
	}
	if first != second {
		t.Fatalf("expected idempotent output\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
