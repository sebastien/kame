package program

import (
	"kame/core"
	"solod.dev/so/slices"
)

func (p *Program) prepareDependency(c *core.EngineContext, index int, dependency *core.Node, ordered bool) bool {
 child := p.instanceIndex(dependency)
 if child >= 0 && !p.claimEnvironment(child, p.Instances[index].Environment) {
  p.failRule(c, index, p.environmentFailure(child, "shared prerequisite has a different recipe environment"))
  return false
 }
	p.adoptTask(dependency, p.Instances[index].runEpoch)
	return p.addPurposeDependency(c, &p.Instances[index], dependency, ordered)
}

func (p *Program) adoptTask(node *core.Node, epoch int64) {
	p.claimStaleTask(node, epoch)
	index := p.instanceIndex(node)
	if index < 0 || epoch == 0 {
		return
	}
	if p.Instances[index].Node.State == core.NodeWaiting || p.Instances[index].Node.State == core.NodeReady {
		return
	}
	if p.Instances[index].runEpoch < epoch {
		p.Instances[index].runEpoch = epoch
	}
}

func (p *Program) addPurposeDependency(c *core.EngineContext, entry *instance, dependency *core.Node, ordered bool) bool {
	existed := slices.Contains(entry.Node.Dynamic, dependency)
	current := false
	if ordered {
		current = c.OrderDependency(dependency.Key)
	} else {
		current = c.Dependency(dependency.Key)
	}
	if !existed && slices.Contains(entry.Node.Dynamic, dependency) {
		event := Event{Kind: DependencyDiscovered, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, DependencyID: dependency.ID, DependencyKey: dependency.Key}
		p.emit(event)
	}
	return current
}

func (p *Program) nodeForRequest(id int64) *core.Node {
	for i := range p.Instances {
		if p.Instances[i].Node.HostRequestID == id {
			return p.Instances[i].Node
		}
	}
	return nil
}
