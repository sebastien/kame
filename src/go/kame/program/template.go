package program

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Bindings are string literals in a lexical scope. Clone argv/descriptor text
// because a compiled session outlives the host's input descriptor.
func templateBindings(a mem.Allocator, root *expr.Expr, defines []string) *expr.Expr {
	if len(defines) == 0 {
		return root
	}
	application := mem.Alloc[expr.Expr](a)
	application.Kind, application.Span = expr.Application, root.Span
	head := mem.Alloc[expr.Expr](a)
	head.Kind, head.Text = expr.Name, "let"
	bindings := mem.Alloc[expr.Expr](a)
	bindings.Kind = expr.List
	for i := range defines {
		equal := strings.IndexByte(defines[i], '=')
		if equal <= 0 {
			continue
		}
		name := mem.Alloc[expr.Expr](a)
		name.Kind, name.Text, name.TextOwned = expr.Name, cloneText(a, defines[i][:equal]), true
		value := mem.Alloc[expr.Expr](a)
		value.Kind, value.Text, value.TextOwned = expr.Symbol, cloneText(a, defines[i][equal+1:]), true
		bindings.Items = slices.Append(a, bindings.Items, name, value)
	}
	application.Items = slices.Append(a, application.Items, head, bindings, root)
	return application
}
