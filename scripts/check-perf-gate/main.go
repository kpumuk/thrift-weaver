// Package main enforces the CI performance gate from a perf-report JSON artifact.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

const (
	setSmall   = "small"
	setTypical = "typical"
	setLarge   = "large"
)

type config struct {
	jsonPath     string
	parseP95Max  float64
	formatP95Max float64
}

type report struct {
	ParseBench  []benchSetReport `json:"parse_bench"`
	FormatBench []benchSetReport `json:"format_bench"`
	Memory      memoryReport     `json:"memory"`
}

type benchSetReport struct {
	Set     string      `json:"set"`
	Samples int         `json:"samples"`
	Stats   sampleStats `json:"stats"`
}

type sampleStats struct {
	P95MS float64 `json:"p95_ms"`
}

type memoryReport struct {
	UnboundedGrowthHint bool `json:"unbounded_growth_hint"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "perf-gate: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.jsonPath, "json", "", "path to perf-report JSON output")
	flag.Float64Var(&cfg.parseP95Max, "parse-p95-max", 50, "maximum allowed parse p95 for the typical corpus set, in milliseconds")
	flag.Float64Var(&cfg.formatP95Max, "format-p95-max", 100, "maximum allowed format p95 for the typical corpus set, in milliseconds")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if cfg.jsonPath == "" {
		return errors.New("--json is required")
	}

	rep, err := loadReport(cfg.jsonPath)
	if err != nil {
		return err
	}
	parseBenches, err := indexBenchSets("parse", rep.ParseBench)
	if err != nil {
		return err
	}
	parseTypical := parseBenches[setTypical]
	if parseTypical.Stats.P95MS > cfg.parseP95Max {
		return fmt.Errorf("parse typical p95 %.2fms exceeds %.2fms", parseTypical.Stats.P95MS, cfg.parseP95Max)
	}

	formatBenches, err := indexBenchSets("format", rep.FormatBench)
	if err != nil {
		return err
	}
	formatTypical := formatBenches[setTypical]
	if formatTypical.Stats.P95MS > cfg.formatP95Max {
		return fmt.Errorf("format typical p95 %.2fms exceeds %.2fms", formatTypical.Stats.P95MS, cfg.formatP95Max)
	}

	if rep.Memory.UnboundedGrowthHint {
		return errors.New("memory benchmark flagged unbounded growth")
	}

	fmt.Printf("perf gate passed: parse typical p95=%.2fms, format typical p95=%.2fms, memory stable\n", parseTypical.Stats.P95MS, formatTypical.Stats.P95MS)
	return nil
}

func loadReport(path string) (report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}

	var rep report
	if err := json.Unmarshal(data, &rep); err != nil {
		return report{}, err
	}
	return rep, nil
}

func indexBenchSets(name string, benches []benchSetReport) (map[string]benchSetReport, error) {
	indexed := make(map[string]benchSetReport, len(benches))
	for _, bench := range benches {
		indexed[bench.Set] = bench
	}

	for _, set := range []string{setSmall, setTypical, setLarge} {
		bench, ok := indexed[set]
		if !ok {
			return nil, fmt.Errorf("%s coverage: missing set %q", name, set)
		}
		if bench.Samples == 0 {
			return nil, fmt.Errorf("%s coverage: set %q has no samples", name, set)
		}
	}
	return indexed, nil
}
