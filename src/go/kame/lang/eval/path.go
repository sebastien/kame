package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func pathGlob(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' { i++; continue }
		if text[i] == '*' || text[i] == '?' || text[i] == '[' { return true }
	}
	return false
}

func appendPathText(b *strings.Builder, text string) {
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) { i++ }
		b.WriteByte(text[i])
	}
}

func (p *Program) pathExpression(scope *Scope, expression *expr.Expr, c *Context) Result {
	result := Result{Value: core.NewString(c.Run, expression.Text)}
	glob := pathGlob(expression.Text)
	if len(expression.Parts) == 0 && !expression.LiteralPath && strings.IndexByte(expression.Text, '\\') >= 0 {
		b := strings.NewBuilder(c.Run)
		appendPathText(&b, expression.Text)
		result.Value.Free(c.Run)
		result.Value = core.NewString(c.Run, b.String())
		b.Free()
		result.Value.GlobPattern = owned(c.Run, expression.Text)
	}
	if len(expression.Parts) != 0 {
		result.Value.Free(c.Run)
		result = p.pathValue(scope, expression, c)
		glob = false
		for i := range expression.Parts { if expression.Parts[i].Expr == nil && pathGlob(expression.Parts[i].Text) { glob = true } }
	}
	if result.Waiting || result.Diagnostic.Code != "" || !glob || expression.LiteralPath { return result }
	if p.Registry == nil { result.Free(c.Run); return failure(c.Run, "OP_UNKNOWN", expression.Span, "glob expressions require wildcard") }
	operation := p.Registry.lookup("wildcard")
	if operation == nil { result.Free(c.Run); return failure(c.Run, "OP_UNKNOWN", expression.Span, "glob expressions require wildcard") }
	ready := p.prepareOperation(operation, 1, c, expression.Span)
	if ready.Waiting || ready.Diagnostic.Code != "" { result.Free(c.Run); return ready }
	values := slices.Make[core.Value](c.Run, 1)
	values[0] = result.Value
	output := p.invokeOperation(operation, values, nil, c, expression.Span)
	freeValues(c.Run, values)
	return output
}

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
			if expression.LiteralPath { text.WriteString(part.Text) } else { appendPathText(&text, part.Text) }
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
