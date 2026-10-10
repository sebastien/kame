package program

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
)

// Both completed input resolution and intermediate comma barriers request
// prerequisites through this path. Resolution never starts a later group early.
func (p *Program) requestInputs(c *core.EngineContext, index int, inputs []string, resources []PlanInput) inputsResult {
	ready := true
	for i := range inputs {
		if i > 0 && i < len(resources) && (resources[i-1].SequenceEnd || resources[i].Group > resources[i-1].Group) && !ready { return inputsResult{Waiting: true} }
		input := inputs[i]
		kind := core.ResourceTarget
		ordered := i < len(resources) && resources[i].OrderOnly
		if i < len(resources) { kind = resources[i].Key.Kind }
		resolved := p.instanceFor(input)
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code != "" && d.Code != "TGT_NO_RULE" { return inputsResult{Diagnostic: d} }
		d.Free(p.Alloc)
		if dep != nil {
			if !p.prepareDependency(c, index, dep, ordered) { ready = false }
			continue
		}
		if kind == core.ResourceFile || (kind == core.ResourceTarget && isFileName(input)) {
			name := p.canonicalTarget(input, true)
			key := core.ResourceKey{Kind: core.ResourceFile, Name: name}
			dep = p.Engine.Lookup(key)
			if dep == nil {
				external := mem.Alloc[externalFileState](p.Alloc)
				external.Program, external.Name = p, cloneText(p.Alloc, name)
				dep = p.Engine.AddOwned(key, produceExternalFile, external, freeExternalFileState)
			}
			current := p.prepareDependency(c, index, dep, ordered)
			mem.FreeString(p.Alloc, name)
			if !current { ready = false
			} else if !dep.Current || dep.Latest.Kind == core.Nil { return inputsResult{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "required input does not exist: "+input)} }
			continue
		}
		definition := p.Eval.Definition(input)
		if definition == nil { return inputsResult{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+input)} }
		entry := &p.Instances[index]
		context := &eval.Context{Environment: entry.Environment, HasEnvironment: entry.EnvironmentClaimed, Cwd: p.Options.Directory, Phase: eval.EvaluatePhase}
		p.bindDefinitionEnvironment(context)
		dep = p.Eval.DefinitionWith(definition.Key.Name, context)
		if dep == nil || !p.addPurposeDependency(c, entry, dep, ordered) { ready = false }
	}
	return inputsResult{Waiting: !ready}
}
