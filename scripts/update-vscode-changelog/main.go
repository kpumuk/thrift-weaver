// Package main rolls the VS Code changelog from Unreleased into a versioned release section.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type config struct {
	Path    string
	Version string
	Date    string
}

type section struct {
	Name string
	Date string
	Body string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "update-vscode-changelog: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(cfg.Path)
	if err != nil {
		return fmt.Errorf("read changelog: %w", err)
	}
	out, err := updateChangelog(string(src), cfg.Version, cfg.Date)
	if err != nil {
		return err
	}
	if bytes.Equal(src, []byte(out)) {
		return nil
	}
	return os.WriteFile(cfg.Path, []byte(out), 0o600)
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.Path, "path", "", "Path to CHANGELOG.md")
	flag.StringVar(&cfg.Version, "version", "", "Release version heading, for example v0.1.2")
	flag.StringVar(&cfg.Date, "date", "", "Release date in YYYY-MM-DD")
	flag.Parse()
	if strings.TrimSpace(cfg.Path) == "" || strings.TrimSpace(cfg.Version) == "" || strings.TrimSpace(cfg.Date) == "" {
		return config{}, errors.New("required flags: --path, --version, --date")
	}
	return cfg, nil
}

func updateChangelog(src, version, date string) (string, error) {
	prefix, sections, err := parseSections(src)
	if err != nil {
		return "", err
	}
	if len(sections) == 0 || sections[0].Name != "Unreleased" {
		return "", errors.New("CHANGELOG must start with an [Unreleased] section")
	}

	var release *section
	var others []section
	for i := range sections {
		sec := sections[i]
		if sec.Name == version {
			clone := sec
			release = &clone
			continue
		}
		if sec.Name == "Unreleased" {
			continue
		}
		others = append(others, sec)
	}

	unreleased := normalizeBody(sections[0].Body)
	if release == nil {
		release = &section{Name: version, Date: date}
	} else {
		release.Date = date
	}

	releaseBody := normalizeBody(release.Body)
	switch {
	case unreleased != "" && releaseBody != "":
		release.Body = unreleased + "\n\n" + releaseBody
	case unreleased != "":
		release.Body = unreleased
	case releaseBody != "":
		release.Body = releaseBody
	default:
		release.Body = "- No extension-specific user-visible changes in this release."
	}

	newSections := make([]section, 0, 2+len(others))
	newSections = append(newSections,
		section{Name: "Unreleased", Body: ""},
		section{Name: release.Name, Date: release.Date, Body: release.Body},
	)
	newSections = append(newSections, others...)

	return renderChangelog(prefix, newSections), nil
}

func parseSections(src string) (string, []section, error) {
	lines := strings.Split(normalizeNewlines(src), "\n")
	lines = trimTrailingRefs(lines)

	var prefixLines []string
	var sections []section
	var current *section
	inSections := false
	for _, line := range lines {
		if name, date, ok := parseHeading(line); ok {
			if current != nil {
				current.Body = strings.TrimSpace(current.Body)
				sections = append(sections, *current)
			}
			current = &section{Name: name, Date: date}
			inSections = true
			continue
		}

		if !inSections {
			prefixLines = append(prefixLines, line)
			continue
		}
		if current == nil {
			return "", nil, errors.New("malformed changelog sections")
		}
		if current.Body == "" {
			current.Body = line
			continue
		}
		current.Body += "\n" + line
	}
	if current != nil {
		current.Body = strings.TrimSpace(current.Body)
		sections = append(sections, *current)
	}
	return strings.TrimRight(strings.Join(prefixLines, "\n"), "\n"), sections, nil
}

func trimTrailingRefs(lines []string) []string {
	end := len(lines)
	for end > 0 {
		line := strings.TrimSpace(lines[end-1])
		switch {
		case line == "":
			end--
		case strings.HasPrefix(line, "[") && strings.Contains(line, "]: "):
			end--
		default:
			return lines[:end]
		}
	}
	return lines[:end]
}

func parseHeading(line string) (name, date string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "## [") {
		return "", "", false
	}
	rest := strings.TrimPrefix(line, "## [")
	name, remainder, ok := strings.Cut(rest, "]")
	if !ok {
		return "", "", false
	}
	remainder = strings.TrimSpace(remainder)
	if trimmed, ok := strings.CutPrefix(remainder, "- "); ok {
		date = strings.TrimSpace(trimmed)
	}
	return name, date, true
}

func normalizeBody(body string) string {
	return strings.TrimSpace(body)
}

func renderChangelog(prefix string, sections []section) string {
	var b strings.Builder
	if prefix != "" {
		b.WriteString(strings.TrimRight(prefix, "\n"))
		b.WriteString("\n\n")
	}
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## [")
		b.WriteString(sec.Name)
		b.WriteString("]")
		if sec.Name != "Unreleased" && sec.Date != "" {
			b.WriteString(" - ")
			b.WriteString(sec.Date)
		}
		b.WriteString("\n")
		if body := strings.TrimSpace(sec.Body); body != "" {
			b.WriteString("\n")
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(renderLinks(sections))
	return b.String()
}

func renderLinks(sections []section) string {
	var versions []string
	for _, sec := range sections {
		if sec.Name == "Unreleased" {
			continue
		}
		versions = append(versions, sec.Name)
	}

	var b strings.Builder
	if len(versions) > 0 {
		fmt.Fprintf(&b, "[Unreleased]: https://github.com/kpumuk/thrift-weaver/compare/%s...HEAD\n", versions[0])
	} else {
		b.WriteString("[Unreleased]: https://github.com/kpumuk/thrift-weaver/compare/HEAD\n")
	}
	for i, version := range versions {
		if i+1 < len(versions) {
			fmt.Fprintf(&b, "[%s]: https://github.com/kpumuk/thrift-weaver/compare/%s...%s\n", version, versions[i+1], version)
			continue
		}
		fmt.Fprintf(&b, "[%s]: https://github.com/kpumuk/thrift-weaver/releases/tag/%s\n", version, version)
	}
	return strings.TrimRight(b.String(), "\n")
}

func normalizeNewlines(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	return strings.ReplaceAll(src, "\r", "\n")
}
