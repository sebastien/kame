package cli

import (
	"kame/core"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Outcome struct {
	Scope      int64
	Node       int64
	Generation int64
	Kind       core.ResourceKind
	Name       string
	Started    bool
	Terminal   bool
	StartedNS  int64
	ElapsedMS  int64
	HasTiming  bool
	HostClock  bool
}

type ProgressWorkerState struct {
	Scope      int64
	Node       int64
	Target     string
	Program    string
	Request    int64
	Generation int64
	Attempt    int64
	Started    int64
	Ready      bool
	State      string
	Internal   bool
}

// ProgressState owns only presentation facts, with no terminal or clock access.
// All retained strings are copied through the invocation's allocator.
type ProgressState struct {
	Scope    int64
	Counts   Progress
	Outcomes []Outcome
	Workers  []ProgressWorkerState
}

func (s *ProgressState) FreeOutcomes(a mem.Allocator) {
	for i := range s.Outcomes {
		mem.FreeString(a, s.Outcomes[i].Name)
	}
	slices.Free(a, s.Outcomes)
	s.Outcomes = nil
	s.Counts = Progress{}
}

func (s *ProgressState) Free(a mem.Allocator) {
	s.FreeOutcomes(a)
	for i := range s.Workers {
		s.freeWorker(a, i)
	}
	slices.Free(a, s.Workers)
	s.Workers = nil
}

func (s *ProgressState) freeWorker(a mem.Allocator, i int) {
	mem.FreeString(a, s.Workers[i].Target)
	mem.FreeString(a, s.Workers[i].Program)
	s.Workers[i] = ProgressWorkerState{}
}

func (s *ProgressState) ObserveOutcome(a mem.Allocator, event program.Event, nowNS int64) {
	if event.Kind != program.TargetStarted && event.Kind != program.TargetCompleted && event.Kind != program.TargetFailed && event.Kind != program.TargetCancelled {
		return
	}
	index := -1
	for i := range s.Outcomes {
		if s.Outcomes[i].Scope == s.Scope && s.Outcomes[i].Node == event.NodeID && s.Outcomes[i].Generation == event.Generation && s.Outcomes[i].Kind == event.Key.Kind && s.Outcomes[i].Name == event.Key.Name {
			index = i
			break
		}
	}
	if index < 0 {
		s.Outcomes = slices.Append(a, s.Outcomes, Outcome{Scope: s.Scope, Node: event.NodeID, Generation: event.Generation, Kind: event.Key.Kind, Name: core.NewString(a, event.Key.Name).Text, Started: event.Kind == program.TargetStarted})
		index = len(s.Outcomes) - 1
		if event.Kind == program.TargetStarted {
			s.Outcomes[index].StartedNS = nowNS
			s.Outcomes[index].HostClock = event.HasMonotonic
			if event.HasMonotonic {
				s.Outcomes[index].StartedNS = event.MonotonicNS
			}
			s.Counts.Active++
			return
		}
	}
	if event.Kind == program.TargetStarted || s.Outcomes[index].Terminal {
		return
	}
	s.Outcomes[index].Terminal = true
	finished := nowNS
	if event.HasMonotonic {
		finished = event.MonotonicNS
	}
	if s.Outcomes[index].Started && s.Outcomes[index].HostClock == event.HasMonotonic && finished >= s.Outcomes[index].StartedNS {
		s.Outcomes[index].ElapsedMS = (finished - s.Outcomes[index].StartedNS) / 1000000
		s.Outcomes[index].HasTiming = true
	}
	if s.Outcomes[index].Started && s.Counts.Active != 0 {
		s.Counts.Active--
	}
	if event.Kind == program.TargetCompleted {
		s.Counts.Completed++
	}
	if event.Kind == program.TargetFailed {
		s.Counts.Failed++
	}
	if event.Kind == program.TargetCancelled {
		s.Counts.Cancelled++
	}
}

func (s *ProgressState) ObserveWorker(a mem.Allocator, event program.Event, nowNS int64) {
	if event.Kind == program.ProcessStarted && event.Target != "" {
		for i := range s.Outcomes {
			o := s.Outcomes[i]
			if o.Scope == s.Scope && o.Node == event.NodeID && (o.Generation > event.Generation || (o.Generation == event.Generation && o.Terminal)) {
				return
			}
		}
		for i := range s.Workers {
			w := s.Workers[i]
			if w.Target != "" && w.Scope == s.Scope && w.Node == event.NodeID {
				if w.Generation > event.Generation || (w.Generation == event.Generation && w.Attempt > event.Attempt) {
					return
				}
			}
			if w.Scope == s.Scope && w.Node == event.NodeID && w.Target == event.Target && w.Request == event.RequestID && w.Generation == event.Generation && w.Attempt == event.Attempt && w.Program == event.Program && !w.Internal {
				return
			}
		}
		for i := range s.Workers {
			w := s.Workers[i]
			if w.Target != "" && w.Scope == s.Scope && w.Node == event.NodeID && (w.Generation < event.Generation || (w.Generation == event.Generation && w.Attempt < event.Attempt)) {
				s.freeWorker(a, i)
			}
		}
		work := ProgressWorkerState{Scope: s.Scope, Node: event.NodeID, Target: core.NewString(a, event.Target).Text, Program: core.NewString(a, event.Program).Text, Request: event.RequestID, Generation: event.Generation, Attempt: event.Attempt, Started: nowNS}
		slot := -1
		for i := range s.Workers {
			if s.Workers[i].Target == "" {
				slot = i
				break
			}
		}
		if slot < 0 {
			s.Workers = slices.Append(a, s.Workers, work)
		} else {
			s.Workers[slot] = work
		}
	}
	for i := range s.Workers {
		w := &s.Workers[i]
		if w.Target == "" || w.Scope != s.Scope || w.Node != event.NodeID || w.Target != event.Target || w.Generation != event.Generation || event.Attempt < w.Attempt {
			continue
		}
		if event.Kind == program.ServiceState {
			w.Ready = event.State == "ready" || event.State == "checking-health"
			w.State = ""
			if event.State == "stopping" {
				w.State = "stopping"
			} else if event.State == "restarting" {
				w.State = "retrying"
			}
		}
		if (event.Kind == program.ProcessExited && (event.RequestID == 0 || w.Request == event.RequestID)) || event.Kind == program.TargetCompleted || event.Kind == program.TargetFailed || event.Kind == program.TargetCancelled {
			s.freeWorker(a, i)
		}
	}
}

func (s *ProgressState) ObserveWork(a mem.Allocator, node int64, generation int64, attempt int64, target string, started bool, nowNS int64) {
	if !started {
		for i := range s.Workers {
			w := s.Workers[i]
			if w.Internal && w.Scope == s.Scope && w.Node == node && w.Generation == generation && w.Attempt == attempt {
				s.freeWorker(a, i)
			}
		}
		return
	}
	for i := range s.Workers {
		if s.Workers[i].Target != "" && s.Workers[i].Scope == s.Scope && s.Workers[i].Node == node && s.Workers[i].Generation == generation {
			return
		}
	}
	s.ObserveWorker(a, program.Event{Kind: program.ProcessStarted, NodeID: node, Target: target, Program: "kame", Generation: generation, Attempt: attempt}, nowNS)
	for i := range s.Workers {
		if s.Workers[i].Target != "" && s.Workers[i].Scope == s.Scope && s.Workers[i].Node == node && s.Workers[i].Generation == generation {
			s.Workers[i].Internal, s.Workers[i].State = true, "evaluating"
		}
	}
}

func (s *ProgressState) Timing(event program.Event) Outcome {
	for i := range s.Outcomes {
		o := s.Outcomes[i]
		if o.Scope == s.Scope && o.Node == event.NodeID && o.Generation == event.Generation && o.Kind == event.Key.Kind && o.Name == event.Key.Name {
			return o
		}
	}
	return Outcome{}
}

func (s *ProgressState) WorkerView(a mem.Allocator, nowNS int64) []ProgressWorkerView {
	workers := slices.Make[ProgressWorkerView](a, len(s.Workers))
	for i := range s.Workers {
		w := s.Workers[i]
		elapsed := int64(0)
		if nowNS >= w.Started {
			elapsed = (nowNS - w.Started) / 1000000
		}
		workers[i] = ProgressWorkerView{Target: w.Target, Program: w.Program, State: w.State, Ready: w.Ready, ElapsedMS: elapsed}
	}
	return workers
}
