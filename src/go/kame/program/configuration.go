package program

import (
	"kame/diagnostic"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) configureParameters(defines, parameters, environment []string) diagnostic.Diagnostic {
	var effective []string
	for i := range defines { effective = slices.Append(p.Alloc, effective, defines[i]) }
	for i := range parameters {
		equal := strings.IndexByte(parameters[i], '=')
		if equal > 0 && p.Eval.Definition(parameters[i][:equal]) != nil {
			effective = slices.Append(p.Alloc, effective, parameters[i])
			p.ParameterDefinitions = slices.Append(p.Alloc, p.ParameterDefinitions, cloneText(p.Alloc, parameters[i]))
		} else if equal > 0 {
			known := false
			for j := range p.Parsed.Items {
				r := p.Parsed.Items[j].Rule
				if r == nil { continue }
				for k := range r.Arguments { if r.Arguments[k].Name == parameters[i][:equal] { known = true } }
				for k := range r.Outputs { if !r.Outputs[k].Template && r.Outputs[k].Text == parameters[i] { known = true } }
			}
			if !known {
				slices.Free(p.Alloc, effective)
				return failure(p.Alloc, "TGT_ARGUMENT", "unknown parameter: "+parameters[i][:equal])
			}
		}
	}
	result := p.configureDefinitions(effective, environment)
	slices.Free(p.Alloc, effective)
	return result
}

func (p *Program) configureDefinitions(defines, environment []string) diagnostic.Diagnostic {
	for i := range defines {
		equal := strings.IndexByte(defines[i], '=')
		if equal <= 0 || p.Eval.Definition(defines[i][:equal]) == nil {
			return failure(p.Alloc, "DEF_INVALID", "--define must name a declared value definition")
		}
	}
	p.Eval.SetDefinitionOverrides(defines)
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
