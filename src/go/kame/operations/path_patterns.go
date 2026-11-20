package operations

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"strconv"
	"strings"
)

func opCapture(c *eval.Context, _ any, values []core.Value) eval.Result {
	if values[0].Kind == core.PathTemplate {
		discardArgCallables(c, values)
		return ownedFailure(c.Run, "PAT_INVALID", "capture requires a matching pattern")
	}
	if values[0].Kind != core.PathPattern {
		return invalidArgument(c, values, 0, "path pattern")
	}
	if !values[1].IsPathText() {
		return invalidArgument(c, values, 1, "path or string")
	}
	parsed := parsePatternValue(c.Run, values[0])
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	defer parsed.Pattern.Free(c.Run)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.References != 0 {
		return ownedFailure(c.Run, "PAT_INVALID", "capture requires a matching pattern")
	}
	matched := parsed.Pattern.MatchText(c.Run, values[1].Text)
	defer mem.FreeSlice(nil, matched.Captures)
	if matched.Limited {
		return ownedFailure(c.Run, "PAT_LIMIT", "path pattern match exceeded its step budget")
	}
	if !matched.Matched {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	var fields []core.RecordField
	seen := make(map[string]bool)
	for i, name := range parsed.Pattern.Names {
		if name != "" && !seen[name] {
			seen[name] = true
			fields = append(fields, core.RecordField{Key: name, Value: core.NewString(c.Run, matched.Captures[i])})
		}
	}
	result := core.NewRecord(c.Run, fields)
	for _, field := range fields {
		field.Value.Free(c.Run)
	}
	mem.FreeSlice(nil, fields)
	return eval.Result{Value: result}
}

func opBind(c *eval.Context, _ any, values []core.Value) eval.Result {
	if values[0].Kind != core.PathTemplate {
		return invalidArgument(c, values, 0, "path template")
	}
	if values[1].Kind != core.Record {
		return invalidArgument(c, values, 1, "capture record")
	}
	parsed := parsePatternValue(c.Run, values[0])
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	defer parsed.Pattern.Free(c.Run)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.Matchers != 0 || parsed.Pattern.Globs != 0 {
		discardArgCallables(c, values)
		return ownedFailure(c.Run, "PAT_INVALID", "bind requires a reference template")
	}
	var result strings.Builder
	defer result.Reset()
	defer discardArgCallables(c, values)
	captures := make(map[string]core.Value, len(values[1].Record))
	for _, capture := range values[1].Record {
		captures[capture.Key] = capture.Value
	}
	for _, part := range parsed.Pattern.Parts {
		if part.Kind == expr.PatternLiteral {
			result.WriteString(part.Text)
			continue
		}
		if part.Text == "" {
			return missingReferenceFailure(c.Run, cloneText(c.Run, "_"+strconv.Itoa(part.Index)))
		}
		value, found := captures[part.Text]
		if !found {
			return missingReferenceFailure(c.Run, cloneText(c.Run, part.Text))
		}
		switch value.Kind {
		case core.String, core.Path, core.Int, core.Float:
			text, _ := eval.Stringify(c.Run, value)
			invalid := strings.ContainsRune(text, 0)
			result.WriteString(text)
			mem.FreeString(c.Run, text)
			if invalid {
				return c.InvalidOperation("capture values must not contain NUL")
			}
		default:
			return c.InvalidOperation("capture values must be strings, numbers, or paths")
		}
	}
	return eval.Result{Value: expansionValue(cloneText(c.Run, result.String()), isExplicitPath(values[0].Text))}
}

func wildcardPatternText(a mem.Allocator, value core.Value) (scan string, grouped bool, valid bool) {
	parsed := parsePatternValue(a, value)
	defer parsed.Pattern.Free(a)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.References != 0 || strings.ContainsRune(value.Text, 0) {
		return "", false, false
	}
	grouped = parsed.Pattern.Matchers != 0
	var glob strings.Builder
	defer glob.Reset()
	for _, part := range parsed.Pattern.Parts {
		// A recursive matcher can span components even when it is embedded
		// inside a component. The host's ** must occupy a whole component.
		if grouped && part.MayCrossSlash(a) {
			prefix := glob.String()
			return cloneText(a, prefix[:strings.LastIndexByte(prefix, '/')+1]+"**"), grouped, true
		}
		switch part.Kind {
		case expr.PatternGlob:
			if grouped {
				glob.WriteByte('*')
			} else {
				glob.WriteString(part.Text)
			}
		case expr.PatternMatcher:
			glob.WriteByte('*')
		case expr.PatternLiteral:
			for i := 0; i < len(part.Text); i++ {
				ch := part.Text[i]
				if strings.IndexByte("*?[]\\", ch) >= 0 {
					glob.WriteByte('\\')
				}
				glob.WriteByte(ch)
			}
		}
	}
	return cloneText(a, glob.String()), grouped, true
}
