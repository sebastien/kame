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
	if v[0].Kind != core.List {
		return invalidArgument(c, v, 0, "list of strings")
	}
	if v[1].Kind != core.String {
		return invalidArgument(c, v, 1, "string")
	}
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	for i := range v[0].List {
		if v[0].List[i].Kind != core.String {
			return c.InvalidElement(0, i, "string", v[0].List[i].Kind)
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
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	if v[1].Kind != core.String || v[1].Text == "" {
		return invalidArgument(c, v, 1, "non-empty string")
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
		return invalidArgument(c, v, 0, "string")
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
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	if v[1].Kind != core.String {
		return invalidArgument(c, v, 1, "string")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.Contains(left, v[1].Text)}}
}
func opStarts(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	if v[1].Kind != core.String {
		return invalidArgument(c, v, 1, "string")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasPrefix(left, v[1].Text)}}
}
func opEnds(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	left, ok := text(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	if v[1].Kind != core.String {
		return invalidArgument(c, v, 1, "string")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(left, v[1].Text)}}
}
func opUppercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToUpper(c.Run, value)}}
}
func opLowercase(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := text(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToLower(c.Run, value)}}
}

func opTerminalStyle(c *eval.Context, state any, v []core.Value) eval.Result {
	_ = state
	start, end := "", ""
	switch c.OperationName {
	case "black":
		start, end = "\x1b[30m", "\x1b[39m"
	case "red":
		start, end = "\x1b[31m", "\x1b[39m"
	case "green":
		start, end = "\x1b[32m", "\x1b[39m"
	case "yellow":
		start, end = "\x1b[33m", "\x1b[39m"
	case "blue":
		start, end = "\x1b[34m", "\x1b[39m"
	case "magenta":
		start, end = "\x1b[35m", "\x1b[39m"
	case "cyan":
		start, end = "\x1b[36m", "\x1b[39m"
	case "white":
		start, end = "\x1b[37m", "\x1b[39m"
	case "bold":
		start, end = "\x1b[1m", "\x1b[22m"
	case "dim":
		start, end = "\x1b[2m", "\x1b[22m"
	}
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	b.WriteString(start)
	b.WriteString(v[0].Text)
	b.WriteString(end)
	return eval.Result{Value: core.NewString(c.Run, b.String())}
}

func opCat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	for i := range v {
		text, ok := eval.Stringify(c.Run, v[i])
		if !ok {
			return invalidArgument(c, v, i, "text-renderable scalar or list")
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
			return c.InvalidOperation("argument 1 contains bytes that are not valid UTF-8")
		}
		// Copy bytes as a string; Bytes holds binary, String holds UTF-8 text.
		b := mem.AllocSlice[byte](c.Run, len(v[0].Bytes), len(v[0].Bytes))
		copy(b, v[0].Bytes)
		return eval.Result{Value: core.Value{Kind: core.String, Text: string(b)}}
	}
	return invalidArgument(c, v, 0, "string or UTF-8 bytes")
}
