package main

import "testing"

func TestValidateBenchCoverageRequiresSmallTypicalLarge(t *testing.T) {
	benches := []benchSetReport{
		{Set: setSmall, Samples: 1},
		{Set: setTypical, Samples: 2},
		{Set: setLarge, Samples: 3},
	}
	if _, err := indexBenchSets("parse", benches); err != nil {
		t.Fatalf("indexBenchSets: %v", err)
	}
}

func TestValidateBenchCoverageRejectsMissingSet(t *testing.T) {
	benches := []benchSetReport{{Set: setSmall, Samples: 1}, {Set: setTypical, Samples: 1}}
	if _, err := indexBenchSets("parse", benches); err == nil {
		t.Fatal("expected missing set error")
	}
}
