package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/template"

	"strings"
)

func (p *Program) Render(run mem.Allocator, value *template.String, scope *Scope, context *Context) Result {
	// A named local keeps the fallback context alive for the whole call; a
	// Context literal inside the branch would become a block-scoped temporary.
	fallback := Context{Program: p, Scope: scope, Run: run}
	if context == nil {
		context = &fallback
	}
	var children []*expr.Expr
	for i := range value.Parts {
		if value.Parts[i].Expr != nil {
			children = append(children, value.Parts[i].Expr)
		}
	}
	operands := p.argumentValues(scope, children, context, value.Span)
	mem.FreeSlice(nil, children)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	defer freeValuesWithCallables(run, operands.Values)
	next := 0
	b := strings.Builder{}
	defer b.Reset()
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
				if !current {
					return Result{Waiting: true}
				}
			} else {
				key.Free(context.Run)
			}
			b.WriteString(tool)
			continue
		}
		value := operands.Values[next]
		next++
		var text string
		var ok bool
		if part.Kind == template.Reference {
			text, ok = InterpolationText(run, value)
		} else {
			text, ok = stringify(run, value)
		}
		if !ok {
			return renderFailure(context.Run, part.Expr, part.Span, value.Kind)
		}
		b.WriteString(text)
		mem.FreeString(run, text)
	}
	return Result{Value: core.NewString(run, b.String())}
}
