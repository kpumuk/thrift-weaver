// Package main evaluates CI perf-gate policy from the current and previous breach states.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

type config struct {
	eventName      string
	currentBreach  bool
	previousKnown  bool
	previousBreach bool
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "perf-policy: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.eventName, "event-name", "", "GitHub event name (for example push or pull_request)")
	flag.BoolVar(&cfg.currentBreach, "current-breach", false, "whether the current perf gate breached the SLA")
	flag.BoolVar(&cfg.previousKnown, "previous-known", false, "whether a previous branch-local perf result is available")
	flag.BoolVar(&cfg.previousBreach, "previous-breach", false, "whether the previous branch-local perf result breached the SLA")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if cfg.eventName == "" {
		return errors.New("--event-name is required")
	}
	fail, message := evaluatePolicy(cfg)
	if message != "" {
		fmt.Println(message)
	}
	if fail {
		return errors.New(message)
	}
	return nil
}

func evaluatePolicy(cfg config) (bool, string) {
	if !cfg.currentBreach {
		return false, "perf policy passed: current run is within SLA"
	}

	if cfg.eventName != "push" {
		return true, "perf SLA breach on " + cfg.eventName
	}

	if cfg.previousKnown && cfg.previousBreach {
		return true, "repeated perf SLA breach on push"
	}

	if !cfg.previousKnown {
		return false, "perf SLA breach recorded on push; no previous branch-local result was available"
	}

	return false, "perf SLA breach recorded on push; current run is the first consecutive branch-local breach"
}
