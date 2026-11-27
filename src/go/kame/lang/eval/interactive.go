package eval

import (
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"slices"
	"strconv"
)

// InteractiveName is a top-level name visible in an interactive session.
type InteractiveName struct {
	Name     string
	Callable bool   // session function or library operation
	Detail   string // parameters, arity, or binding kind, for display
}

// InteractiveNames lists names visible at the session top level for
// completion: session functions and registered operations, then the other
// top-level bindings. Nothing is evaluated.
func (p *Program) InteractiveNames() []InteractiveName {
	var names []InteractiveName
	for _, entry := range p.Scope.bindingIndex {
		b := &p.Scope.Bindings[entry.index]
		if b.Kind == bindingSourced {
			b = b.Source.localBinding(b.Name)
		}
		name := InteractiveName{Name: entry.name}
		switch b.Kind {
		case bindingFunction:
			name.Callable, name.Detail = true, "function"
			if b.Function != nil {
				for _, parameter := range b.Function.Parameters {
					name.Detail += " " + parameter.Name
					if parameter.Rest {
						name.Detail += "..."
					}
				}
			}
		case bindingEager:
			name.Detail = "let"
		case bindingDefinition, bindingLocalDefinition:
			name.Detail = "lazy"
		default:
			name.Detail = "value"
		}
		names = append(names, name)
	}
	if p.Registry != nil {
		for i := range p.Registry.Items {
			operation := &p.Registry.Items[i]
			arity := strconv.Itoa(operation.MinArity)
			switch {
			case operation.MaxArity < 0:
				arity += "+"
			case operation.MaxArity != operation.MinArity:
				arity += "-" + strconv.Itoa(operation.MaxArity)
			}
			names = append(names, InteractiveName{Name: operation.Name, Callable: true, Detail: "operation, " + arity + " args"})
		}
	}
	return names
}

// HasFunction reports whether name is bound to a top-level session function.
func (p *Program) HasFunction(name string) bool {
	b := p.Scope.lookup(name)
	if b != nil && b.Kind == bindingSourced {
		b = b.Source.localBinding(b.Name)
	}
	return b != nil && b.Kind == bindingFunction
}

// HasLexicalBinding keeps driver commands below session lexical names.
func (p *Program) HasLexicalBinding(name string) bool {
	return p.Scope.localBinding(name) != nil
}

// RegisterInteractive adds definitions entered into a live interactive session.
// Eager bindings and functions shadow earlier top-level bindings, as successive
// REPL entries do. Lazy definitions are engine resources whose evaluated value
// may already be retained, so they cannot be redefined; contributions would
// likewise mutate an already-evaluated aggregate. Validation completes before
// any registration so a rejected entry leaves the session unchanged.
func (p *Program) RegisterInteractive(definitions []*definition.Definition, captured []string) []diagnostic.Diagnostic {
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
		duplicate := p.Definition(d.Name) != nil || existing != nil && (existing.Kind == bindingDefinition || existing.Kind == bindingLocalDefinition)
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
	p.Scope.replaceBindings = true
	letNames := make(map[string]bool)
	for _, d := range definitions {
		switch {
		case d.Default && p.Scope.lookup(d.Name) != nil:
		case d.Eager:
			p.Scope.reserveEager(d.Expression, letNames[d.Name])
			letNames[d.Name] = true
		case d.Function:
			// The binding and saved aliases own this body, not parsed history.
			cloned := mem.Alloc[definition.Definition](p.Alloc)
			*cloned = *d
			cloned.Parameters = slices.Clone(d.Parameters)
			cloned.Expression = expr.Clone(p.Alloc, d.Expression)
			p.registerFunction(cloned).DefinitionOwned = true
		default:
			p.registerLazy(d)
		}
	}
	p.retainCapturedLets(captured)
	return nil
}

func (p *Program) retainCapturedLets(captured []string) {
	for _, name := range captured {
		if b := p.Scope.localBinding(name); b != nil && b.Kind == bindingEager {
			b.Captured = true
		}
	}
}

// CheckInteractiveAssignments resolves references to the session's existing
// lets before an entry changes its binding table.
func (p *Program) CheckInteractiveAssignments(part *script.Script) []string {
	return part.ValidateSessionAssignments(func(name string) (bool, bool) {
		b := p.Scope.localBinding(name)
		return b != nil && b.Kind == bindingEager, b != nil && b.Captured
	})
}
