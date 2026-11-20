package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"

	"strings"
)

func (p *Program) pathExpression(scope *Scope, expression *expr.Expr, c *Context) Result {
	if len(expression.Parts) != 0 {
		return p.pathValue(scope, expression, c)
	}
	if expression.Pattern != nil {
		return Result{Value: pathPatternValue(c.Run, expression.Pattern)}
	}
	if expression.PathGlob {
		text := core.NewString(c.Run, expression.Text)
		text.Kind = core.PathPattern
		return Result{Value: text}
	}
	if strings.Contains(expression.Text, "\\") {
		var text, pattern strings.Builder
		expr.AppendPathText(&text, expression.Text, false)
		expr.AppendPathText(&pattern, expression.Text, true)
		value := core.NewPath(c.Run, text.String())
		value.GlobPattern = owned(c.Run, pattern.String())
		return Result{Value: value}
	}
	return Result{Value: core.NewPath(c.Run, expression.Text)}
}

func pathPatternValue(a mem.Allocator, pattern *expr.Pattern) core.Value {
	kind := core.PathPattern
	if pattern.References != 0 {
		kind = core.PathTemplate
	}
	return core.Value{Kind: kind, Text: expr.CanonicalPathPattern(a, pattern)}
}

func (p *Program) pathValue(scope *Scope, expression *expr.Expr, c *Context) Result {
	var children []*expr.Expr
	for i := range expression.Parts {
		if expression.Parts[i].Expr != nil {
			children = append(children, expression.Parts[i].Expr)
		}
	}
	operands := p.argumentValues(scope, children, c, expression.Span)
	mem.FreeSlice(nil, children)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	defer freeValuesWithCallables(c.Run, operands.Values)
	text, pattern := strings.Builder{}, strings.Builder{}
	defer text.Reset()
	defer pattern.Reset()
	var instantiated *expr.Pattern
	defer instantiated.Free(c.Run)
	next := 0
	characterClass, escaped := false, false
	for i := range expression.Parts {
		part := expression.Parts[i]
		if part.Expr == nil {
			expr.AppendPathText(&text, part.Text, false)
			if expression.PathGlob {
				pattern.WriteString(part.Text)
			} else {
				expr.AppendPathText(&pattern, part.Text, true)
			}
			// Only authored bytes can open or close a glob character class.
			for j := 0; j < len(part.Text); j++ {
				b := part.Text[j]
				if escaped {
					escaped = false
					continue
				}
				if b == '\\' {
					escaped = true
					continue
				}
				if b == '[' && !characterClass {
					characterClass = true
				} else if b == ']' && characterClass {
					characterClass = false
				}
			}
			continue
		}
		value := operands.Values[next]
		next++
		if !value.IsPathText() || strings.IndexByte(value.Text, 0) >= 0 {
			return failure(c.Run, "EXPR_INVALID", part.Span, "path interpolation requires text without NUL")
		}
		text.WriteString(value.Text)
		for j := 0; j < len(value.Text); j++ {
			b := value.Text[j]
			if expression.PathGlob && (b == '{' || b == '}') {
				pattern.WriteByte('\\')
			}
			if b == '*' || b == '?' || b == '[' || b == '\\' || (characterClass && (b == ']' || b == '-' || b == '!' || b == '^')) {
				pattern.WriteByte('\\')
			}
			pattern.WriteByte(b)
		}
	}
	name := text.String()
	if !(strings.HasPrefix(name, "/") || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "~/")) || strings.IndexByte(name, 0) >= 0 {
		return failure(c.Run, "EXPR_INVALID", expression.Span, "interpolated path requires an explicit /, ./, ../ or ~/ root and no NUL")
	}
	if expression.Pattern != nil {
		instantiated = expression.Pattern.Clone(c.Run)
		for i := range instantiated.Parts {
			part := &instantiated.Parts[i]
			if part.Kind != expr.PatternInterpolation {
				continue
			}
			part.Kind = expr.PatternLiteral
			part.Text = owned(c.Run, operands.Values[part.Index].Text)
		}
		return Result{Value: pathPatternValue(c.Run, instantiated)}
	}
	if expression.PathGlob {
		value := core.NewString(c.Run, pattern.String())
		value.Kind = core.PathPattern
		return Result{Value: value}
	}
	value := core.NewPath(c.Run, name)
	value.GlobPattern = owned(c.Run, pattern.String())
	return Result{Value: value}
}
