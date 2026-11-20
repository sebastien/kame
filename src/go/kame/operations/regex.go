package operations

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
)

// regexCache keeps recently used regex-match patterns parsed and compiled.
// Scripts typically apply one pattern to many subjects; recompiling it per
// call dominated their matching cost. It belongs to one registry, whose
// evaluation is single-threaded, and allocates from the registry allocator.
type regexCache struct {
	alloc   mem.Allocator
	entries [16]regexCacheEntry
	next    int
}

type regexCacheEntry struct {
	kind     core.Kind
	text     string
	parsed   expr.PatternParse
	compiled expr.CompiledPattern
	valid    bool
}

func (e *regexCacheEntry) free(a mem.Allocator) {
	e.compiled.Free(a)
	e.parsed.Pattern.Free(a)
	mem.FreeSlice(nil, e.parsed.Diagnostics)
	mem.FreeString(a, e.text)
	*e = regexCacheEntry{}
}

func freeRegexCache(a mem.Allocator, value any) {
	cache := value.(*regexCache)
	for i := range cache.entries {
		cache.entries[i].free(cache.alloc)
	}
	mem.Free(a, cache)
}

func (r *regexCache) lookup(value core.Value) *regexCacheEntry {
	for i := range r.entries {
		if r.entries[i].parsed.Pattern != nil && r.entries[i].text == value.Text && r.entries[i].kind == value.Kind {
			return &r.entries[i]
		}
	}
	entry := &r.entries[r.next]
	r.next = (r.next + 1) % len(r.entries)
	entry.free(r.alloc)
	entry.text = core.NewString(r.alloc, value.Text).Text
	entry.kind = value.Kind
	entry.parsed = parsePatternValue(r.alloc, value)
	pattern := entry.parsed.Pattern
	entry.valid = len(entry.parsed.Diagnostics) == 0 && pattern.Matchers != 0 && pattern.References == 0 && hasRegexMatcher(pattern)
	if entry.valid {
		entry.compiled = expr.CompilePattern(r.alloc, pattern)
	}
	return entry
}

func opRegexMatch(c *eval.Context, state any, values []core.Value) eval.Result {
	if values[0].Kind != core.Pattern && values[0].Kind != core.PathPattern {
		return invalidArgument(c, values, 0, "pattern")
	}
	if !values[1].IsPathText() {
		return invalidArgument(c, values, 1, "string")
	}
	entry := state.(*regexCache).lookup(values[0])
	if !entry.valid {
		return ownedFailure(c.Run, "PAT_INVALID", "regex-match needs a regex match pattern")
	}
	parsed := entry.parsed
	var matched expr.MatchResult
	if entry.compiled.Valid {
		matched = entry.compiled.MatchText(c.Run, values[1].Text)
	} else {
		matched = parsed.Pattern.MatchText(c.Run, values[1].Text)
	}
	if matched.Limited {
		mem.FreeSlice(nil, matched.Captures)
		return ownedFailure(c.Run, "PAT_LIMIT", "regular-expression match exceeded its step budget")
	}
	if !matched.Matched {
		mem.FreeSlice(nil, matched.Captures)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return regexMatchRecord(c, parsed.Pattern, values[1].Text, matched.Captures)
}

func hasRegexMatcher(pattern *expr.Pattern) bool {
	for i := range pattern.Parts {
		if pattern.Parts[i].Kind == expr.PatternMatcher && pattern.Parts[i].Regex {
			return true
		}
	}
	return false
}

func regexMatchRecord(c *eval.Context, pattern *expr.Pattern, subject string, captures []string) eval.Result {
	values := make([]core.Value, len(captures))
	for i := range captures {
		values[i] = core.NewString(c.Run, captures[i])
	}
	captureList := core.NewList(c.Run, values)
	freeValues(c, values)

	var namedFields []core.RecordField
	for i := range pattern.Names {
		name := pattern.Names[i]
		if name == "" || i >= len(captures) {
			continue
		}
		duplicate := false
		for j := 0; j < i; j++ {
			if pattern.Names[j] == name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			namedFields = append(namedFields, core.RecordField{Key: name, Value: core.NewString(c.Run, captures[i])})
		}
	}
	named := core.NewRecord(c.Run, namedFields)
	for i := range namedFields {
		namedFields[i].Value.Free(c.Run)
	}
	mem.FreeSlice(nil, namedFields)

	fields := []core.RecordField{
		{Key: "text", Value: core.NewString(c.Run, subject)},
		{Key: "captures", Value: captureList},
		{Key: "named", Value: named},
	}
	result := core.NewRecord(c.Run, fields)
	for i := range fields {
		fields[i].Value.Free(c.Run)
	}
	mem.FreeSlice(nil, captures)
	return eval.Result{Value: result}
}

func opRegexGroup(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[1].Kind != core.Record {
		return invalidArgument(c, values, 1, "regex match record")
	}
	if values[0].Kind == core.Int {
		index := values[0].Int
		if index < 0 {
			return eval.Result{Value: core.Value{Kind: core.Nil}}
		}
		list := regexRecordValue(c.Run, values[1], "captures")
		if list.Kind != core.List {
			list.Free(c.Run)
			discardArgCallables(c, values)
			return c.InvalidOperation("regex-group expects a regex match record")
		}
		if index >= int64(len(list.List)) {
			list.Free(c.Run)
			return eval.Result{Value: core.Value{Kind: core.Nil}}
		}
		selected := list.List[index].Clone(c.Run)
		list.Free(c.Run)
		return eval.Result{Value: selected}
	}
	if values[0].Kind == core.String {
		named := regexRecordValue(c.Run, values[1], "named")
		if named.Kind != core.Record {
			named.Free(c.Run)
			discardArgCallables(c, values)
			return c.InvalidOperation("regex-group expects a regex match record")
		}
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
		if record.Record[i].Key == key {
			return record.Record[i].Value.Clone(a)
		}
	}
	return core.Value{Kind: core.Nil}
}

func opResub(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Pattern && values[0].Kind != core.PathPattern {
		return invalidArgument(c, values, 0, "pattern")
	}
	if !values[1].IsPathText() && values[1].Kind != core.PathTemplate && values[1].Kind != core.Callable {
		return invalidArgument(c, values, 1, "string or expansion pattern")
	}
	if !values[2].IsPathText() && values[2].Kind != core.List {
		return invalidArgument(c, values, 2, "string or list of strings")
	}
	var replace *replaceState
	parsed := parseReplaceState(c, values[0], values[1], &replace)
	if parsed.Diagnostic.Code != "" {
		discardArgCallables(c, values)
		return parsed
	}
	if !hasRegexMatcher(replace.match) {
		freeReplaceState(c.Run, replace)
		// parseReplaceState transferred the replacement's callable ownership.
		discardArgCallables(c, values[2:])
		return ownedFailure(c.Run, "PAT_INVALID", "resub needs a regex matcher")
	}
	result := replaceApply(c, replace, values[2])
	freeReplaceState(c.Run, replace)
	return result
}
