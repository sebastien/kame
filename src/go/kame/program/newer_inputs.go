package program

import (
	"kame/core"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// One accepted content snapshot serves every newer-input selector in a rule render.
type newerInputState struct {
	Paths   []string
	Names   []string
	Ready   bool
	Values  []core.Value
}

// Scan authored source so selectors reached through helpers share the snapshot.
func (p *Program) needsNewerInputs(entry *instance) bool {
	return entry.Rule.Kind == rule.FileRule && (strings.Contains(p.Parsed.Source.Text, "@<?") || strings.Contains(p.Parsed.Source.Text, "inputs/changed"))
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
		for i := range state.Paths {
			key := core.ResourceKey{Kind: core.ResourceFile, Name: state.Paths[i]}
			c.ObserveAspect(key, core.ObservationContent, p.dependencyContentSignature(key))
		}
		return core.ProducerCompleted
	}
	state = mem.Alloc[newerInputState](p.Alloc)
	for i := range names {
		if (i < len(resources) && resources[i].OrderOnly) || !isFileName(names[i]) {
			continue
		}
		canonical := p.canonicalTarget(names[i], true)
		if slices.Contains(state.Paths, canonical) {
			mem.FreeString(p.Alloc, canonical)
			continue
		}
		state.Paths = slices.Append(p.Alloc, state.Paths, canonical)
		state.Names = slices.Append(p.Alloc, state.Names, cloneText(p.Alloc, names[i]))
		key := core.ResourceKey{Kind: core.ResourceFile, Name: canonical}
		c.ObserveAspect(key, core.ObservationContent, p.dependencyContentSignature(key))
	}
	entry.NewerInputs = state
	var stored *core.SignatureRecord
	if entry.FileContext != nil && entry.FileContext.RecordReason == "" {
		stored = &entry.FileContext.Stored
	}
	p.selectNewerInputs(state, stored, entry.AcceptedRecord.Outputs, len(entry.Plan.Outputs))
	return core.ProducerCompleted
}

func (p *Program) selectNewerInputs(state *newerInputState, stored *core.SignatureRecord, outputs []core.Observation, outputCount int) {
	all := stored == nil || len(outputs) != outputCount
	for i := range outputs {
		if outputs[i].Signature.Mode == core.SignatureMissing {
			all = true
		}
	}
	for i := range state.Names {
		key := core.ResourceKey{Kind: core.ResourceFile, Name: state.Paths[i]}
		current := p.dependencyContentSignature(key)
		previous := core.Signature{}
		if stored != nil {
			for j := range stored.Inputs {
				item := stored.Inputs[j]
				if item.Key.Kind == key.Kind && item.Key.Name == key.Name && (item.Aspect == core.ObservationContent || item.Aspect == core.ObservationValue) {
					previous = item.Signature
					if item.Aspect == core.ObservationContent {
						break
					}
				}
			}
		}
		if all || !current.Equal(previous) {
			state.Values = slices.Append(p.Alloc, state.Values, core.NewString(p.Alloc, state.Names[i]))
		}
	}
	state.Ready = true
}
