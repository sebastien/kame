package cli

import (
	"kame/core"
	"kame/program"
	"solod.dev/so/mem"
)

type ProgressObservation struct {
	Event   program.Event
	Work    bool
	Started bool
	OK      bool
}

// ParseProgressObservation borrows the decoded event; observation copies only
// the fields it retains. Public event serialization remains unchanged.
func ParseProgressObservation(value core.Value) ProgressObservation {
	if value.Kind != core.Record {
		return ProgressObservation{}
	}
	for i := range value.Record {
		f := value.Record[i]
		switch f.Key {
		case "type", "target", "program", "state":
			if f.Value.Kind != core.String {
				return ProgressObservation{}
			}
		case "node", "generation", "attempt", "request":
			if f.Value.Kind != core.Int || f.Value.Int < 0 {
				return ProgressObservation{}
			}
		}
	}
	kind := program.EventKind(-1)
	work, started := false, false
	switch reportField(value, "type").Text {
	case "target-started":
		kind = program.TargetStarted
	case "target-completed":
		kind = program.TargetCompleted
	case "target-failed":
		kind = program.TargetFailed
	case "target-cancelled":
		kind = program.TargetCancelled
	case "process-started":
		kind = program.ProcessStarted
	case "process-exited":
		kind = program.ProcessExited
	case "service-state":
		kind = program.ServiceState
	case "work-started":
		work, started = true, true
	case "work-finished":
		work = true
	default:
		return ProgressObservation{}
	}
	key := core.ResourceKey{}
	resource := reportField(value, "resource")
	if resource.Kind != core.Nil {
		if resource.Kind != core.Record || reportField(resource, "name").Kind != core.String || reportField(resource, "kind").Kind != core.String {
			return ProgressObservation{}
		}
		key.Name = reportField(resource, "name").Text
		switch reportField(resource, "kind").Text {
		case "definition":
			key.Kind = core.ResourceDefinition
		case "target":
			key.Kind = core.ResourceTarget
		case "file":
			key.Kind = core.ResourceFile
		case "task":
			key.Kind = core.ResourceTask
		case "service":
			key.Kind = core.ResourceService
		case "glob":
			key.Kind = core.ResourceGlob
		case "environment":
			key.Kind = core.ResourceEnvironment
		case "tool":
			key.Kind = core.ResourceTool
		case "operation":
			key.Kind = core.ResourceOperation
		default:
			return ProgressObservation{}
		}
	}
	return ProgressObservation{OK: true, Work: work, Started: started, Event: program.Event{Kind: kind, NodeID: reportField(value, "node").Int, Generation: reportField(value, "generation").Int, Attempt: reportField(value, "attempt").Int, RequestID: reportField(value, "request").Int, Target: reportField(value, "target").Text, Program: reportField(value, "program").Text, State: reportField(value, "state").Text, Key: key}}
}

func (s *ProgressState) Observe(a mem.Allocator, observation ProgressObservation, nowNS int64) {
	if !observation.OK {
		return
	}
	e := observation.Event
	if observation.Work {
		s.ObserveWork(a, e.NodeID, e.Generation, e.Attempt, e.Target, observation.Started, nowNS)
	} else {
		s.ObserveOutcome(a, e, nowNS)
		s.ObserveWorker(a, e, nowNS)
	}
}
