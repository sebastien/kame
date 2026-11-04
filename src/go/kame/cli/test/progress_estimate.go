package cli_test

import (
	"kame/cli"
	"kame/program"
	"solod.dev/so/testing"
)

func TestProgressEstimateUsesParallelWallTimeAndColdFallback(t *testing.T) {
	a := t.Allocator()
	state := cli.ProgressState{}
	defer state.Free(a)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetStarted, NodeID: 1}, 0)
	if state.Estimate(100000000).Known {
		t.Error("cold work has an ETA")
	}
	state.ObserveOutcome(a, program.Event{Kind: program.TargetEstimate, NodeID: 1, EstimateMS: 1000}, 0)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetStarted, NodeID: 2}, 0)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetEstimate, NodeID: 2, EstimateMS: 1000}, 0)
	estimate := state.Estimate(500000000)
	if !estimate.Known || estimate.RemainingMS != 500 || estimate.Percent != 50 {
		t.Error("parallel targets were summed instead of taking the longest remaining wall time")
	}
	if state.Estimate(1100000000).Known {
		t.Error("overrun timing still supplies an ETA")
	}
	state.ObserveOutcome(a, program.Event{Kind: program.TargetCompleted, NodeID: 1}, 1200000000)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetCompleted, NodeID: 2}, 1200000000)
	if state.Estimate(1200000000).Known {
		t.Error("finished work supplies a running ETA")
	}
}
