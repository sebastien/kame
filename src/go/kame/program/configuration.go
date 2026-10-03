package program

import (
	"kame/diagnostic"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) configureDefinitions(defines, environment []string) diagnostic.Diagnostic {
	for i := range defines {
		equal := strings.IndexByte(defines[i], '=')
		if equal <= 0 || p.Eval.Definition(defines[i][:equal]) == nil {
			return failure(p.Alloc, "DEF_INVALID", "--define must name a declared value definition")
		}
	}
	for i := range p.Eval.Definitions {
		name := p.Eval.Definitions[i].Name
		prefix := "KAME_" + name + "="
		value, provided := "", false
		for j := range environment {
			if strings.HasPrefix(environment[j], prefix) {
				value, provided = environment[j][len(prefix):], true
			}
		}
		prefix = name + "="
		for j := range defines {
			if strings.HasPrefix(defines[j], prefix) {
				value, provided = defines[j][len(prefix):], true
			}
		}
		if !provided {
			continue
		}
		if !p.Eval.OverrideDefinition(name, value) {
			return failure(p.Alloc, "DEF_INVALID", "cannot override an active definition")
		}
		p.Configuration = slices.Append(p.Alloc, p.Configuration, cloneText(p.Alloc, prefix+value))
	}
	return diagnostic.Diagnostic{}
}
