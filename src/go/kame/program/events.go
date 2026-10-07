package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/rule"
	"solod.dev/so/slices"
)

func (p *Program) emit(event Event) {
	event.Target = cloneText(p.Alloc, event.Target)
	event.Program = cloneText(p.Alloc, event.Program)
	event.Argv = cloneStrings(p.Alloc, event.Argv)
	if event.Key.Name != "" {
		event.Key = event.Key.Clone(p.Alloc)
	}
	if event.DependencyKey.Name != "" {
		event.DependencyKey = event.DependencyKey.Clone(p.Alloc)
	}
	p.Events = slices.Append(p.Alloc, p.Events, event)
}

// ObserveDefinition reports selected value roots; rule roots report themselves.
func (p *Program) ObserveDefinition(h *Handle) {
	if h == nil || !h.Definition || !h.Node.Current || h.valueRevision == h.Node.Revision { return }
	n := h.Node
	p.emit(Event{Kind: TargetValue, Target: h.Target, Key: n.Key, NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, Value: n.Latest.Clone(p.Alloc)})
	p.emitNode(n, h.Target, TargetCompleted, diagnostic.Span{}, nil)
	h.valueRevision = n.Revision
}

func (p *Program) emitNode(node *core.Node, target string, kind EventKind, span diagnostic.Span, data []byte) {
	if node == nil {
		return
	}
	event := Event{Kind: kind, Target: target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID, Span: span}
	if len(data) != 0 {
		event.Data = slices.Clone(p.Alloc, data)
	}
	p.emit(event)
}

func (p *Program) setServiceState(entry *instance, state string) {
	if entry == nil || entry.Rule.Kind != rule.ServiceRule || entry.ServiceState == state {
		return
	}
	entry.ServiceState = state
	node := entry.Node
	p.emit(Event{Kind: ServiceState, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, State: state})
}

func (p *Program) observeInstances() {
	for i := range p.Instances {
		entry := &p.Instances[i]
		if entry.Inspection {
			continue
		}
		node := entry.Node
		if node.Current && node.Revision != entry.valueRevision {
			event := Event{Kind: TargetValue, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID, Value: node.Latest.Clone(p.Alloc)}
			p.emit(event)
			entry.valueRevision = node.Revision
		}
		if entry.terminalEmitted && entry.terminalGeneration == node.Generation {
			continue
		}
		kind := EventKind(-1)
		if node.State == core.NodeComplete {
			kind = TargetCompleted
		} else if node.State == core.NodeFailed {
			kind = TargetFailed
		} else if node.State == core.NodeCancelled {
			kind = TargetCancelled
		}
		if kind < 0 {
			continue
		}
		if entry.Rule.Kind == rule.ServiceRule {
			p.setServiceState(entry, "terminal")
		}
		if kind == TargetCompleted || kind == TargetFailed || kind == TargetCancelled {
			p.releaseCacheLock(entry)
		}
		event := Event{Kind: kind, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID}
		if node.Diagnostic.Code != "" {
			event.Diagnostic = node.Diagnostic.Clone(p.Alloc)
		}
		p.emit(event)
		entry.terminalEmitted, entry.terminalGeneration = true, node.Generation
	}
}

func (p *Program) NextEvent() EventResult {
	if len(p.Events) == 0 {
		return EventResult{}
	}
	event := p.Events[0]
	copy(p.Events, p.Events[1:])
	p.Events = p.Events[:len(p.Events)-1]
	return EventResult{Event: event, OK: true}
}
