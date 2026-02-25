// generate-vscode-release-metadata emits release metadata for the packaged VS Code extension.
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
	"strings"
)

type extensionPackageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Engines struct {
		VSCode string `json:"vscode"`
	} `json:"engines"`
}

type releaseMetadata struct {
	SchemaVersion  int               `json:"schema_version"`
	Extension      extensionArtifact `json:"extension"`
	LanguageServer languageServerRef `json:"language_server"`
}

type extensionArtifact struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	EnginesVSCode string `json:"engines_vscode"`
	Filename      string `json:"filename"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size_bytes"`
}

type languageServerRef struct {
	ManifestSchemaVersion int    `json:"manifest_schema_version"`
	ManifestFilename      string `json:"manifest_filename"`
	VersionPolicy         string `json:"version_policy"`
	Version               string `json:"version"`
}

type config struct {
	VSIX        string
	Checksums   string
	PackageJSON string
	Output      string
	ToolVersion string
	ExtVersion  string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "generate-vscode-release-metadata: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}
	pkg, err := readPackageJSON(cfg.PackageJSON)
	if err != nil {
		return err
	}
	checksumMap, err := parseChecksums(cfg.Checksums)
	if err != nil {
		return err
	}
	vsixName := filepath.Base(cfg.VSIX)
	checksum, ok := checksumMap[vsixName]
	if !ok {
		return fmt.Errorf("missing checksum for %s", vsixName)
	}
	info, err := os.Stat(cfg.VSIX)
	if err != nil {
		return fmt.Errorf("stat vsix: %w", err)
	}
	actual, err := sha256File(cfg.VSIX)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, checksum) {
		return fmt.Errorf("checksum mismatch for %s", vsixName)
	}
	md := releaseMetadata{
		SchemaVersion: 1,
		Extension: extensionArtifact{
			Name:          pkg.Name,
			Version:       firstNonEmpty(cfg.ExtVersion, pkg.Version),
			EnginesVSCode: pkg.Engines.VSCode,
			Filename:      vsixName,
			SHA256:        strings.ToLower(checksum),
			Size:          info.Size(),
		},
		LanguageServer: languageServerRef{
			ManifestSchemaVersion: 1,
			ManifestFilename:      "thriftls-manifest.json",
			VersionPolicy:         "exact",
			Version:               cfg.ToolVersion,
		},
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
	return enc.Encode(md)
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.VSIX, "vsix", "", "Path to packaged VS Code extension .vsix")
	flag.StringVar(&cfg.Checksums, "checksums", "", "Path to aggregated checksums.txt")
	flag.StringVar(&cfg.PackageJSON, "package-json", "editors/vscode/package.json", "Path to extension package.json")
	flag.StringVar(&cfg.Output, "output", "dist/vscode-release-metadata.json", "Output metadata JSON path")
	flag.StringVar(&cfg.ToolVersion, "tool-version", "", "thriftls version referenced by extension metadata")
	flag.StringVar(&cfg.ExtVersion, "extension-version", "", "Override VS Code extension version (defaults to package.json version)")
	flag.Parse()
	if cfg.VSIX == "" || cfg.Checksums == "" || cfg.ToolVersion == "" {
		return config{}, errors.New("required flags: --vsix, --checksums, --tool-version")
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func readPackageJSON(path string) (extensionPackageJSON, error) {
	var pkg extensionPackageJSON
	b, err := os.ReadFile(path)
	if err != nil {
		return pkg, fmt.Errorf("read package.json: %w", err)
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return pkg, fmt.Errorf("parse package.json: %w", err)
	}
	return pkg, nil
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
		m[filepath.Base(parts[len(parts)-1])] = parts[0]
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("scan checksums: %w", err)
	}
	return m, nil
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
