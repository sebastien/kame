package program

import (
	"kame/core"
	"solod.dev/so/slices"
)

func (p *Program) definitionNode(name string) *core.Node { return p.Eval.Definition(name) }

func (p *Program) prepareDependency(c *core.EngineContext, index int, dependency *core.Node) bool {
	p.adoptTask(dependency, p.Instances[index].runEpoch)
	return p.addDependency(c, &p.Instances[index], dependency)
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

func (p *Program) addDependency(c *core.EngineContext, entry *instance, dependency *core.Node) bool {
	existed := slices.Contains(entry.Node.Dynamic, dependency)
	current := c.Dependency(dependency.Key)
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
