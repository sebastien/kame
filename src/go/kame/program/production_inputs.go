package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type productionObservation struct {
	Observation core.Observation
	Consumer    int
}

type productionValidationState struct {
	Program  *Program
	Node     *core.Node
	Root     *core.Root
	Inputs   []productionObservation
	Changed  []core.ResourceKey
	Unknown  []core.ResourceKey
	Suspect  []int
	Index    int
	Pending  bool
	Saving   bool
	Probing  bool
	Revision int64
}

func clearProductionValidation(a mem.Allocator, state *productionValidationState) {
	for i := range state.Inputs {
		state.Inputs[i].Observation.Key.Free(a)
	}
	slices.Free(a, state.Inputs)
	for i := range state.Changed {
		state.Changed[i].Free(a)
	}
	for i := range state.Unknown {
		state.Unknown[i].Free(a)
	}
	slices.Free(a, state.Changed)
	slices.Free(a, state.Unknown)
	slices.Free(a, state.Suspect)
	state.Inputs, state.Changed, state.Unknown, state.Suspect = nil, nil, nil, nil
	state.Index, state.Pending, state.Saving, state.Probing = 0, false, false, false
}

func freeProductionValidation(a mem.Allocator, value any) {
	state := value.(*productionValidationState)
	clearProductionValidation(a, state)
	mem.Free(a, state)
}

func (p *Program) validationState() *productionValidationState {
	if p.productionValidation == nil {
		return nil
	}
	return p.productionValidation.(*productionValidationState)
}

// ChangedProductionInputs is borrowed until the next production validation.
func (p *Program) ChangedProductionInputs() []core.ResourceKey {
	state := p.validationState()
	if state == nil || p.productionDirty {
		return nil
	}
	return state.Changed
}

// A new root or invalidation starts a new cycle: previously reported changes
// are no longer evidence for the work that is about to run.
func (p *Program) clearChangedProductionInputs() {
	slices.Free(p.Alloc, p.deferredTerminal)
	p.deferredTerminal = nil
	state := p.validationState()
	if state == nil || state.Root != nil || p.productionDirty {
		return
	}
	for i := range state.Changed {
		state.Changed[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, state.Changed)
	state.Changed = nil
}

func appendProductionKey(a mem.Allocator, keys []core.ResourceKey, key core.ResourceKey) []core.ResourceKey {
	for i := range keys {
		if keys[i].Kind == key.Kind && keys[i].Name == key.Name {
			return keys
		}
	}
	return slices.Append(a, keys, key.Clone(a))
}

func (p *Program) clearProductionChanges() {
	if p == nil {
		return
	}
	for i := range p.productionChanges {
		p.productionChanges[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, p.productionChanges)
	p.productionChanges = nil
}

// buildActive reports participating work that may still consume or produce.
func (p *Program) buildActive() bool {
	for i := range p.Instances {
		entry := &p.Instances[i]
		if !entry.Inspection && entry.Node.Interest > 0 && !taskTerminal(entry.Node.State) {
			return true
		}
	}
	return false
}

// Notifications are evidence only after a host has compared fingerprints.
// Keep that evidence even if a later edit restores the original bytes.
func (p *Program) NoteProductionInputChange(key core.ResourceKey) {
	if p != nil && key.Kind == core.ResourceFile && (p.productionDirty || p.buildActive()) {
		p.productionChanges = appendProductionKey(p.Alloc, p.productionChanges, key)
	}
}

func (p *Program) advanceProductionValidation() {
	state := p.validationState()
	if state != nil && state.Root != nil {
		if taskTerminal(state.Node.State) {
			p.Engine.Release(state.Root)
			state.Root = nil
		}
		return
	}
	if !p.productionDirty {
		if !p.buildActive() {
			p.clearProductionChanges()
		}
		return
	}
	for i := range p.Instances {
		entry := &p.Instances[i]
		if !entry.Inspection && entry.Node.Interest > 0 && entry.Rule.Kind != rule.ServiceRule && (!taskTerminal(entry.Node.State) || entry.Node.ValidationPending) {
			return
		}
	}
	if state == nil {
		state = mem.Alloc[productionValidationState](p.Alloc)
		state.Program = p
		node := p.Engine.AddOwned(core.ResourceKey{Kind: core.ResourceOperation, Name: "\x00production-input-validation"}, validateProductionInputs, state, freeProductionValidation)
		if node == nil {
			mem.Free(p.Alloc, state)
			p.productionDirty = false
			p.productionWarning("INPUT_PROOF_UNUSABLE", "could not schedule input validation")
			return
		}
		state.Node = node
		p.productionValidation = state
	} else {
		clearProductionValidation(p.Alloc, state)
		p.Engine.Invalidate(state.Node)
	}
	for i := range p.Instances {
		entry := &p.Instances[i]
		if entry.Inspection || entry.Node.Interest == 0 || entry.Node.State != core.NodeComplete || entry.Rule.Kind == rule.ServiceRule {
			continue
		}
		inputs := p.acceptedInputs(entry)
		for n := range inputs {
			if inputs[n].Key.Kind == core.ResourceFile || inputs[n].Key.Kind == core.ResourceGlob {
				state.Inputs = slices.Append(p.Alloc, state.Inputs, productionObservation{Observation: inputs[n], Consumer: i})
			} else {
				inputs[n].Key.Free(p.Alloc)
			}
		}
		slices.Free(p.Alloc, inputs)
	}
	state.Revision = p.productionRevision
	state.Root = p.Engine.RequestRoot(state.Node)
}

func observationOp(key core.ResourceKey, aspect core.ObservationAspect) string {
	if key.Kind == core.ResourceGlob {
		return host.OpWildcard
	}
	if aspect == core.ObservationExistence {
		return host.OpExists
	}
	if aspect == core.ObservationMetadata {
		return host.OpStat
	}
	if aspect == core.ObservationLinkMetadata {
		return host.OpLstat
	}
	if aspect == core.ObservationLinkTarget {
		return host.OpReadlink
	}
	return host.OpFileContent
}

func observationSignature(key core.ResourceKey, aspect core.ObservationAspect, value core.Value) core.Signature {
	if key.Kind == core.ResourceGlob {
		return core.ValueSignature(value)
	}
	if aspect == core.ObservationContent || aspect == core.ObservationValue {
		return contentObservation(value)
	}
	if aspect == core.ObservationMetadata || aspect == core.ObservationLinkMetadata {
		return core.MetadataSignature(value)
	}
	return core.ValueSignature(value)
}

// acceptProductionInput returns true when it submitted an asynchronous probe.
func acceptProductionInput(c *core.EngineContext, state *productionValidationState, completion core.Completion) bool {
	p := state.Program
	input := state.Inputs[state.Index]
	if state.Probing {
		state.Probing = false
		missing := completion.Diagnostic.Code == "" && completion.HasValue && completion.Value.Kind == core.Bool && !completion.Value.Bool
		completion.Value.Free(p.Alloc)
		completion.Diagnostic.Free(p.Alloc)
		if missing {
			state.Changed = appendProductionKey(p.Alloc, state.Changed, input.Observation.Key)
		} else {
			state.Unknown = appendProductionKey(p.Alloc, state.Unknown, input.Observation.Key)
		}
		if !slices.Contains(state.Suspect, input.Consumer) {
			state.Suspect = slices.Append(p.Alloc, state.Suspect, input.Consumer)
		}
		state.Index++
		return false
	}
	item := input.Observation
	signature := core.Signature{}
	if completion.Diagnostic.Code == "" && completion.HasValue {
		signature = observationSignature(item.Key, item.Aspect, completion.Value)
	}
	completion.Value.Free(p.Alloc)
	completion.Diagnostic.Free(p.Alloc)
	changed := false
	for i := range p.productionChanges {
		changed = changed || p.productionChanges[i].Name == item.Key.Name
	}
	unknown := signature.Mode == core.SignatureUnavailable || item.Signature.Mode == core.SignatureUnavailable
	if changed || (!unknown && !signature.Equal(item.Signature)) {
		state.Changed = appendProductionKey(p.Alloc, state.Changed, item.Key)
	} else if unknown {
		// A failed metadata probe cannot distinguish disappearance from an
		// unreadable path. Ask for existence before calling it unverifiable.
		if item.Aspect == core.ObservationMetadata || item.Aspect == core.ObservationLinkMetadata {
			state.Probing = true
			if p.Forwarding {
				state.Pending = true
				p.submitFileContextRequest(c, host.RequestReadFile, host.FilePayload(p.Alloc, host.OpExists, item.Key.Name))
				return true
			}
			acceptProductionInput(c, state, p.fileCompletion(host.Request{}, host.OpExists, item.Key.Name))
			return false
		}
		state.Unknown = appendProductionKey(p.Alloc, state.Unknown, item.Key)
	} else {
		state.Index++
		return false
	}
	if !slices.Contains(state.Suspect, input.Consumer) {
		state.Suspect = slices.Append(p.Alloc, state.Suspect, input.Consumer)
	}
	state.Index++
	return false
}

func validateProductionInputs(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*productionValidationState)
	p := state.Program
	if state.Pending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		state.Pending = false
		if state.Saving {
			if completion.Diagnostic.Code != "" {
				p.productionWarning("INPUT_PROOF_UNUSABLE", "could not invalidate a suspect build record; do not reuse this build's cache")
			}
			completion.Value.Free(p.Alloc)
			completion.Diagnostic.Free(p.Alloc)
			state.Index++
		} else if acceptProductionInput(c, state, completion) {
			return core.ProducerSubmitted
		}
	}
	if !state.Saving {
		for state.Index < len(state.Inputs) {
			item := state.Inputs[state.Index].Observation
			op := observationOp(item.Key, item.Aspect)
			if p.Forwarding {
				state.Pending = true
				p.submitFileContextRequest(c, host.RequestReadFile, host.FilePayload(p.Alloc, op, item.Key.Name))
				return core.ProducerSubmitted
			}
			if acceptProductionInput(c, state, p.fileCompletion(host.Request{}, op, item.Key.Name)) {
				return core.ProducerSubmitted
			}
		}
		// Propagate taint through normal edges, including definitions and grouped
		// artifact aliases, without treating an order-only prerequisite as data.
		var consumers []*core.Node
		for n := range state.Suspect {
			consumers = slices.Append(p.Alloc, consumers, p.Instances[state.Suspect[n]].Node)
		}
		for j := 0; j < len(consumers); j++ {
			for _, dependent := range consumers[j].Dependents {
				if slices.Contains(dependent.OrderOnly, consumers[j]) {
					continue
				}
				index := p.instanceIndex(dependent)
				if index >= 0 && !p.Instances[index].Inspection && !slices.Contains(state.Suspect, index) {
					state.Suspect = slices.Append(p.Alloc, state.Suspect, index)
				}
				if !slices.Contains(consumers, dependent) {
					consumers = slices.Append(p.Alloc, consumers, dependent)
				}
			}
		}
		slices.Free(p.Alloc, consumers)
		state.Saving, state.Index = true, 0
	}
	for state.Index < len(state.Suspect) {
		entry := &p.Instances[state.Suspect[state.Index]]
		entry.AcceptedRecord.Guard = core.Signature{}
		entry.Node.Signature = core.Signature{}
		if entry.Rule.Kind != rule.FileRule && entry.Rule.Kind != rule.CachedTaskRule {
			state.Index++
			continue
		}
		if p.Forwarding {
			key := entry.FileContextKey[:]
			if entry.Rule.Kind == rule.CachedTaskRule {
				key = p.cacheKey(entry)
			}
			payload := host.CacheGetPayload(p.Alloc, key)
			if entry.Rule.Kind == rule.CachedTaskRule {
				slices.Free(p.Alloc, key)
			}
			state.Pending = true
			p.submitFileContextRequest(c, host.RequestCacheDelete, payload)
			return core.ProducerSubmitted
		}
		name := p.fileContextPath(entry)
		if entry.Rule.Kind == rule.CachedTaskRule {
			mem.FreeString(p.Alloc, name)
			name = p.cachePath(entry)
		}
		info := p.Host.Stat(name)
		if info.Failed || (info.Exists && p.Host.Remove(name) != nil) {
			p.productionWarning("INPUT_PROOF_UNUSABLE", "could not invalidate a suspect build record; do not reuse this build's cache")
		}
		mem.FreeString(p.Alloc, name)
		state.Index++
	}
	if len(state.Changed) != 0 || len(state.Unknown) != 0 {
		message := strings.NewBuilder(p.Alloc)
		message.WriteString("inputs changed or could not be verified during this build; outputs may be inconsistent")
		for i := range state.Changed {
			p.appendProductionPath(&message, "\n  changed: ", state.Changed[i].Name)
		}
		for i := range state.Unknown {
			confirmed := false
			for j := range state.Changed {
				confirmed = confirmed || state.Unknown[i].Name == state.Changed[j].Name
			}
			if !confirmed {
				p.appendProductionPath(&message, "\n  unverifiable: ", state.Unknown[i].Name)
			}
		}
		if p.Options.Watch && len(state.Changed) != 0 {
			message.WriteString("\na rebuild is queued for changed inputs")
		} else {
			message.WriteString("\nrun the build again to ensure consistent outputs")
		}
		p.productionWarning("INPUT_UNSTABLE", message.String())
		message.Free()
	}
	p.productionDirty = state.Revision != p.productionRevision
	if !p.productionDirty {
		p.clearProductionChanges()
	}
	c.Publish(core.Value{Kind: core.Nil})
	return core.ProducerCompleted
}

func (p *Program) appendProductionPath(message *strings.Builder, prefix string, name string) {
	message.WriteString(prefix)
	display := p.relativePath(name)
	message.WriteString(display)
	mem.FreeString(p.Alloc, display)
}

func (p *Program) productionWarning(code string, message string) {
	p.emit(Event{Kind: InputWarning, Diagnostic: diagnostic.Diagnostic{Code: cloneText(p.Alloc, code), Severity: diagnostic.Warning, Message: cloneText(p.Alloc, message), Owned: true}})
}

func (p *Program) ReportProductionCancellation(keys []core.ResourceKey) {
	message := strings.NewBuilder(p.Alloc)
	message.WriteString("cancelling active build; restart queued for changed inputs:")
	for i := range keys {
		duplicate := false
		for j := 0; j < i; j++ {
			duplicate = duplicate || (keys[j].Kind == keys[i].Kind && keys[j].Name == keys[i].Name)
		}
		if !duplicate {
			p.appendProductionPath(&message, "\n  ", keys[i].Name)
		}
	}
	p.productionWarning("INPUT_CHANGE_CANCELLED", message.String())
	message.Free()
}
