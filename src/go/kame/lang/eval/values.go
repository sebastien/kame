package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"

	"strconv"
	"strings"
)

func (p *Program) stringValue(scope *Scope, expression *expr.Expr, context *Context) Result {
	parts := expression.Parts
	var children []*expr.Expr
	for i := range parts {
		if parts[i].Expr != nil {
			children = append(children, parts[i].Expr)
		}
	}
	operands := p.argumentValues(scope, children, context, expression.Span)
	mem.FreeSlice(nil, children)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	defer freeValuesWithCallables(context.Run, operands.Values)
	next := 0
	b := strings.Builder{}
	defer b.Reset()
	for i := range parts {
		if parts[i].Expr == nil {
			b.WriteString(parts[i].Text)
			continue
		}
		writeInterpolationText(context.Run, &b, operands.Values[next])
		next++
	}
	return Result{Value: core.NewString(context.Run, b.String())}
}

// writeInterpolationText renders a value into a Kame string or a Kash word.
func writeInterpolationText(a mem.Allocator, out *strings.Builder, value core.Value) {
	if value.Kind == core.List {
		for i, item := range value.List {
			if i != 0 {
				out.WriteByte(' ')
			}
			writeInterpolationText(a, out, item)
		}
		return
	}
	if text, ok := stringify(a, value); ok {
		out.WriteString(text)
		mem.FreeString(a, text)
		return
	}
	if value.Kind == core.Callable && value.CallableOwner == nil {
		out.WriteString("<callable>")
		return
	}
	writeValueSource(a, out, value, true)
}

// InterpolationText converts the scalar kinds accepted by template expressions.
func InterpolationText(a mem.Allocator, value core.Value) (string, bool) {
	switch value.Kind {
	case core.String, core.Symbol, core.Path, core.Nil, core.Bool, core.Int, core.Float:
		return stringify(a, value)
	default:
		return "", false
	}
}

func stringify(a mem.Allocator, value core.Value) (string, bool) {
	if value.IsPathText() || value.Kind == core.Symbol {
		return owned(a, value.Text), true
	}
	if value.Kind == core.Nil {
		return "", true
	}
	if value.Kind == core.Bool {
		if value.Bool {
			return owned(a, "true"), true
		}
		return owned(a, "false"), true
	}
	if value.Kind == core.Int {

		return owned(a, strconv.FormatInt(value.Int, 10)), true
	}
	if value.Kind == core.Float {

		return owned(a, strconv.FormatFloat(value.Float, 'g', -1, 64)), true
	}
	if value.Kind == core.Pattern {
		return owned(a, value.Text), true
	}
	if value.Kind == core.List {
		builder := strings.Builder{}
		for i := range value.List {
			if i != 0 {
				builder.WriteByte(' ')
			}
			text, ok := stringify(a, value.List[i])
			if !ok {
				builder.Reset()
				return "", false
			}
			builder.WriteString(text)
			mem.FreeString(a, text)
		}
		result := owned(a, builder.String())
		builder.Reset()
		return result, true
	}
	return "", false
}

// Stringify returns Kame's canonical scalar/list rendering. It is exported for
// hosts that serialize a completed portable value without exposing Value's
// allocation-backed representation across an ABI boundary.
func Stringify(a mem.Allocator, value core.Value) (string, bool) {
	return stringify(a, value)
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
