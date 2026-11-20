package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"path"
	"slices"
	"strings"
)

// DeclarationBindings tracks definitions spliced into one declaration block.
// The scope chain still owns each binding and supplies lexical lookup.
type DeclarationBindings struct {
	values map[string]*localDefinition
}

// AddDeclarationIncludeSource records spans for independently parsed declarations.
func (p *Program) AddDeclarationIncludeSource(parsed *script.Script, signature core.Signature, frames []diagnostic.Frame) int {
	p.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
	p.SetSourcePartDirectory(path.Dir(parsed.Source.Name))
	p.SetSourcePartIncludes(frames)
	origin := len(p.SourceParts)
	p.SourceParts[origin-1].Included, p.SourceParts[origin-1].Signature = parsed.Source, signature
	SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), origin)
	return origin
}

// BindDeclarationInclude gives a segment access to the definitions already
// spliced into its lexical scope. Direct definitions within the segment share
// a scope, including forward references.
func (p *Program) BindDeclarationInclude(parent *Scope, items []script.ScriptItem, origin int, block *DeclarationBindings) (*Scope, diagnostic.Diagnostic) {
	scope := newScope(p.Alloc, parent)
	if block.values == nil {
		block.values = make(map[string]*localDefinition)
	}
	retainOperandScope := false
	for _, item := range items {
		d := item.Definition
		if item.Kind != script.Definition || d == nil {
			continue
		}
		previous := scope.lookup(d.Name)
		if d.Default && previous != nil {
			continue
		}
		if d.Contribution {
			if previous != nil && previous.Kind != bindingLocalDefinition && previous.Kind != bindingDefinition {
				scope.Free()
				return nil, p.includedDefinitionError(d, origin, "contributions require a value definition: "+d.Name)
			}
			local := p.addIncludedContribution(scope, block, d, previous, origin)
			retainOperandScope = retainOperandScope || local.Scope != scope
			continue
		}
		if previous != nil {
			scope.Free()
			return nil, p.includedDefinitionError(d, origin, "duplicate included definition: "+d.Name)
		}
		addModuleDefinition(scope, d)
		if !d.Function {
			local := scope.localBinding(d.Name).Local
			block.values[d.Name] = local
			p.configureIncludedDefinition(local, d)
		}
	}
	if d := p.registerImports(items, scope); d.Code != "" {
		scope.Free()
		return nil, d
	}
	// An earlier rule can see a contribution spliced through a later include.
	// Retain that operand's lexical scope for the duration of the program.
	if retainOperandScope {
		scope.Retain()
		p.declarationScopes = append(p.declarationScopes, scope)
	}
	return scope, diagnostic.Diagnostic{}
}

func (p *Program) includedDefinitionError(d *definition.Definition, origin int, message string) diagnostic.Diagnostic {
	r := failure(p.Alloc, "DEF_INVALID", d.NameSpan, message)
	attachSource(&r, &Context{Program: p, Run: p.Alloc, Source: p.Script.Source.Name, SourceOrigin: origin})
	return r.Diagnostic
}

func (p *Program) addIncludedContribution(scope *Scope, block *DeclarationBindings, d *definition.Definition, previous *binding, origin int) *localDefinition {
	local := block.values[d.Name]
	if previous == nil || local == nil || previous.Local != local {
		local = scope.setLocalDefinition(d.Name, d.Expression)
		block.values[d.Name] = local
		p.configureIncludedDefinition(local, d)
		local.Aggregate = &localAggregate{}
		if previous != nil {
			local.Aggregate.Inherited = expr.Expr{Kind: expr.Name, Text: d.Name, Span: d.NameSpan, SourceOrigin: origin}
			local.Aggregate.append(&local.Aggregate.Inherited, scope.Parent)
		}
	} else if local.Aggregate == nil {
		local.Aggregate = &localAggregate{}
		local.Aggregate.append(local.Expression, local.Scope)
	}
	local.Aggregate.append(d.Expression, scope)
	return local
}

func (p *Program) configureIncludedDefinition(local *localDefinition, d *definition.Definition) {
	if p.declarationValues == nil {
		p.declarationValues = make(map[string]definition.Override)
	}
	override := definition.SelectOverride(d.Name, p.explicitDefinitionOverrides, p.ModuleEnvironment)
	p.declarationValues[d.Name] = override
	if override.Name == "" {
		return
	}
	if override.Typed {
		parsed := expr.Parse(p.Alloc, "<set>", override.Text)
		local.Configured = expr.Clone(p.Alloc, parsed.Expr)
		parsed.Free()
	} else {
		literal := expr.LiteralString(override.Text, d.Expression.Span)
		local.Configured = expr.Clone(p.Alloc, &literal)
	}
}

func (p *Program) HasConfigurableDefinition(name string) bool {
	_, included := p.declarationValues[name]
	return p.Definition(name) != nil || included
}

// IncludedConfiguration returns effective overrides for values introduced by
// selected local includes. Values already represented by a global are skipped.
func (p *Program) IncludedConfiguration() []definition.Override {
	var overrides []definition.Override
	for name, override := range p.declarationValues {
		if override.Name != "" && p.Definition(name) == nil {
			overrides = append(overrides, override)
		}
	}
	slices.SortFunc(overrides, func(left, right definition.Override) int { return strings.Compare(left.Name, right.Name) })
	return overrides
}
