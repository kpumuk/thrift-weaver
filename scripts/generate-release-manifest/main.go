// generate-release-manifest emits the managed-install manifest for thriftls release artifacts.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Tool          string         `json:"tool"`
	Version       string         `json:"version"`
	GeneratedAt   string         `json:"generated_at"`
	Source        manifestSource `json:"source"`
	Platforms     []platformItem `json:"platforms"`
}

type manifestSource struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Checksums  string `json:"checksums"`
}

type platformItem struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size_bytes"`
	Filename string `json:"filename"`
}

type config struct {
	Version      string
	Repo         string
	Tag          string
	ArtifactsDir string
	Checksums    string
	Output       string
	BaseURL      string
}

var thriftlsArchivePattern = regexp.MustCompile(`^thriftls_(.+)_(linux|darwin|windows)_(amd64|arm64)\.(tar\.gz|zip)$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "generate-release-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}

	checksums, err := parseChecksums(cfg.Checksums)
	if err != nil {
		return err
	}

	entries, err := collectPlatformItems(cfg, checksums)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("no thriftls artifacts found")
	}

	m := manifest{
		SchemaVersion: 1,
		Tool:          "thriftls",
		Version:       cfg.Version,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Source: manifestSource{
			Repository: cfg.Repo,
			Tag:        cfg.Tag,
			Checksums:  filepath.Base(cfg.Checksums),
		},
		Platforms: entries,
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Output), 0o750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	f, err := os.Create(cfg.Output)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.Version, "version", "", "Release version without leading v")
	flag.StringVar(&cfg.Repo, "repo", "", "GitHub repository (owner/name)")
	flag.StringVar(&cfg.Tag, "tag", "", "Git tag (e.g. v0.1.0)")
	flag.StringVar(&cfg.ArtifactsDir, "artifacts-dir", "dist/release-artifacts", "Directory containing release artifacts")
	flag.StringVar(&cfg.Checksums, "checksums", "", "Path to checksums.txt")
	flag.StringVar(&cfg.Output, "output", "dist/thriftls-manifest.json", "Output manifest path")
	flag.StringVar(&cfg.BaseURL, "base-url", "", "Base URL for artifact downloads (defaults to GitHub releases URL)")
	flag.Parse()

	if cfg.Version == "" || cfg.Repo == "" || cfg.Checksums == "" {
		return config{}, errors.New("required flags: --version, --repo, --checksums")
	}
	if cfg.Tag == "" {
		cfg.Tag = "v" + cfg.Version
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = fmt.Sprintf("https://github.com/%s/releases/download/%s", cfg.Repo, cfg.Tag)
	}
	return cfg, nil
}

func parseChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open checksums: %w", err)
	}
	defer func() { _ = f.Close() }()

	m := make(map[string]string)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid checksum line: %q", line)
		}
		name := filepath.Base(parts[len(parts)-1])
		m[name] = parts[0]
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("scan checksums: %w", err)
	}
	return m, nil
}

func collectPlatformItems(cfg config, checksums map[string]string) ([]platformItem, error) {
	entries, err := os.ReadDir(cfg.ArtifactsDir)
	if err != nil {
		return nil, fmt.Errorf("read artifacts dir: %w", err)
	}

	items := make([]platformItem, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		matches := thriftlsArchivePattern.FindStringSubmatch(name)
		if matches == nil {
			continue
		}
		if matches[1] != cfg.Version {
			continue
		}
		checksum, ok := checksums[name]
		if !ok {
			return nil, fmt.Errorf("missing checksum for %s", name)
		}
		path := filepath.Join(cfg.ArtifactsDir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat artifact %s: %w", name, err)
		}
		actualChecksum, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(actualChecksum, checksum) {
			return nil, fmt.Errorf("checksum mismatch for %s", name)
		}
		items = append(items, platformItem{
			OS:       matches[2],
			Arch:     matches[3],
			URL:      strings.TrimRight(cfg.BaseURL, "/") + "/" + name,
			SHA256:   strings.ToLower(checksum),
			Size:     info.Size(),
			Filename: name,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OS != items[j].OS {
			return items[i].OS < items[j].OS
		}
		return items[i].Arch < items[j].Arch
	})
	return items, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
