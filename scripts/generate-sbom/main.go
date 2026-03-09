// Package main generates a CycloneDX SBOM for the current Go module graph.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type config struct {
	output  string
	version string
}

type goModule struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Main    bool      `json:"Main"`
	Replace *goModule `json:"Replace"`
}

type bom struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Serial      string      `json:"serialNumber"`
	Version     int         `json:"version"`
	Metadata    metadata    `json:"metadata"`
	Components  []component `json:"components,omitempty"`
}

type metadata struct {
	Timestamp string     `json:"timestamp,omitempty"`
	Component *component `json:"component,omitempty"`
}

type component struct {
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	PURL       string     `json:"purl,omitempty"`
	Properties []property `json:"properties,omitempty"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "generate-sbom: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.output, "output", "", "output path for CycloneDX JSON")
	flag.StringVar(&cfg.version, "version", "", "application version to record for the main module")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if cfg.output == "" {
		return errors.New("--output is required")
	}

	modules, err := listModules()
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return errors.New("go list returned no modules")
	}

	mainModule := modules[0]
	if !mainModule.Main {
		return errors.New("first module is not the main module")
	}

	version := cfg.version
	if version == "" {
		version = moduleVersion(mainModule)
	}

	root := component{
		Type:    "application",
		Name:    mainModule.Path,
		Version: version,
		PURL:    modulePURL(mainModule, version),
	}
	if replacement := mainModule.Replace; replacement != nil {
		root.Properties = append(root.Properties, property{Name: "thrift-weaver:replace", Value: replacement.Path})
	}

	components := make([]component, 0, len(modules)-1)
	for _, module := range modules[1:] {
		version := moduleVersion(module)
		component := component{
			Type:    "library",
			Name:    module.Path,
			Version: version,
			PURL:    modulePURL(module, version),
		}
		if replacement := module.Replace; replacement != nil {
			component.Properties = append(component.Properties,
				property{Name: "thrift-weaver:replace", Value: replacement.Path},
				property{Name: "thrift-weaver:replaceVersion", Value: moduleVersion(*replacement)},
			)
		}
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })

	out := bom{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Serial:      newSerialNumber(),
		Version:     1,
		Metadata: metadata{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Component: &root,
		},
		Components: components,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(cfg.output, data, 0o600)
}

func listModules() ([]goModule, error) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-m", "-json", "all")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(&stdout)
	var modules []goModule
	for {
		var module goModule
		if err := decoder.Decode(&module); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func moduleVersion(module goModule) string {
	if module.Replace != nil && module.Replace.Version != "" {
		return module.Replace.Version
	}
	if module.Version != "" {
		return module.Version
	}
	if module.Main {
		return "(devel)"
	}
	return "unknown"
}

func modulePURL(module goModule, version string) string {
	path := strings.TrimPrefix(module.Path, "https://")
	path = strings.TrimPrefix(path, "http://")
	return fmt.Sprintf("pkg:golang/%s@%s", path, version)
}

func newSerialNumber() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"urn:uuid:%08x-%04x-%04x-%04x-%012x",
		uint32(buf[0])<<24|uint32(buf[1])<<16|uint32(buf[2])<<8|uint32(buf[3]),
		uint16(buf[4])<<8|uint16(buf[5]),
		uint16(buf[6])<<8|uint16(buf[7]),
		uint16(buf[8])<<8|uint16(buf[9]),
		uint64(buf[10])<<40|uint64(buf[11])<<32|uint64(buf[12])<<24|uint64(buf[13])<<16|uint64(buf[14])<<8|uint64(buf[15]),
	)
}
