package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
)

type declarationScope struct {
	Scope      *eval.Scope
	Signature  core.Signature
	Included   *eval.DeclarationBindings
	Iteration  *declarationIteration
	Generation *declarationGeneration
}

type declarationIteration struct {
	Parent   *declarationIteration
	Name     string
	Number   int
	Location eval.Location
}

func (p *Program) registrationValue(e *expr.Expr, scope *eval.Scope) eval.Result {
	state := generatedEvaluation{Program: p}
	defer func() {
		mem.FreeSlice(nil, state.Stack)
		freeStrings(p.Alloc, state.Dependencies)
	}()

	return p.evaluateDeclaration(e, scope, &state)
}

func (p *Program) declarationContext(scope *eval.Scope, state *generatedEvaluation) *eval.Context {
	return &eval.Context{Program: p.Eval, Scope: scope, Run: p.Alloc, Cwd: p.Options.Directory,
		Source: p.Parsed.Source.Name, Phase: eval.PlanningPhase, ResolverState: state,
		ResolveDefinition: resolveGeneratedDefinition, DeclarationFiles: observeDeclarationFiles,
		ResolveDeclarationImport: resolveDeclarationImport,
		TrackRuleCalls:           true, Environment: p.Options.Environment, HasEnvironment: true, Grants: p.Options.Grants}
}

func (p *Program) evaluateDeclaration(e *expr.Expr, scope *eval.Scope, state *generatedEvaluation) eval.Result {
	c := p.declarationContext(scope, state)
	r := p.Eval.EvaluateWith(e, c)
	eval.FreeEffects(p.Alloc, c.Effects)
	freeStrings(p.Alloc, c.WritePaths)
	if r.Diagnostic.Code == "" && (r.Waiting || r.Completed || r.Stream != nil || c.PhaseInvalid() || len(c.Effects) != 0) {
		r.Free(p.Alloc)
		return eval.Result{Diagnostic: p.outputDiagnostic(e.Span, "PHASE_INVALID", "declaration expressions must produce a value without runtime work")}
	}
	return r
}

func (p *Program) bindDeclarationValue(parent declarationScope, name string, value *core.Value) declarationScope {
	signature := core.ValueSignature(*value)
	if signature.Mode == core.SignatureContent && (parent.Scope == p.Eval.Scope || parent.Signature.Mode == core.SignatureContent) {
		digest := core.NewDigest()
		digest.Text("kame-declaration-binding-v1")
		digest.Write(parent.Signature.Digest[:])
		digest.Text(name)
		digest.Write(signature.Digest[:])
		digest.Sum(signature.Digest[:])
	} else {
		signature = core.Signature{}
	}
	parent.Scope = eval.DeclarationChildScope(p.Alloc, parent.Scope, name, value)
	parent.Signature = signature
	return parent
}

func (p *Program) registerDeclarationBlock(items []script.ScriptItem, initial declarationScope) diagnostic.Diagnostic {
	current := initial
	current.Included = &eval.DeclarationBindings{}
	current.Scope.Retain()
	defer func() { current.Scope.Free() }()
	return p.registerDeclarationItems(items, &current)
}

func (p *Program) registerDeclarationItems(items []script.ScriptItem, current *declarationScope) diagnostic.Diagnostic {
	for _, item := range items {
		if p.registrationPending {
			break
		}
		switch item.Kind {
		case script.Rule:
			if _, d := p.registerScopedRule(item.Rule, *current); d.Code != "" {
				return d
			}
		case script.DeclarationLet:
			r := p.registrationValue(item.Definition.Expression, current.Scope)
			if r.Diagnostic.Code != "" {
				return r.Diagnostic
			}
			next := p.bindDeclarationValue(*current, item.Definition.Name, &r.Value)
			r.Free(p.Alloc)
			if next.Scope == nil {
				return p.declarationDiagnostic(item.Span, item.SourceOrigin, "DEF_ESCAPE", "declaration binding would retain its own scope")
			}
			current.Scope.Free()
			*current = next
		case script.DeclarationFor:
			if d := p.registerDeclarationLoop(item, *current); d.Code != "" {
				return d
			}
		case script.Composition:
			if d := p.registerDeclarationBranches(item.Branches, *current); d.Code != "" {
				return d
			}
		case script.Generate:
			if d := p.registerRuleValues(item, *current); d.Code != "" {
				return d
			}
		case script.Include:
			if d := p.registerDeclarationInclude(item, current); d.Code != "" {
				return d
			}
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) registerDeclarationLoop(item script.ScriptItem, scope declarationScope) diagnostic.Diagnostic {
	r := p.registrationValue(item.Expression, scope.Scope)
	if r.Diagnostic.Code != "" {
		return r.Diagnostic
	}
	defer r.Free(p.Alloc)
	if r.Value.Kind != core.List {
		return p.declarationDiagnostic(item.Span, item.SourceOrigin, "EXPR_INVALID", "declaration for requires a list")
	}
	context := &eval.Context{Run: p.Alloc}
	location := p.targetLocation(rule.Target{SourceOrigin: item.SourceOrigin, Span: item.Loop.NameSpan})
	for index, value := range r.Value.List {
		binding := context.BorrowValue(value)
		iteration := p.bindDeclarationValue(scope, item.Loop.Name, &binding)
		if iteration.Scope == nil {
			rejected := eval.Result{Value: binding}
			rejected.Free(p.Alloc)
			return p.declarationDiagnostic(item.Span, item.SourceOrigin, "DEF_ESCAPE", "declaration binding would retain its own scope")
		}
		iteration.Iteration = &declarationIteration{Parent: scope.Iteration, Name: item.Loop.Name, Number: index + 1, Location: location}
		d := p.registerDeclarationBlock(item.Loop.Items, iteration)
		iteration.Scope.Free()
		if d.Code != "" {
			if !d.Owned {
				d = d.Clone(p.Alloc)
			}
			p.appendDeclarationIteration(&d, iteration.Iteration)
			return d
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) registerDeclarationBranches(branches []script.DeclarationBranch, scope declarationScope) diagnostic.Diagnostic {
	for _, branch := range branches {
		if branch.Condition != nil {
			r := p.registrationValue(branch.Condition, scope.Scope)
			if r.Diagnostic.Code != "" {
				return r.Diagnostic
			}
			selected := r.Value.Kind != core.Nil && !(r.Value.Kind == core.Bool && !r.Value.Bool)
			r.Free(p.Alloc)
			if !selected {
				continue
			}
		}
		return p.registerDeclarationBlock(branch.Items, scope)
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) registerScopedRule(authored *rule.Rule, scope declarationScope) (*rule.Rule, diagnostic.Diagnostic) {
	resolved := p.resolveOutputs(authored, scope.Scope)
	if resolved.Diagnostic.Code != "" {
		return nil, resolved.Diagnostic
	}
	candidate := resolved.Rule
	if d := duplicateLiteral(p, candidate); d.Code != "" {
		return nil, d
	}
	for _, argument := range authored.Arguments {
		if p.Eval.Definition(argument.Name) != nil {
			return nil, p.outputDiagnostic(argument.Span, "TGT_ARGUMENT", "target argument shadows definition: "+argument.Name)
		}
	}
	if scope.Scope != p.Eval.Scope || scope.Iteration != nil || scope.Generation != nil {
		if candidate == authored {
			candidate = mem.Alloc[rule.Rule](p.Alloc)
			*candidate = *authored
			p.ScopedRules = append(p.ScopedRules, candidate)
		}
		if p.RuleDeclarations == nil {
			p.RuleDeclarations = make(map[*rule.Rule]declarationScope)
		}
		scope.Scope.Retain()
		p.RuleDeclarations[candidate] = scope
	}
	p.Rules = append(p.Rules, registeredRule{Rule: candidate})
	p.declareRuleTools(authored, authoredShell(p.Parsed), true)
	return candidate, diagnostic.Diagnostic{}
}
