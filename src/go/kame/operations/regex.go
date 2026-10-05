package operations

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func opRegexMatch(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Pattern { return invalidArgument(c, values, 0, "pattern") }
	if values[1].Kind != core.String { return invalidArgument(c, values, 1, "string") }
	parsed := expr.ParsePatternText(c.Run, values[0].Text, 0)
	defer parsed.Pattern.Free(c.Run)
	defer slices.Free(c.Run, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.Matchers == 0 || parsed.Pattern.References != 0 || !hasRegexMatcher(parsed.Pattern) {
		return ownedFailure(c.Run, "PAT_INVALID", "regex-match needs a regex match pattern")
	}
	matched := parsed.Pattern.MatchText(c.Run, values[1].Text)
	if matched.Limited {
		slices.Free(c.Run, matched.Captures)
		return ownedFailure(c.Run, "PAT_LIMIT", "regular-expression match exceeded its step budget")
	}
	if !matched.Matched {
		slices.Free(c.Run, matched.Captures)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return regexMatchRecord(c, parsed.Pattern, values[1].Text, matched.Captures)
}

func hasRegexMatcher(pattern *expr.Pattern) bool {
	for i := range pattern.Parts {
		if pattern.Parts[i].Kind == expr.PatternMatcher && pattern.Parts[i].Regex { return true }
	}
	return false
}

func regexMatchRecord(c *eval.Context, pattern *expr.Pattern, subject string, captures []string) eval.Result {
	values := slices.Make[core.Value](c.Run, len(captures))
	for i := range captures { values[i] = core.NewString(c.Run, captures[i]) }
	captureList := core.NewList(c.Run, values)
	freeValues(c, values)

	var namedFields []core.RecordField
	for i := range pattern.Names {
		name := pattern.Names[i]
		if name == "" || i >= len(captures) { continue }
		duplicate := false
		for j := 0; j < i; j++ { if pattern.Names[j] == name { duplicate = true; break } }
		if !duplicate { namedFields = slices.Append(c.Run, namedFields, core.RecordField{Key: name, Value: core.NewString(c.Run, captures[i])}) }
	}
	named := core.NewRecord(c.Run, namedFields)
	for i := range namedFields { namedFields[i].Value.Free(c.Run) }
	slices.Free(c.Run, namedFields)

	fields := []core.RecordField{
		{Key: "text", Value: core.NewString(c.Run, subject)},
		{Key: "captures", Value: captureList},
		{Key: "named", Value: named},
	}
	result := core.NewRecord(c.Run, fields)
	for i := range fields { fields[i].Value.Free(c.Run) }
	slices.Free(c.Run, captures)
	return eval.Result{Value: result}
}

func opCapture(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[1].Kind != core.Record { return invalidArgument(c, values, 1, "regex match record") }
	if values[0].Kind == core.Int {
		index := values[0].Int
		if index < 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }
		list := regexRecordValue(c.Run, values[1], "captures")
		if list.Kind != core.List { list.Free(c.Run); return c.InvalidOperation("capture expects a regex match record") }
		if index >= int64(len(list.List)) { list.Free(c.Run); return eval.Result{Value: core.Value{Kind: core.Nil}} }
		selected := list.List[index].Clone(c.Run)
		list.Free(c.Run)
		return eval.Result{Value: selected}
	}
	if values[0].Kind == core.String {
		named := regexRecordValue(c.Run, values[1], "named")
		if named.Kind != core.Record { named.Free(c.Run); return c.InvalidOperation("capture expects a regex match record") }
		for i := range named.Record {
			if named.Record[i].Key == values[0].Text {
				selected := named.Record[i].Value.Clone(c.Run)
				named.Free(c.Run)
				return eval.Result{Value: selected}
			}
		}
		named.Free(c.Run)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return invalidArgument(c, values, 0, "integer index or string name")
}

func regexRecordValue(a mem.Allocator, record core.Value, key string) core.Value {
	for i := range record.Record {
		if record.Record[i].Key == key { return record.Record[i].Value.Clone(a) }
	}
	return core.Value{Kind: core.Nil}
}

func opRegexReplace(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Pattern { return invalidArgument(c, values, 0, "pattern") }
	if values[1].Kind != core.String && values[1].Kind != core.Pattern { return invalidArgument(c, values, 1, "string or expansion pattern") }
	if values[2].Kind != core.String && values[2].Kind != core.List { return invalidArgument(c, values, 2, "string or list of strings") }
	var replace *replaceState
	parsed := parseReplaceState(c, values[0], values[1], &replace)
	if parsed.Diagnostic.Code != "" { return parsed }
	if !hasRegexMatcher(replace.match) {
		freeReplaceState(c.Run, replace)
		return ownedFailure(c.Run, "PAT_INVALID", "regex-replace needs a regex matcher")
	}
	result := replaceApply(c, replace, values[2])
	freeReplaceState(c.Run, replace)
	return result
}
