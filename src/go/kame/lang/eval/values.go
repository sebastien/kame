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
		if !ok {
			freeCallables(context.Run, &r.Value)
			r.Value.Free(context.Run)
			return failure(context.Run, "EXPR_INVALID", parts[i].Span, "records and bytes require explicit text conversion")
		}
		r.Value.Free(context.Run)
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

// Display renders a value exactly as the CLI writes a result to stdout using
// the canonical value notation of docs/spec/005-evaluation.md: nil and booleans
// as :nil/:true/:false, strings and patterns as quoted literals, ints and
// floats numerically, and lists and records bracketed. The notation is a subset
// of the expression grammar, so evaluating it reproduces a value that displays
// identically. Hosts use it so a completed value matches native output
// byte-for-byte.
func Display(a mem.Allocator, value core.Value) string {
	var out []byte
	appendDisplay(a, &out, value)
	result := owned(a, string(out))
	slices.Free(a, out)
	return result
}

func appendDisplay(a mem.Allocator, out *[]byte, value core.Value) {
	switch value.Kind {
	case core.String, core.Pattern:
		*out = appendQuotedText(a, *out, value.Text)
	case core.Bytes:
		*out = appendTextBytes(a, *out, string(value.Bytes))
	case core.Bool:
		if value.Bool {
			*out = appendTextBytes(a, *out, ":true")
		} else {
			*out = appendTextBytes(a, *out, ":false")
		}
	case core.Int:
		var buffer [strconv.MaxIntBase10Len]byte
		*out = appendTextBytes(a, *out, strconv.FormatInt(buffer[:], value.Int, 10))
	case core.Float:
		var buffer [strconv.MaxFloat64Len]byte
		text := strconv.FormatFloat(buffer[:], value.Float, 'g', -1, 64)
		// Negative zero parses back as the integer 0, so fold it to keep the
		// display a fixed point under evaluation.
		if text == "-0" {
			text = "0"
		}
		*out = appendTextBytes(a, *out, text)
	case core.Nil:
		*out = appendTextBytes(a, *out, ":nil")
	case core.Resource:
		*out = appendTextBytes(a, *out, value.Resource.Name)
	case core.Process:
		// An opaque display marker is not a serialization of the handle.
		*out = appendTextBytes(a, *out, "<process>")
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

// appendQuotedText writes text as a double-quoted Kame string, escaping the
// characters the language recognises. It mirrors operations.quote so nested
// values read back as the string they hold.
func appendQuotedText(a mem.Allocator, out []byte, text string) []byte {
	out = slices.Append(a, out, '"')
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\\':
			out = slices.Append(a, out, '\\')
			out = slices.Append(a, out, text[i])
		case '\n':
			out = appendTextBytes(a, out, "\\n")
		case '\r':
			out = appendTextBytes(a, out, "\\r")
		case '\t':
			out = appendTextBytes(a, out, "\\t")
		default:
			out = slices.Append(a, out, text[i])
		}
	}
	return slices.Append(a, out, '"')
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
