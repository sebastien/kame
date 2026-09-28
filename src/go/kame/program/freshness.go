package program

import (
	"kame/core"
	"solod.dev/so/mem"
)

func (p *Program) freshness(plan *Plan, node *core.Node) Freshness {
	if len(plan.Outputs) == 0 {
		return Stale
	}
	inputs := plan.Inputs
	if plan.Resolved {
		inputs = plan.ResolvedInputs
	}
	fileInputs := 0
	var oldest int64
	for i := range plan.Outputs {
		name := p.canonicalTarget(plan.Outputs[i], true)
		result := p.Host.Stat(name)
		mem.FreeString(p.Alloc, name)
		if !result.Exists {
			return Stale
		}
		if i == 0 || result.Info.ModTime < oldest {
			oldest = result.Info.ModTime
		}
	}
	for i := range inputs {
		if !isFileName(inputs[i]) {
			continue
		}
		fileInputs++
		name := p.canonicalTarget(inputs[i], true)
		result := p.Host.Stat(name)
		mem.FreeString(p.Alloc, name)
		if !result.Exists {
			return Stale
		}
		if oldest < result.Info.ModTime {
			return Stale
		}
	}
	if node != nil {
		for i := range node.Dynamic {
			dependency := node.Dynamic[i]
			if dependency.Key.Kind != core.ResourceFile {
				continue
			}
			fileInputs++
			name := p.canonicalTarget(dependency.Key.Name, true)
			result := p.Host.Stat(name)
			mem.FreeString(p.Alloc, name)
			if !result.Exists {
				return Stale
			}
			if oldest < result.Info.ModTime {
				return Stale
			}
		}
	}
	if fileInputs == 0 {
		return Stale
	}
	return Fresh
}

func isPath(value string) bool {
	return len(value) != 0 && (value[0] == '/' || (len(value) > 1 && value[0] == '.' && value[1] == '/'))
}
func isFileName(value string) bool { return isPath(value) || hasSlash(value) }
func hasSlash(value string) bool {
	for i := range value {
		if value[i] == '/' {
			return true
		}
	}
	return false
}
