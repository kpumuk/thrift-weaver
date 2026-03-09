package main

import "testing"

func TestEvaluatePolicyPassesWhenCurrentRunIsHealthy(t *testing.T) {
	fail, _ := evaluatePolicy(config{eventName: "push"})
	if fail {
		t.Fatal("expected pass")
	}
}

func TestEvaluatePolicyFailsImmediateBreachOnPullRequest(t *testing.T) {
	fail, message := evaluatePolicy(config{eventName: "pull_request", currentBreach: true})
	if !fail {
		t.Fatal("expected failure")
	}
	if message != "perf SLA breach on pull_request" {
		t.Fatalf("message=%q", message)
	}
}

func TestEvaluatePolicyAllowsFirstPushBreachWithoutHistory(t *testing.T) {
	fail, message := evaluatePolicy(config{eventName: "push", currentBreach: true})
	if fail {
		t.Fatal("expected pass")
	}
	if message == "" {
		t.Fatal("expected explanatory message")
	}
}

func TestEvaluatePolicyAllowsFirstConsecutivePushBreachWhenPreviousWasHealthy(t *testing.T) {
	fail, message := evaluatePolicy(config{
		eventName:      "push",
		currentBreach:  true,
		previousKnown:  true,
		previousBreach: false,
	})
	if fail {
		t.Fatal("expected pass")
	}
	if message != "perf SLA breach recorded on push; current run is the first consecutive branch-local breach" {
		t.Fatalf("message=%q", message)
	}
}

func TestEvaluatePolicyFailsRepeatedPushBreach(t *testing.T) {
	fail, message := evaluatePolicy(config{
		eventName:      "push",
		currentBreach:  true,
		previousKnown:  true,
		previousBreach: true,
	})
	if !fail {
		t.Fatal("expected failure")
	}
	if message != "repeated perf SLA breach on push" {
		t.Fatalf("message=%q", message)
	}
}
