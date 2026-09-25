package operations

import (
	"littlemake/core"
	"littlemake/lang/eval"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func opJoin(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.List || v[1].Kind != core.String {
		return invalid()
	}
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	for i := range v[0].List {
		if v[0].List[i].Kind != core.String {
			return invalid()
		}
		if i != 0 {
			b.WriteString(v[1].Text)
		}
		b.WriteString(v[0].List[i].Text)
	}
	return eval.Result{Value: core.NewString(c.Run, b.String())}
}
func opSplit(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String || v[1].Text == "" {
		return invalid()
	}
	var out []core.Value
	for {
		cut := strings.Index(left, v[1].Text)
		if cut < 0 {
			out = slices.Append(c.Run, out, core.NewString(c.Run, left))
			break
		}
		out = slices.Append(c.Run, out, core.NewString(c.Run, left[:cut]))
		left = left[cut+len(v[1].Text):]
	}
	result := core.NewList(c.Run, out)
	freeValues(c, out)
	return eval.Result{Value: result}
}
func opStrip(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		return invalid()
	}
	return eval.Result{Value: core.NewString(c.Run, strings.TrimSpace(value))}
}
func opReplace(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.Pattern {
		// Legacy literal form: replace occurrences of one plain string.
		if len(v) != 3 || v[0].Kind != core.String || v[1].Kind != core.String || v[2].Kind != core.String {
			return failure("PAT_INVALID", "invalid replace arguments")
		}
		value := strings.ReplaceAll(c.Run, v[0].Text, v[1].Text, v[2].Text)
		return eval.Result{Value: core.Value{Kind: core.String, Text: value}}
	}
	var state *replaceState
	parsed := parseReplaceState(c, v[0], v[1], &state)
	if parsed.Diagnostic.Code != "" {
		return parsed
	}
	if len(v) == 2 {
		return replaceSection(c, state)
	}
	out := replaceApply(c, state, v[2])
	freeReplaceState(c.Run, state)
	return out
}
func opIncludes(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String {
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.Contains(left, v[1].Text)}}
}
func opStarts(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String {
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasPrefix(left, v[1].Text)}}
}
func opEnds(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String {
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(left, v[1].Text)}}
}
func opUppercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToUpper(c.Run, value)}}
}
func opLowercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToLower(c.Run, value)}}
}
