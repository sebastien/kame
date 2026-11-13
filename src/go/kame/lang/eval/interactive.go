package eval

import (
	"kame/diagnostic"
	"kame/lang/definition"
)

// RegisterInteractive adds definitions entered into a live interactive session.
// Eager bindings and functions shadow earlier top-level bindings, as successive
// REPL entries do. Lazy definitions are engine resources whose evaluated value
// may already be retained, so they cannot be redefined; contributions would
// likewise mutate an already-evaluated aggregate. Validation completes before
// any registration so a rejected entry leaves the session unchanged.
func (p *Program) RegisterInteractive(definitions []*definition.Definition) []diagnostic.Diagnostic {
	var diagnostics []diagnostic.Diagnostic
	fail := func(d *definition.Definition, message string, tip string) {
		failure := diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: d.NameSpan.Start, End: d.NameSpan.End}}
		if tip != "" {
			failure.Tips = []string{tip}
		}
		diagnostics = append(diagnostics, failure.Clone(p.Alloc))
	}
	for i, d := range definitions {
		if d.Contribution {
			fail(d, "contributions are not supported interactively: "+d.Name, "rebind the complete value with let")
			continue
		}
		if d.Eager || d.Function {
			continue
		}
		existing := p.Scope.lookup(d.Name)
		if existing != nil && d.Default {
			continue
		}
		duplicate := existing != nil && (existing.Kind == bindingDefinition || existing.Kind == bindingLocalDefinition)
		for j := 0; j < i && !duplicate; j++ {
			duplicate = definitions[j].Name == d.Name && !definitions[j].Eager && !definitions[j].Function
		}
		if duplicate {
			fail(d, "lazy definition already exists in this session: "+d.Name, "use let NAME = VALUE to rebind a name interactively")
		}
	}
	if len(diagnostics) != 0 {
		return diagnostics
	}
	for _, d := range definitions {
		switch {
		case d.Default && p.Scope.lookup(d.Name) != nil:
		case d.Eager:
			p.Scope.reserveEager(d.Name)
		case d.Function:
			p.registerFunction(d)
		default:
			p.registerLazy(d)
		}
	}
	return nil
}
