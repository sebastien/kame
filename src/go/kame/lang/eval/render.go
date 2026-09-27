// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

func (p *Program) Render(run mem.Allocator, value *template.String, scope *Scope, context *Context) Result {
	// A named local keeps the fallback context alive for the whole call; a
	// Context literal inside the branch would become a block-scoped temporary.
	fallback := Context{Program: p, Scope: scope, Run: run}
	if context == nil {
		context = &fallback
	}
	b := strings.NewBuilder(run)
	defer b.Free()
	for i := range value.Parts {
		part := value.Parts[i]
		if part.Kind == template.Literal {
			b.WriteString(part.Text)
			continue
		}
		if part.Kind == template.Tool {
			if context.ToolResolver == nil {
				return failure(context.Run, "TOOL_UNKNOWN", part.Span, "tool registry is unavailable")
			}
			tool, ok := context.ToolResolver(context.ResolverState, part.Text)
			if !ok {
				return failure(context.Run, "TOOL_MISSING", part.Span, "required tool is unavailable: "+part.Text)
			}
			key := core.NewResourceKey(context.Run, core.ResourceFile, tool)
			if context.Engine != nil {
				current := context.Engine.Dependency(key)
				if context.DependencyObserver != nil {
					context.DependencyObserver(context.ResolverState, key)
				}
				key.Free(context.Run)
				if !current { return Result{Waiting: true} }
			} else {
				key.Free(context.Run)
			}
			b.WriteString(tool)
			continue
		}
		var r Result
		if part.Kind == template.Selector {
			r = p.selector(part.Text, part.Span, context)
		} else {
			r = p.evaluate(context.Engine, scope, part.Expr, context)
		}
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		text, ok := stringify(run, r.Value)
		r.Value.Free(run)
		if !ok {
			return failure(context.Run, "EXPR_INVALID", part.Span, "records and bytes require explicit text conversion")
		}
		b.WriteString(text)
		mem.FreeString(run, text)
	}
	return Result{Value: core.NewString(run, b.String())}
}
