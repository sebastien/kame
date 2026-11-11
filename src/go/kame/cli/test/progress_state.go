package clicheck

import (
	"kame/cli"
	"kame/core"
	"kame/internal/testing"
	"kame/program"
)

func TestProgressLifecycleIdentityAndClock(t *testing.T) {
	a := t.Allocator()
	var state cli.ProgressState
	defer state.Free(a)
	e := program.Event{Kind: program.TargetStarted, NodeID: 7, Generation: 1, Key: core.ResourceKey{Kind: core.ResourceTask, Name: "root"}, Target: "root", HasMonotonic: true, MonotonicNS: 1000000000}
	state.ObserveOutcome(a, e, 0)
	state.ObserveOutcome(a, e, 50)
	if state.Counts.Active != 1 {
		t.Error("duplicate start changed active count")
	}
	e.Kind, e.MonotonicNS = program.TargetCompleted, 1123000000
	state.ObserveOutcome(a, e, 80)
	state.ObserveOutcome(a, e, 100)
	timing := state.Timing(e)
	if state.Counts.Active != 0 || state.Counts.Completed != 1 || !timing.HasTiming || timing.ElapsedMS != 123 {
		t.Error("outcome timing or deduplication changed")
	}
	state.Scope = 8
	e.Kind = program.TargetStarted
	state.ObserveOutcome(a, e, 0)
	e.Kind = program.TargetCancelled
	state.ObserveOutcome(a, e, 0)
	if state.Counts.Cancelled != 1 || state.Counts.Completed != 1 {
		t.Error("execution-instance identity was lost")
	}
	e.Generation, e.Kind, e.HasMonotonic = 2, program.TargetStarted, false
	state.ObserveOutcome(a, e, 1000)
	e.Kind, e.HasMonotonic = program.TargetFailed, true
	state.ObserveOutcome(a, e, 2000)
	if state.Timing(e).HasTiming {
		t.Error("mixed clock domains fabricated a duration")
	}
}

func TestProgressWorkersRetriesAndServices(t *testing.T) {
	a := t.Allocator()
	var state cli.ProgressState
	defer state.Free(a)
	e := program.Event{Kind: program.ProcessStarted, Target: "root", Program: "cc", NodeID: 3, Generation: 1, Attempt: 1, RequestID: 10}
	state.ObserveWorker(a, e, 100)
	state.ObserveWorker(a, e, 200)
	if len(state.Workers) != 1 || state.Workers[0].Started != 100 {
		t.Error("duplicate process start consumed a slot or reset timing")
	}
	e.RequestID = 11
	state.ObserveWorker(a, e, 300)
	e.Kind, e.RequestID = program.ProcessExited, 10
	state.ObserveWorker(a, e, 400)
	if state.Workers[0].Target != "" || state.Workers[1].Target != "root" {
		t.Error("request-specific exit removed another process")
	}
	e.Kind, e.State = program.ServiceState, "ready"
	state.ObserveWorker(a, e, 500)
	if !state.Workers[1].Ready {
		t.Error("ready service was not retained")
	}
	state.FreeOutcomes(a)
	if !state.Workers[1].Ready {
		t.Error("cycle reset removed a ready service")
	}
	e.Kind, e.Attempt, e.RequestID = program.ProcessStarted, 2, 12
	state.ObserveWorker(a, e, 600)
	e.Kind, e.Attempt, e.RequestID = program.ProcessExited, 1, 11
	state.ObserveWorker(a, e, 700)
	e.Kind = program.ProcessStarted
	state.ObserveWorker(a, e, 800)
	if state.Workers[0].Request != 12 || state.Workers[1].Target != "" {
		t.Error("late retry events replaced the current worker")
	}
	e.Kind, e.Attempt = program.TargetCancelled, 2
	state.Observe(a, cli.ProgressObservation{Event: e, OK: true}, 900)
	e.Kind = program.ProcessStarted
	state.ObserveWorker(a, e, 1000)
	if state.Workers[0].Target != "" || state.Counts.Cancelled != 1 {
		t.Error("terminal target acquired a late ghost worker")
	}
}

func TestProgressInternalDispatchIsNotAQueuedParent(t *testing.T) {
	a := t.Allocator()
	var state cli.ProgressState
	defer state.Free(a)
	state.ObserveOutcome(a, program.Event{Kind: program.TargetStarted, Target: "parent", NodeID: 1}, 0)
	if len(state.Workers) != 0 {
		t.Error("target interest was mistaken for running work")
	}
	state.ObserveWork(a, 2, 1, 1, "render", true, 1000000000)
	state.ObserveWork(a, 2, 1, 1, "render", true, 1100000000)
	if len(state.Workers) != 1 || !state.Workers[0].Internal || state.Workers[0].State != "evaluating" || state.Workers[0].Started != 1000000000 {
		t.Error("internal work identity or timing drifted")
	}
	state.ObserveWork(a, 2, 0, 1, "render", false, 1200000000)
	if state.Workers[0].Target == "" {
		t.Error("stale dispatch completion removed current work")
	}
	state.ObserveWork(a, 2, 1, 1, "render", false, 1300000000)
	if state.Workers[0].Target != "" {
		t.Error("completed dispatch retained a worker")
	}
}

func TestProgressWorkerNamespaces(t *testing.T) {
	a := t.Allocator()
	var state cli.ProgressState
	defer state.Free(a)
	e := program.Event{Kind: program.ProcessStarted, Target: "root", Program: "cc", NodeID: 3, Generation: 1, Attempt: 1, RequestID: 1}
	state.Scope = 1
	state.ObserveWorker(a, e, 0)
	state.Scope = 2
	state.ObserveWorker(a, e, 0)
	if len(state.Workers) != 2 {
		t.Error("workers from separate execution instances collided")
		return
	}
	e.Kind = program.ProcessExited
	state.ObserveWorker(a, e, 10)
	if state.Workers[0].Target != "root" || state.Workers[1].Target != "" {
		t.Error("an exit cleared a worker in another namespace")
	}
}
