// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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

// Stringify returns Kame's canonical scalar/list rendering. It is exported for
// hosts that serialize a completed portable value without exposing Value's
// allocation-backed representation across an ABI boundary.
func Stringify(a mem.Allocator, value core.Value) (string, bool) {
	return stringify(a, value)
}

// Display renders a value exactly as the CLI writes a result to stdout: strings
// and patterns verbatim, bytes raw, nil as "nil", and lists and records
// bracketed. Hosts use it so a completed value matches native output
// byte-for-byte.
func Display(a mem.Allocator, value core.Value) string {
	var out []byte
	appendDisplay(a, &out, value)
	result := owned(a, string(out))
	if len(out) != 0 {
		slices.Free(a, out)
	}
	return result
}

func appendDisplay(a mem.Allocator, out *[]byte, value core.Value) {
	switch value.Kind {
	case core.String, core.Pattern:
		*out = appendTextBytes(a, *out, value.Text)
	case core.Bytes:
		*out = appendTextBytes(a, *out, string(value.Bytes))
	case core.Bool:
		if value.Bool {
			*out = appendTextBytes(a, *out, "true")
		} else {
			*out = appendTextBytes(a, *out, "false")
		}
	case core.Int:
		var buffer [strconv.MaxIntBase10Len]byte
		*out = appendTextBytes(a, *out, strconv.FormatInt(buffer[:], value.Int, 10))
	case core.Float:
		var buffer [strconv.MaxFloat64Len]byte
		*out = appendTextBytes(a, *out, strconv.FormatFloat(buffer[:], value.Float, 'g', -1, 64))
	case core.Nil:
		*out = appendTextBytes(a, *out, "nil")
	case core.Resource:
		*out = appendTextBytes(a, *out, value.Resource.Name)
	case core.List:
		*out = appendTextBytes(a, *out, "[")
		for i := range value.List {
			if i != 0 {
				*out = appendTextBytes(a, *out, " ")
			}
			appendDisplay(a, out, value.List[i])
		}
		*out = appendTextBytes(a, *out, "]")
	case core.Record:
		*out = appendTextBytes(a, *out, "[")
		for i := range value.Record {
			if i != 0 {
				*out = appendTextBytes(a, *out, " ")
			}
			*out = appendTextBytes(a, *out, value.Record[i].Key)
			*out = appendTextBytes(a, *out, ": ")
			appendDisplay(a, out, value.Record[i].Value)
		}
		*out = appendTextBytes(a, *out, "]")
	}
}

func appendTextBytes(a mem.Allocator, out []byte, text string) []byte {
	for i := 0; i < len(text); i++ {
		out = slices.Append(a, out, text[i])
	}
	return out
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
