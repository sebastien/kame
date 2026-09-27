// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

func (p *Program) stringValue(scope *Scope, parts []expr.StringPart, context *Context) Result {
	b := strings.NewBuilder(context.Run)
	defer b.Free()
	for i := range parts {
		if parts[i].Expr == nil {
			b.WriteString(parts[i].Text)
			continue
		}
		r := p.evaluate(context.Engine, scope, parts[i].Expr, context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		text, ok := stringify(context.Run, r.Value)
		r.Value.Free(context.Run)
		if !ok {
			return failure(context.Run, "EXPR_INVALID", parts[i].Span, "records and bytes require explicit text conversion")
		}
		b.WriteString(text)
		mem.FreeString(context.Run, text)
	}
	return Result{Value: core.NewString(context.Run, b.String())}
}

func stringify(a mem.Allocator, value core.Value) (string, bool) {
	if value.Kind == core.String {
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
		var buffer [strconv.MaxIntBase10Len]byte
		return owned(a, strconv.FormatInt(buffer[:], value.Int, 10)), true
	}
	if value.Kind == core.Float {
		var buffer [strconv.MaxFloat64Len]byte
		return owned(a, strconv.FormatFloat(buffer[:], value.Float, 'g', -1, 64)), true
	}
	if value.Kind == core.Pattern {
		return owned(a, value.Text), true
	}
	if value.Kind == core.List {
		builder := strings.NewBuilder(a)
		for i := range value.List {
			if i != 0 {
				builder.WriteByte(' ')
			}
			text, ok := stringify(a, value.List[i])
			if !ok {
				builder.Free()
				return "", false
			}
			builder.WriteString(text)
			mem.FreeString(a, text)
		}
		result := owned(a, builder.String())
		builder.Free()
		return result, true
	}
	return "", false
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
