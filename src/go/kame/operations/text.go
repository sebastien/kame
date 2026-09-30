package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func opJoin(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.List || v[1].Kind != core.String {
		freeArgCallables(c, v)
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
		freeArgCallables(c, v)
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
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.NewString(c.Run, strings.TrimSpace(value))}
}
func opReplace(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.Pattern {
		// Legacy literal form: replace occurrences of one plain string.
		if len(v) != 3 || v[0].Kind != core.String || v[1].Kind != core.String || v[2].Kind != core.String {
			freeArgCallables(c, v)
			return failure("PAT_INVALID", "invalid replace arguments")
		}
		value := strings.ReplaceAll(c.Run, v[0].Text, v[1].Text, v[2].Text)
		return eval.Result{Value: core.Value{Kind: core.String, Text: value}}
	}
	var state *replaceState
	parsed := parseReplaceState(c, v[0], v[1], &state)
	if parsed.Diagnostic.Code != "" {
		freeArgCallables(c, v)
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
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.Contains(left, v[1].Text)}}
}
func opStarts(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasPrefix(left, v[1].Text)}}
}
func opEnds(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok || v[1].Kind != core.String {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(left, v[1].Text)}}
}
func opUppercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToUpper(c.Run, value)}}
}
func opLowercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToLower(c.Run, value)}}
}

func opCat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	for i := range v {
		text, ok := eval.Stringify(c.Run, v[i])
		if !ok {
			freeArgCallables(c, v)
			return invalid()
		}
		b.WriteString(text)
		mem.FreeString(c.Run, text)
	}
	return eval.Result{Value: core.NewString(c.Run, b.String())}
}

func opText(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	switch v[0].Kind {
	case core.String:
		return eval.Result{Value: v[0].Clone(c.Run)}
	case core.Bytes:
		if !utf8.Valid(v[0].Bytes) {
			freeArgCallables(c, v)
			return invalid()
		}
		// Copy bytes as a string; Bytes holds binary, String holds UTF-8 text.
		b := mem.AllocSlice[byte](c.Run, len(v[0].Bytes), len(v[0].Bytes))
		copy(b, v[0].Bytes)
		return eval.Result{Value: core.Value{Kind: core.String, Text: string(b)}}
	}
	freeArgCallables(c, v)
	return invalid()
}
