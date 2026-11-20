package program

import (
	"kame/core"
	"kame/lang/rule"
	"slices"
)

type jobSlot struct {
	node       *core.Node
	generation int64
}

func (p *Program) ordinaryJob(node *core.Node) bool {
	state, ok := node.Context.(*instanceState)
	if !ok || state.Program != p || state.Index >= len(p.Instances) {
		return false
	}
	entry := &p.Instances[state.Index]
	return entry.Node == node && !entry.Inspection && entry.Rule.Kind != rule.ServiceRule
}

func (p *Program) canRunJob(node *core.Node) bool {
	return len(p.activeJobs) < max(1, p.Options.Jobs) ||
		node.IsTearingDown() || !p.ordinaryJob(node) ||
		slices.Contains(p.activeJobs, jobSlot{node, node.Generation})
}

func (p *Program) takeJobSlot(node *core.Node) {
	if node.IsTearingDown() || !p.ordinaryJob(node) {
		return
	}
	slot := jobSlot{node, node.Generation}
	if !slices.Contains(p.activeJobs, slot) {
		p.activeJobs = append(p.activeJobs, slot)
	}
}

func (p *Program) releaseJobSlots() {
	p.activeJobs = slices.DeleteFunc(p.activeJobs, func(slot jobSlot) bool {
		node := slot.node
		if slot.generation != node.Generation || node.Interest == 0 || p.Engine.Suspended(node) ||
			node.State == core.NodeComplete || node.State == core.NodeFailed || node.State == core.NodeCancelled {
			return true
		}
		// A parent awaiting another rule yields its slot and must acquire one
		// again on resumption. Pure evaluation and child processes within the
		// same recipe can make progress in the parent's slot.
		return !node.Submitted && (node.State == core.NodeWaiting || node.State == core.NodeReady) &&
			p.awaitsJob(node, make(map[*core.Node]bool))
	})
}

func (p *Program) awaitsJob(node *core.Node, seen map[*core.Node]bool) bool {
	if seen[node] {
		return false
	}
	seen[node] = true
	for _, edge := range node.Static {
		if (edge.State != core.NodeComplete || edge.ValidationPending) && p.unfinishedJob(edge, seen) {
			return true
		}
	}
	for _, edge := range node.Dynamic {
		if (!edge.Current || edge.ValidationPending) && p.unfinishedJob(edge, seen) {
			return true
		}
	}
	return false
}

func (p *Program) unfinishedJob(node *core.Node, seen map[*core.Node]bool) bool {
	return node.State != core.NodeFailed && node.State != core.NodeCancelled &&
		(p.ordinaryJob(node) || p.awaitsJob(node, seen))
}
