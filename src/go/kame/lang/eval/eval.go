// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func (p *Program) Evaluate(run mem.Allocator, expression *expr.Expr, scope *Scope) Result {
	return p.evaluate(nil, scope, expression, &Context{Program: p, Scope: scope, Run: run, Requests: p.Requests})
}

// EvaluateWith evaluates an expression with caller-provided capability grants
// and selector frames. The context remains caller-owned.
func (p *Program) EvaluateWith(expression *expr.Expr, context *Context) Result {
	if p == nil || context == nil {
		return failure(mem.System, "EXPR_INVALID", source.Span{}, "missing evaluation context")
	}
	if context.Program == nil {
		context.Program = p
	}
	if context.Scope == nil {
		context.Scope = p.Scope
	}
	if context.Requests == nil {
		context.Requests = p.Requests
	}
	result := p.evaluate(context.Engine, context.Scope, expression, context)
	attachContextFrames(&result, context)
	attachSource(&result, context)
	return result
}

// attachContextFrames prepends caller frames. Owned diagnostics release every
// frame label, so borrowed context labels are cloned before attaching.
func attachContextFrames(result *Result, context *Context) {
	if result.Diagnostic.Code == "" || len(context.Frames) == 0 {
		return
	}
	a := context.Run
	if a == nil {
		a = mem.System
	}
	total := len(context.Frames) + len(result.Diagnostic.Frames)
	frames := slices.Make[diagnostic.Frame](a, total)
	for i := range context.Frames {
		label := context.Frames[i].Label
		if result.Diagnostic.Owned {
			label = owned(a, label)
		}
		kind, source := context.Frames[i].Kind, context.Frames[i].Source
		if result.Diagnostic.Owned {
			kind, source = owned(a, kind), owned(a, source)
		}
		frames[i] = diagnostic.Frame{Kind: kind, Label: label, Source: source, Span: context.Frames[i].Span}
	}
	copy(frames[len(context.Frames):], result.Diagnostic.Frames)
	if result.Diagnostic.Owned {
		for i := range result.Diagnostic.Frames {
			if result.Diagnostic.Frames[i].Kind != "" {
				mem.FreeString(a, result.Diagnostic.Frames[i].Kind)
			}
			if result.Diagnostic.Frames[i].Label != "" {
				mem.FreeString(a, result.Diagnostic.Frames[i].Label)
			}
			if result.Diagnostic.Frames[i].Source != "" {
				mem.FreeString(a, result.Diagnostic.Frames[i].Source)
			}
		}
	}
	slices.Free(a, result.Diagnostic.Frames)
	result.Diagnostic.Frames = frames
}

// EvaluateDefinition evaluates one lazy value definition in the caller's phase.
// Planning uses it to resolve only definitions reached by its active expression.
func (p *Program) EvaluateDefinition(key core.ResourceKey, context *Context) Result {
	if p == nil || key.Kind != core.ResourceDefinition {
		return failure(context.Run, "REF_MISSING", source.Span{}, "unknown definition")
	}
	for i := range p.Definitions {
		d := p.Definitions[i]
		if d.Name != key.Name {
			continue
		}
		result := p.definitionValue(nil, d, p.Scope, context)
		attachFrame(&result, context, d.Span, "definition")
		attachSource(&result, context)
		return result
	}
	return failure(context.Run, "REF_MISSING", source.Span{}, "unknown definition: "+key.Name)
}

func attachSource(result *Result, context *Context) {
	// Context and program sources outlive their diagnostic results. Diagnostics
	// borrowed from engine nodes must not be cloned or freed here; owned
	// diagnostics copy the source so Free releases it consistently.
	if result.Diagnostic.Code != "" && context.Source != "" && result.Diagnostic.Source == "" {
		if result.Diagnostic.Owned {
			result.Diagnostic.Source = cloneFailureText(context.Run, context.Source)
		} else {
			result.Diagnostic.Source = context.Source
		}
	}
}
