package program

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// One timestamp snapshot serves every newer-input selector in a rule render.
type newerInputState struct {
	Paths   []string
	Names   []string
	Outputs int
	Ready   bool
	Values  []core.Value
}

func (p *Program) freeNewerInputs(state *newerInputState) {
	if state == nil {
		return
	}
	freeStrings(p.Alloc, state.Paths)
	freeStrings(p.Alloc, state.Names)
	freeValues(p.Alloc, state.Values)
	mem.Free(p.Alloc, state)
}

func (p *Program) prepareNewerInputs(c *core.EngineContext, index int, names []string, resources []PlanInput) core.ProducerResult {
	entry := &p.Instances[index]
	state := entry.NewerInputs
	if state != nil {
		if state.Ready {
			return core.ProducerCompleted
		}
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		if completion.Diagnostic.Code != "" {
			completion.Value.Free(p.Alloc)
			p.failRule(c, index, completion.Diagnostic)
			return core.ProducerFailed
		}
		ok := p.selectNewerInputs(state, completion.Value)
		completion.Value.Free(p.Alloc)
		if !ok {
			p.failRule(c, index, failure(p.Alloc, "HOST_FAIL", "invalid newer-input timestamp response"))
			return core.ProducerFailed
		}
		return core.ProducerCompleted
	}
	state = mem.Alloc[newerInputState](p.Alloc)
	state.Outputs = len(entry.Plan.Outputs)
	for i := range entry.Plan.Outputs {
		state.Paths = slices.Append(p.Alloc, state.Paths, p.canonicalTarget(entry.Plan.Outputs[i], true))
	}
	for i := range names {
		if (i < len(resources) && resources[i].OrderOnly) || !isFileName(names[i]) {
			continue
		}
		canonical := p.canonicalTarget(names[i], true)
		if slices.Contains(state.Paths[state.Outputs:], canonical) {
			mem.FreeString(p.Alloc, canonical)
			continue
		}
		state.Paths = slices.Append(p.Alloc, state.Paths, canonical)
		state.Names = slices.Append(p.Alloc, state.Names, cloneText(p.Alloc, names[i]))
	}
	entry.NewerInputs = state
	if p.Forwarding {
		p.submitFileTimes(c, state.Paths)
		return core.ProducerSubmitted
	}
	var times []core.Value
	for i := range state.Paths {
		stat := p.Host.Stat(state.Paths[i])
		value := core.Value{Kind: core.Nil}
		if stat.Exists {
			value.Kind, value.Int = core.Int, stat.Info.ModTime
		}
		times = slices.Append(p.Alloc, times, value)
	}
	value := core.NewList(p.Alloc, times)
	slices.Free(p.Alloc, times)
	ok := p.selectNewerInputs(state, value)
	value.Free(p.Alloc)
	if !ok {
		p.failRule(c, index, failure(p.Alloc, "HOST_FAIL", "cannot inspect newer inputs"))
		return core.ProducerFailed
	}
	return core.ProducerCompleted
}

func (p *Program) selectNewerInputs(state *newerInputState, times core.Value) bool {
	if times.Kind != core.List || len(times.List) != len(state.Paths) {
		return false
	}
	missing := false
	var oldest int64
	for i := 0; i < state.Outputs; i++ {
		var timestamp int64
		if times.List[i].Kind == core.Nil {
			missing = true
			continue
		}
		if !contextTime(times.List[i], &timestamp) {
			return false
		}
		if i == 0 || timestamp < oldest {
			oldest = timestamp
		}
	}
	for i := range state.Names {
		var timestamp int64
		if !contextTime(times.List[state.Outputs+i], &timestamp) {
			return false
		}
		if missing || timestamp > oldest {
			state.Values = slices.Append(p.Alloc, state.Values, core.NewString(p.Alloc, state.Names[i]))
		}
	}
	state.Ready = true
	return true
}
