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
		value := operands.Values[next]
		next++
		text, ok := stringify(context.Run, value)
		if !ok {
			return renderFailure(context.Run, parts[i].Expr, parts[i].Span, value.Kind)
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
	mem.FreeSlice(nil, out)
	return result
}

func appendDisplay(a mem.Allocator, out *[]byte, value core.Value) {
	switch value.Kind {
	case core.Module:
		*out = appendTextBytes(a, *out, "<kash-module>")
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

		*out = appendTextBytes(a, *out, strconv.FormatInt(value.Int, 10))
	case core.Float:

		text := strconv.FormatFloat(value.Float, 'g', -1, 64)
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
	case core.Callable:
		if value.CallableOwner != nil {
			function := value.Callable.(*Function)
			if function.Kind == FunctionOperation {
				*out = appendTextBytes(a, *out, "<operation:")
				*out = appendTextBytes(a, *out, function.OperationName)
				*out = appendTextBytes(a, *out, ">")
			} else if value.Text == "kash" {
				*out = appendTextBytes(a, *out, "<kash-constructor>")
			} else if value.Text == "kame" {
				*out = appendTextBytes(a, *out, "kame")
			} else {
				*out = appendTextBytes(a, *out, "<kash-script>")
			}
		}
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
		out = append(out, text[i])
	}
	return out
}

// appendQuotedText writes text as a double-quoted Kame string, escaping the
// characters the language recognises. It mirrors operations.quote so nested
// values read back as the string they hold.
func appendQuotedText(a mem.Allocator, out []byte, text string) []byte {
	out = append(out, '"')
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\\':
			out = append(out, '\\')
			out = append(out, text[i])
		case '\n':
			out = appendTextBytes(a, out, "\\n")
		case '\r':
			out = appendTextBytes(a, out, "\\r")
		case '\t':
			out = appendTextBytes(a, out, "\\t")
		default:
			out = append(out, text[i])
		}
	}
	return append(out, '"')
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
