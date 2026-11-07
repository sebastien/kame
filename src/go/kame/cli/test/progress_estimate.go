package cli_test

import (
	"kame/cli"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
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

func TestWorkerETAInDashboard(t *testing.T) {
	a := t.Allocator()
	state := cli.ProgressState{}
	defer state.Free(a)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetStarted, NodeID: 1, Target: "build"}, 0)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetEstimate, NodeID: 1, EstimateMS: 3000}, 0)
	state.ObserveWorker(a, program.Event{Kind: program.ProcessStarted, NodeID: 1, Target: "build", Program: "cc"}, 0)
	workers := state.WorkerView(a, 1000000000)
	var out = bytes.NewBuffer(a, nil)
	cli.WriteDashboard(&out, cli.ProgressView{Command: "build", Width: 80, Height: 12, Workers: workers})
	if workers[0].RemainingMS != 2000 || !strings.Contains(out.String(), "ETA ~2.0s") {
		t.Error("dashboard did not show the historical target ETA")
	}
	out.Free()
	slices.Free(a, workers)
	workers = state.WorkerView(a, 4000000000)
	if workers[0].RemainingMS != 0 {
		t.Error("overrun target still has an ETA")
	}
	slices.Free(a, workers)
}
