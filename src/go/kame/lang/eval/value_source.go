package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"math"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FormatValueSource returns allocator-owned expression source or the reason
// why a value cannot be represented by an expression.
func FormatValueSource(a mem.Allocator, value core.Value) (string, string) {
	var out strings.Builder
	if invalid := writeValueSource(a, &out, value, false); invalid != "" {
		return "", invalid
	}
	return owned(a, out.String()), ""
}

// Display uses fmt's canonical source spelling for representable values and
// the CLI's presentation for runtime-only values.
func Display(a mem.Allocator, value core.Value) string {
	var out strings.Builder
	writeValueSource(a, &out, value, true)
	return owned(a, out.String())
}

// writeValueSource returns the kind of the first unrepresentable value, if any.
func writeValueSource(a mem.Allocator, out *strings.Builder, value core.Value, display bool) string {
	switch value.Kind {
	case core.Nil:
		out.WriteString("nil")
	case core.Bool:
		out.WriteString(strconv.FormatBool(value.Bool))
	case core.Int:
		out.WriteString(strconv.FormatInt(value.Int, 10))
	case core.Float:
		number := strconv.FormatFloat(value.Float, 'g', -1, 64)
		if math.IsInf(value.Float, 0) || math.IsNaN(value.Float) {
			if !display {
				return "non-finite float"
			}
			out.WriteString(number)
			return ""
		}
		out.WriteString(number)
		if !strings.ContainsAny(number, ".eE") {
			out.WriteString(".0")
		}
	case core.String:
		if !display && !utf8.ValidString(value.Text) {
			return "non-UTF-8 string"
		}
		writeValueString(out, value.Text)
	case core.Symbol:
		out.WriteByte(':')
		out.WriteString(value.Text)
	case core.Path:
		if !writeValuePath(out, value.Text) {
			if !display {
				return "path"
			}
			out.WriteString(value.Text)
		}
	case core.PathPattern, core.PathTemplate:
		if isValuePatternAtom(a, value) {
			out.WriteString(value.Text)
		} else {
			out.WriteString("(std/pattern ")
			writeValueString(out, value.Text)
			out.WriteByte(')')
		}
	case core.List:
		out.WriteByte('[')
		for i, item := range value.List {
			if i != 0 {
				out.WriteString(", ")
			}
			if invalid := writeValueSource(a, out, item, display); invalid != "" {
				return invalid
			}
		}
		out.WriteByte(']')
	case core.Record:
		var keys map[string]struct{}
		if !display && len(value.Record) > 1 {
			keys = make(map[string]struct{}, len(value.Record))
		}
		out.WriteByte('{')
		for i, field := range value.Record {
			if i != 0 {
				out.WriteString(", ")
			}
			if keys != nil {
				if _, duplicate := keys[field.Key]; duplicate {
					return "record with duplicate keys"
				}
				keys[field.Key] = struct{}{}
			}
			if !display && !utf8.ValidString(field.Key) {
				return "record with a non-UTF-8 key"
			}
			if expr.ValidBindingName(field.Key) {
				out.WriteString(field.Key)
			} else {
				writeValueString(out, field.Key)
			}
			out.WriteString(": ")
			if invalid := writeValueSource(a, out, field.Value, display); invalid != "" {
				return invalid
			}
		}
		out.WriteByte('}')
	default:
		if !display {
			return core.KindName(value.Kind)
		}
		writeRuntimeValue(out, value)
	}
	return ""
}

// These values have no round-trippable expression spelling.
func writeRuntimeValue(out *strings.Builder, value core.Value) {
	switch value.Kind {
	case core.Module:
		out.WriteString("<kash-module>")
	case core.Pattern:
		writeValueString(out, value.Text)
	case core.Bytes:
		out.Write(value.Bytes)
	case core.Resource:
		out.WriteString(value.Resource.Name)
	case core.Rule:
		out.WriteString("<rule>")
	case core.Process:
		out.WriteString("<process>")
	case core.Stream:
		out.WriteString("<stream>")
	case core.Callable:
		if value.CallableOwner != nil {
			function := value.Callable.(*Function)
			switch {
			case function.Kind == FunctionOperation:
				out.WriteString("<operation:" + function.OperationName + ">")
			case value.Text == "kash":
				out.WriteString("<kash-constructor>")
			case value.Text == "kame":
				out.WriteString("kame")
			default:
				out.WriteString("<kash-script>")
			}
		}
	}
}

func isValuePatternAtom(a mem.Allocator, value core.Value) bool {
	text := value.Text
	if strings.ContainsAny(text, "\r\n") ||
		(!strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "./") &&
			!strings.HasPrefix(text, "../") && !strings.HasPrefix(text, "~/")) {
		return false
	}
	parsed := expr.ParsePathPatternText(a, text)
	defer parsed.Pattern.Free(a)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 {
		return false
	}
	if value.Kind == core.PathTemplate {
		return parsed.Pattern.References > 0
	}
	return parsed.Pattern.Matchers > 0 || parsed.Pattern.Globs > 0
}

func writeValueString(out *strings.Builder, text string) {
	out.WriteByte('"')
	for i := range len(text) {
		switch text[i] {
		case '"', '\\':
			out.WriteByte('\\')
		case '$':
			if i+1 < len(text) && text[i+1] == '{' {
				out.WriteByte('\\')
			}
		case '\r':
			out.WriteString("\\r")
			continue
		case '\n':
			out.WriteString("\\n")
			continue
		case '\t':
			out.WriteString("\\t")
			continue
		}
		out.WriteByte(text[i])
	}
	out.WriteByte('"')
}

func writeValuePath(out *strings.Builder, text string) bool {
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return false
	}
	for _, root := range []string{"../", "./", "~/", "/"} {
		if rest, ok := strings.CutPrefix(text, root); ok {
			out.WriteString(root)
			if strings.ContainsAny(rest, "\r\n") {
				out.WriteString("${")
				writeValueString(out, rest)
				out.WriteByte('}')
				return true
			}
			for i := range len(rest) {
				if strings.IndexByte(" \\[]{}(),;#*?\t", rest[i]) >= 0 {
					out.WriteByte('\\')
				}
				out.WriteByte(rest[i])
			}
			return true
		}
	}
	// Path operations can produce clean unrooted results. Joining a cancelling
	// pair yields the same spelling while retaining the Path kind.
	if text == "" || path.Clean(text) != text {
		return false
	}
	out.WriteString("(std/joinpath \"fmt-root\" ../${")
	writeValueString(out, text)
	out.WriteString("})")
	return true
}
