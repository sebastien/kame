package program

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/slices"
)

func (p *Program) clearReasons(entry *instance) {
	for i := range entry.Reasons {
		entry.Reasons[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, entry.Reasons)
	entry.Reasons = nil
}

// Reasons only explain an existing decision; never request more evidence.
func (p *Program) reason(entry *instance, decision string, code string, message string, key core.ResourceKey, aspect string) {
	for i := range entry.Reasons {
		previous := entry.Reasons[i]
		if previous.Decision == decision && previous.Reason == code && previous.DependencyKey.Kind == key.Kind && previous.DependencyKey.Name == key.Name && previous.Aspect == aspect {
			return
		}
	}
	node := entry.Node
	event := Event{Kind: TargetReason, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, Decision: decision, Reason: code, Message: message, DependencyKey: key, Aspect: aspect}
	p.emit(event)
	// Only the deduplication identity is retained after the event is drained.
	entry.Reasons = slices.Append(p.Alloc, entry.Reasons, Event{Decision: cloneText(p.Alloc, decision), Reason: cloneText(p.Alloc, code), DependencyKey: key.Clone(p.Alloc), Aspect: cloneText(p.Alloc, aspect)})
}

func reasonAspect(aspect core.ObservationAspect) string {
	if aspect == core.ObservationContent {
		return "content"
	}
	if aspect == core.ObservationExistence {
		return "existence"
	}
	if aspect == core.ObservationMetadata {
		return "metadata"
	}
	return "value"
}

// Compare only already-collected facts on a miss. A first established reason is
// enough; a source/implementation miss must not accuse obsolete branch inputs.
func (p *Program) recordReason(entry *instance, state *fileContextState, decision string) {
	if state.RecordReason != "" {
		message := "saved record missing"
		if state.RecordReason == "record-invalid" {
			message = "saved record invalid or incompatible"
		}
		if state.RecordReason == "proof-unverifiable" {
			message = "saved record unavailable"
		}
		p.reason(entry, decision, state.RecordReason, message, core.ResourceKey{}, "")
		return
	}
	if !state.Stored.Implementation.Equal(entry.AcceptedRecord.Implementation) {
		p.reason(entry, decision, "implementation-changed", "implementation changed", core.ResourceKey{}, "")
		return
	}
	if p.observationReason(entry, state.Stored.Inputs, entry.AcceptedRecord.Inputs, decision, false) {
		return
	}
	if p.observationReason(entry, state.Stored.Outputs, entry.AcceptedRecord.Outputs, decision, true) {
		return
	}
	p.reason(entry, decision, "proof-unverifiable", "reuse proof unavailable", core.ResourceKey{}, "")
}

func (p *Program) observationReason(entry *instance, stored []core.Observation, current []core.Observation, decision string, output bool) bool {
	for i := range current {
		item := current[i]
		if !item.Signature.Equal(item.Signature) {
			p.reason(entry, decision, "proof-unverifiable", "observation cannot be validated", item.Key, reasonAspect(item.Aspect))
			return true
		}
		for j := range stored {
			previous := stored[j]
			if previous.Key.Kind != item.Key.Kind || previous.Key.Name != item.Key.Name || previous.Aspect != item.Aspect {
				continue
			}
			if previous.Signature.Equal(item.Signature) {
				break
			}
			code, message := "input-changed", "input changed"
			if output {
				code, message = "output-changed", "output changed"
				if item.Signature.Mode == core.SignatureMissing {
					code, message = "output-missing", "output missing"
				}
			} else if item.Key.Kind == core.ResourceGlob {
				code, message = "membership-changed", "dependency membership changed"
			} else if item.Key.Kind == core.ResourceEnvironment {
				code, message = "environment-changed", "environment variable changed"
				if item.Key.Name == eval.ProcessEnvironmentName {
					code, message = "process-environment-changed", "process environment changed"
				}
			}
			key := item.Key
			if code == "process-environment-changed" {
				key = core.ResourceKey{}
			}
			p.reason(entry, decision, code, message, key, reasonAspect(item.Aspect))
			return true
		}
		if !hasAcceptedObservation(stored, item) {
			p.reason(entry, decision, "membership-changed", "dependency membership changed", item.Key, reasonAspect(item.Aspect))
			return true
		}
	}
	for i := range stored {
		if !hasAcceptedObservation(current, stored[i]) {
			p.reason(entry, decision, "membership-changed", "dependency membership changed", stored[i].Key, reasonAspect(stored[i].Aspect))
			return true
		}
	}
	return false
}
