package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/rule"
)

// ConstructedRule pairs authored syntax with the lexical scope at construction.
// The evaluator retains both until consumers of the value have finished.
type ConstructedRule struct {
	Owner       *Program
	Declaration *rule.Rule
	Scope       *Scope
	Frames      []diagnostic.Frame
	syntax      *expr.RuleSyntax
}

func (p *Program) constructRule(scope *Scope, e *expr.Expr, c *Context) Result {
	if e.RuleSyntax == nil {
		return failure(c.Run, "EXPR_INVALID", e.Span, "missing rule declaration")
	}
	scope.Retain()
	e.RuleSyntax.Retain()
	value := &ConstructedRule{Owner: p, Declaration: e.RuleSyntax.Declaration.(*rule.Rule), Scope: scope, syntax: e.RuleSyntax}
	for i := len(c.ruleCallFrames) - 1; i >= 0; i-- {
		frame := c.ruleCallFrames[i]
		frame.Kind = owned(p.Alloc, frame.Kind)
		frame.Label = owned(p.Alloc, frame.Label)
		frame.Source = owned(p.Alloc, frame.Source)
		value.Frames = append(value.Frames, frame)
	}
	p.ConstructedRules = append(p.ConstructedRules, value)
	return Result{Value: core.Value{Kind: core.Rule, Rule: value}}
}

func (r *ConstructedRule) free(a mem.Allocator) {
	r.Scope.Free()
	r.syntax.Free()
	for _, frame := range r.Frames {
		mem.FreeString(a, frame.Kind)
		mem.FreeString(a, frame.Label)
		mem.FreeString(a, frame.Source)
	}
	mem.FreeSlice(nil, r.Frames)
}

func (p *Program) RuleCaptureSignature(scope *Scope) core.Signature {
	digest := core.NewDigest()
	digest.Text("kame-rule-value-scope-v1")
	for s := scope; s != nil && s != p.Scope; s = s.Parent {
		if s.Parent == nil {
			for _, module := range p.Modules {
				if module.Scope == s && module.Signature.Mode == core.SignatureContent {
					digest.Text("authored-module")
					digest.Write(module.Signature.Digest[:])
					signature := core.Signature{Mode: core.SignatureContent}
					digest.Sum(signature.Digest[:])
					return signature
				}
			}
		}
		digest.Uint64(uint64(len(s.Bindings)))
		for _, binding := range s.Bindings {
			if binding.Kind != bindingValue && binding.Kind != bindingEager {
				return core.Signature{}
			}
			signature := core.ValueSignature(binding.Value)
			if signature.Mode != core.SignatureContent {
				return core.Signature{}
			}
			digest.Text(binding.Name)
			digest.Write(signature.Digest[:])
		}
	}
	signature := core.Signature{Mode: core.SignatureContent}
	digest.Sum(signature.Digest[:])
	return signature
}
