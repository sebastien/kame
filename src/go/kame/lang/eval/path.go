package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) pathValue(scope *Scope, expression *expr.Expr, c *Context) Result {
	var children []*expr.Expr
	for i := range expression.Parts {
		if expression.Parts[i].Expr != nil { children = slices.Append(c.Run, children, expression.Parts[i].Expr) }
	}
	operands := p.argumentValues(scope, children, c, expression.Span)
	slices.Free(c.Run, children)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" { return operands.Result }
	defer freeValuesWithCallables(c.Run, operands.Values)
	text, pattern := strings.NewBuilder(c.Run), strings.NewBuilder(c.Run)
	defer text.Free()
	defer pattern.Free()
	next := 0
	for i := range expression.Parts {
		part := expression.Parts[i]
		if part.Expr == nil {
			text.WriteString(part.Text)
			pattern.WriteString(part.Text)
			continue
		}
		value := operands.Values[next]
		next++
		if value.Kind != core.String || strings.IndexByte(value.Text, 0) >= 0 {
			return failure(c.Run, "EXPR_INVALID", part.Span, "path interpolation requires text without NUL")
		}
		text.WriteString(value.Text)
		for j := 0; j < len(value.Text); j++ {
			b := value.Text[j]
			if b == '*' || b == '?' || b == '[' || b == '\\' { pattern.WriteByte('\\') }
			pattern.WriteByte(b)
		}
	}
	name := text.String()
	if !(strings.HasPrefix(name, "/") || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../")) || strings.IndexByte(name, 0) >= 0 {
		return failure(c.Run, "EXPR_INVALID", expression.Span, "interpolated path requires an explicit /, ./ or ../ root and no NUL")
	}
	value := core.NewString(c.Run, name)
	value.GlobPattern = owned(c.Run, pattern.String())
	return Result{Value: value}
}
