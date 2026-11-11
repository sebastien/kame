package program

import (
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"

	"strings"
)

// Apply definition overrides before computed outputs and generators evaluate.
// Other assignment-shaped operands are validated after rule registration.
func (p *Program) configureParameters(defines, parameters, environment []string) diagnostic.Diagnostic {
	var effective []string
	for i := range defines {
		effective = append(effective, defines[i])
	}
	for i := range parameters {
		equal := strings.IndexByte(parameters[i], '=')
		if equal > 0 && p.Eval.Definition(parameters[i][:equal]) != nil {
			effective = append(effective, parameters[i])
			p.ParameterDefinitions = append(p.ParameterDefinitions, cloneText(p.Alloc, parameters[i]))
		}
	}
	result := p.configureDefinitions(effective, environment)
	mem.FreeSlice(nil, effective)
	return result
}

func (p *Program) validateParameters(parameters []string) diagnostic.Diagnostic {
	for i := range parameters {
		equal := strings.IndexByte(parameters[i], '=')
		if equal <= 0 || p.Eval.Definition(parameters[i][:equal]) != nil {
			continue
		}
		known := false
		for j := range p.Rules {
			r := p.Rules[j].Rule
			if r == nil {
				continue
			}
			for k := range r.Arguments {
				if r.Arguments[k].Name == parameters[i][:equal] {
					known = true
				}
			}
			for k := range r.Outputs {
				if !r.Outputs[k].Template && r.Outputs[k].Text == parameters[i] {
					known = true
				}
			}
		}
		if !known {
			return failure(p.Alloc, "TGT_ARGUMENT", "unknown parameter: "+parameters[i][:equal])
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) configureDefinitions(defines, environment []string) diagnostic.Diagnostic {
	for i := range defines {
		override := definition.ParseOverride(defines[i])
		if override.Name == "" || p.Eval.Definition(override.Name) == nil {
			option := "--define"
			if override.Typed {
				option = "--set"
			}
			return failure(p.Alloc, "DEF_INVALID", option+" must name a declared value definition")
		}
		if override.Typed {
			parsed := expr.Parse(p.Alloc, "<set>", override.Text)
			valid := len(parsed.Diagnostics) == 0 && expr.IsLiteral(parsed.Expr)
			parsed.Free()
			if !valid {
				return failure(p.Alloc, "DEF_INVALID", "--set requires a closed literal value for a declared value definition")
			}
		}
	}
	p.Eval.SetDefinitionOverrides(defines)
	for i := range p.Eval.Definitions {
		name := p.Eval.Definitions[i].Name
		prefix := "KAME_" + name + "="
		value, provided, typed := "", false, false
		for j := range environment {
			if strings.HasPrefix(environment[j], prefix) {
				value, provided = environment[j][len(prefix):], true
			}
		}
		prefix = name + "="
		for j := range defines {
			override := definition.ParseOverride(defines[j])
			if override.Name == name {
				value, provided, typed = override.Text, true, override.Typed
			}
		}
		if !provided {
			continue
		}
		installed := false
		if typed {
			installed = p.Eval.OverrideDefinitionValue(name, value)
		} else {
			installed = p.Eval.OverrideDefinition(name, value)
		}
		if !installed {
			return failure(p.Alloc, "DEF_INVALID", "cannot override an active definition")
		}
		if typed {
			prefix = name + ":="
		}
		p.Configuration = append(p.Configuration, cloneText(p.Alloc, prefix+value))
	}
	return diagnostic.Diagnostic{}
}
