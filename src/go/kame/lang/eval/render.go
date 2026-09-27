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
