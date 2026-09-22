// Package lib registers LittleMake's portable pure operations.
package lib

import (
	"littlemake/core"
	"littlemake/host"
	"littlemake/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

const version = "v1"

// Register installs the portable operations that do not access the host.
func Register(r *eval.Registry) bool {
	return add(r, "not", opNot, 1, 1) && add(r, "bool", opBool, 1, 1) &&
		add(r, "str", opStr, 1, 1) && add(r, "count", opCount, 1, 1) &&
		add(r, "first", opFirst, 1, 1) && add(r, "nth", opNth, 2, 2) &&
		add(r, "apply", opApply, 2, 2) && add(r, "list", opList, 0, -1) &&
		add(r, "map", opMap, 2, 2) && add(r, "flatmap", opFlatMap, 2, 2) &&
		add(r, "filter", opFilter, 2, 2) && add(r, "filter-out", opFilterOut, 2, 2) &&
		add(r, "reduce", opReduce, 2, 3) && add(r, "concat", opConcat, 0, -1) &&
		add(r, "slice", opSlice, 2, 3) && add(r, "sorted", opSorted, 1, 1) &&
		add(r, "unique", opUnique, 1, 1) && add(r, "join", opJoin, 2, 2) &&
		add(r, "split", opSplit, 2, 2) && add(r, "strip", opStrip, 1, 1) &&
		add(r, "replace", opReplace, 3, 3) && add(r, "includes?", opIncludes, 2, 2) &&
		add(r, "starts?", opStarts, 2, 2) && add(r, "ends?", opEnds, 2, 2) &&
		add(r, "uppercase", opUppercase, 1, 1) && add(r, "lowercase", opLowercase, 1, 1) &&
		add(r, "basename", opBasename, 1, 1) && add(r, "dirname", opDirname, 1, 1) &&
		add(r, "splitext", opSplitext, 1, 1) && add(r, "ext", opExt, 1, 1) &&
		add(r, "joinpath", opJoinpath, 0, -1) && add(r, "relpath", opRelpath, 2, 2) &&
		add(r, "abspath", opAbspath, 1, 1) &&
		addCapability(r, "read", opRead, 1, 1, eval.Read) &&
		addCapability(r, "exists?", opExists, 1, 1, eval.Read) &&
		addCapability(r, "stat", opStat, 1, 1, eval.Read) &&
		addCapability(r, "wildcard", opWildcard, 1, 1, eval.Read) &&
		addCapability(r, "write", opWrite, 2, 2, eval.Write) &&
		addCapability(r, "env", opEnv, 1, 1, eval.Env) &&
		addCapability(r, "shell", opShell, 1, 2, eval.Run) &&
		add(r, "out", opOut, 1, -1) &&
		add(r, "err", opErr, 1, -1) && add(r, "yield", opYield, 1, -1) &&
		add(r, "nop", opNop, 0, -1)
}

func add(r *eval.Registry, name string, call eval.OperationCall, min int, max int) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Version: version})
}
func addCapability(r *eval.Registry, name string, call eval.OperationCall, min int, max int, capability eval.Capability) bool { return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Capabilities: []eval.Capability{capability}, Version: version}) }

func truth(v core.Value) bool { return v.Kind != core.Nil && !(v.Kind == core.Bool && !v.Bool) }
func invalid() eval.Result { return eval.Result{Diagnostic: core.Diagnostic{Code: "EXPR_INVALID"}} }
func text(v core.Value) (string, bool) { if v.Kind != core.String { return "", false }; return v.Text, true }

func opNot(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; return eval.Result{Value: core.Value{Kind: core.Bool, Bool: !truth(v[0])}} }
func opBool(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; return eval.Result{Value: core.Value{Kind: core.Bool, Bool: truth(v[0])}} }

func opStr(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := stringValue(c.Run, v[0], false)
	if !ok { return invalid() }
	result := eval.Result{Value: core.NewString(c.Run, value)}
	mem.FreeString(c.Run, value)
	return result
}

func stringValue(a mem.Allocator, v core.Value, quoted bool) (string, bool) {
	b := strings.NewBuilder(a)
	defer b.Free()
	if v.Kind == core.Nil { b.WriteString("nil")
	} else if v.Kind == core.Bool { if v.Bool { b.WriteString("true") } else { b.WriteString("false") }
	} else if v.Kind == core.Int { var buf [strconv.MaxIntBase10Len]byte; b.WriteString(strconv.FormatInt(buf[:], v.Int, 10))
	} else if v.Kind == core.Float { var buf [strconv.MaxFloat64Len]byte; b.WriteString(strconv.FormatFloat(buf[:], v.Float, 'g', -1, 64))
	} else if v.Kind == core.String { if quoted { quote(&b, v.Text) } else { b.WriteString(v.Text) }
	} else if v.Kind == core.List {
		b.WriteByte('[')
		for i := range v.List { if i != 0 { b.WriteByte(',') }; item, ok := stringValue(a, v.List[i], true); if !ok { return "", false }; b.WriteString(item); mem.FreeString(a, item) }
		b.WriteByte(']')
	} else if v.Kind == core.Record {
		b.WriteByte('{')
		for i := 0; i < len(v.Record); i++ { selected := -1; for j := range v.Record { rank := 0; for k := range v.Record { if strings.Compare(v.Record[k].Key, v.Record[j].Key) < 0 { rank++ } }; if rank == i { selected = j; break } }; if i != 0 { b.WriteByte(',') }; quote(&b, v.Record[selected].Key); b.WriteByte(':'); item, ok := stringValue(a, v.Record[selected].Value, true); if !ok { return "", false }; b.WriteString(item); mem.FreeString(a, item) }
		b.WriteByte('}')
	} else { return "", false }
	return strings.Clone(a, b.String()), true
}

func quote(b *strings.Builder, value string) {
	b.WriteByte('"')
	for i := range value { if value[i] == '"' || value[i] == '\\' { b.WriteByte('\\') }; if value[i] == '\n' { b.WriteString("\\n") } else if value[i] == '\r' { b.WriteString("\\r") } else if value[i] == '\t' { b.WriteString("\\t") } else { b.WriteByte(value[i]) } }
	b.WriteByte('"')
}

func opCount(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s; n := 0
	if v[0].Kind == core.String { n = utf8.RuneCountInString(v[0].Text) } else if v[0].Kind == core.Bytes { n = len(v[0].Bytes) } else if v[0].Kind == core.List { n = len(v[0].List) } else if v[0].Kind == core.Record { n = len(v[0].Record) } else { return invalid() }
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(n)}}
}
func opFirst(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.List { return invalid() }; if len(v[0].List) == 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }; return eval.Result{Value: v[0].List[0].Clone(c.Run)} }
func opNth(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s; if v[1].Kind != core.Int { return invalid() }; index := int(v[1].Int)
	if v[0].Kind == core.List { if index < 0 { index += len(v[0].List) }; if index < 0 || index >= len(v[0].List) { return eval.Result{Value: core.Value{Kind: core.Nil}} }; return eval.Result{Value: v[0].List[index].Clone(c.Run)} }
	if v[0].Kind != core.String { return invalid() }; if index < 0 { index += utf8.RuneCountInString(v[0].Text) }; start, end := runeOffset(v[0].Text, index), runeOffset(v[0].Text, index+1); if start < 0 || end < 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }; return eval.Result{Value: core.NewString(c.Run, v[0].Text[start:end])}
}
func runeOffset(text string, index int) int { if index < 0 { return -1 }; offset := 0; for i := 0; i < index; i++ { if offset == len(text) { return -1 }; _, width := utf8.DecodeRuneInString(text[offset:]); offset += width }; return offset }
func opApply(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.Callable || v[1].Kind != core.List { return invalid() }; defer c.FreeCallable(&v[0]); return c.Call(v[0], v[1].List) }
func opList(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return eval.Result{Value: core.NewList(c.Run, v)} }
func opNop(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if len(v) == 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }; return eval.Result{Value: v[len(v)-1].Clone(c.Run)} }

func opMap(c *eval.Context, s any, v []core.Value) eval.Result { return transform(c, s, v, false) }
func opFlatMap(c *eval.Context, s any, v []core.Value) eval.Result { return transform(c, s, v, true) }
type callbackState struct { Index int; Values []core.Value; Accumulator core.Value; HasAccumulator bool; Tracked bool }
func freeCallbackState(a mem.Allocator, value any) { state := value.(*callbackState); for i := range state.Values { state.Values[i].Free(a) }; slices.Free(a, state.Values); if state.HasAccumulator { state.Accumulator.Free(a) }; mem.Free(a, state) }
func callbackProgress(c *eval.Context) *callbackState { existing := c.OperationState(); if existing != nil { return existing.(*callbackState) }; state := mem.Alloc[callbackState](c.Run); if c.Engine != nil { state.Tracked = true; c.SetOperationState(state, freeCallbackState) }; return state }
func discardCallback(c *eval.Context, state *callbackState) { if state.Tracked { c.ClearOperationState() } else { freeCallbackState(c.Run, state) } }
func finishCallback(c *eval.Context, state *callbackState) core.Value { result := core.NewList(c.Run, state.Values); discardCallback(c, state); return result }
func transform(c *eval.Context, s any, v []core.Value, flatten bool) eval.Result {
	_ = s; if v[0].Kind != core.Callable || v[1].Kind != core.List { return invalid() }; defer c.FreeCallable(&v[0]); state := callbackProgress(c)
	for state.Index < len(v[1].List) { result := c.Call(v[0], v[1].List[state.Index:state.Index+1]); if result.Waiting { return result }; if result.Diagnostic.Code != "" { discardCallback(c, state); return result }; if flatten { if result.Value.Kind != core.List { result.Value.Free(c.Run); discardCallback(c, state); return invalid() }; for j := range result.Value.List { state.Values = slices.Append(c.Run, state.Values, result.Value.List[j].Clone(c.Run)) }; result.Value.Free(c.Run) } else { state.Values = slices.Append(c.Run, state.Values, result.Value.Clone(c.Run)); result.Value.Free(c.Run) }; state.Index++ }
	return eval.Result{Value: finishCallback(c, state)}
}
func opFilter(c *eval.Context, s any, v []core.Value) eval.Result { return filter(c, s, v, false) }
func opFilterOut(c *eval.Context, s any, v []core.Value) eval.Result { return filter(c, s, v, true) }
func filter(c *eval.Context, s any, v []core.Value, invert bool) eval.Result {
	_ = s; if v[0].Kind != core.Callable || v[1].Kind != core.List { return invalid() }; defer c.FreeCallable(&v[0]); state := callbackProgress(c)
	for state.Index < len(v[1].List) { result := c.Call(v[0], v[1].List[state.Index:state.Index+1]); if result.Waiting { return result }; if result.Diagnostic.Code != "" { discardCallback(c, state); return result }; keep := truth(result.Value); result.Value.Free(c.Run); if keep != invert { state.Values = slices.Append(c.Run, state.Values, v[1].List[state.Index].Clone(c.Run)) }; state.Index++ }
	return eval.Result{Value: finishCallback(c, state)}
}
func opReduce(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s; if v[0].Kind != core.Callable || v[1].Kind != core.List { return invalid() }; defer c.FreeCallable(&v[0]); state := callbackProgress(c)
	if !state.HasAccumulator { state.HasAccumulator = true; if len(v) == 3 { state.Accumulator = v[2].Clone(c.Run) } else if len(v[1].List) != 0 { state.Accumulator, state.Index = v[1].List[0].Clone(c.Run), 1 } else { state.Accumulator = core.Value{Kind: core.Nil} } }
	for state.Index < len(v[1].List) { args := []core.Value{state.Accumulator, v[1].List[state.Index]}; result := c.Call(v[0], args); if result.Waiting { return result }; state.Accumulator.Free(c.Run); if result.Diagnostic.Code != "" { discardCallback(c, state); return result }; state.Accumulator = result.Value; state.Index++ }
	result := state.Accumulator.Clone(c.Run); discardCallback(c, state); return eval.Result{Value: result}
}
func opConcat(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; var out []core.Value; for i := range v { if v[i].Kind == core.List { for j := range v[i].List { out = slices.Append(c.Run, out, v[i].List[j].Clone(c.Run)) } } else { out = slices.Append(c.Run, out, v[i].Clone(c.Run)) } }; result := core.NewList(c.Run, out); freeValues(c, out); return eval.Result{Value: result} }
func opSlice(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s; if v[1].Kind != core.Int || (len(v) == 3 && v[2].Kind != core.Int) { return invalid() }; length := 0; if v[0].Kind == core.List { length = len(v[0].List) } else if v[0].Kind == core.String { length = utf8.RuneCountInString(v[0].Text) } else { return invalid() }; start, end := int(v[1].Int), length; if len(v) == 3 { end = int(v[2].Int) }; if start < 0 { start += length }; if end < 0 { end += length }; if start < 0 || end < start || end > length { return invalid() }; if v[0].Kind == core.List { return eval.Result{Value: core.NewList(c.Run, v[0].List[start:end])} }; return eval.Result{Value: core.NewString(c.Run, v[0].Text[runeOffset(v[0].Text, start):runeOffset(v[0].Text, end)])}
}
func compare(left core.Value, right core.Value) int { if left.Kind != right.Kind { return 2 }; if left.Kind == core.Nil { return 0 }; if left.Kind == core.Bool { if left.Bool == right.Bool { return 0 }; if !left.Bool { return -1 }; return 1 }; if left.Kind == core.Int { if left.Int < right.Int { return -1 }; if left.Int > right.Int { return 1 }; return 0 }; if left.Kind == core.Float { if left.Float < right.Float { return -1 }; if left.Float > right.Float { return 1 }; return 0 }; if left.Kind == core.String { return strings.Compare(left.Text, right.Text) }; return 2 }
func opSorted(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.List { return invalid() }; out := core.NewList(c.Run, v[0].List); for i := 1; i < len(out.List); i++ { for j := i; j > 0; j-- { order := compare(out.List[j-1], out.List[j]); if order == 2 { out.Free(c.Run); return invalid() }; if order <= 0 { break }; out.List[j-1], out.List[j] = out.List[j], out.List[j-1] } }; return eval.Result{Value: out} }
func opUnique(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.List { return invalid() }; var out []core.Value; for i := range v[0].List { found := false; for j := range out { order := compare(out[j], v[0].List[i]); if order == 2 { freeValues(c, out); return invalid() }; if order == 0 { found = true; break } }; if !found { out = slices.Append(c.Run, out, v[0].List[i].Clone(c.Run)) } }; result := core.NewList(c.Run, out); freeValues(c, out); return eval.Result{Value: result} }

func opJoin(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.List || v[1].Kind != core.String { return invalid() }; b := strings.NewBuilder(c.Run); defer b.Free(); for i := range v[0].List { if v[0].List[i].Kind != core.String { return invalid() }; if i != 0 { b.WriteString(v[1].Text) }; b.WriteString(v[0].List[i].Text) }; return eval.Result{Value: core.NewString(c.Run, b.String())} }
func opSplit(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; left, ok := text(v[0]); if !ok || v[1].Kind != core.String || v[1].Text == "" { return invalid() }; var out []core.Value; for { cut := strings.Index(left, v[1].Text); if cut < 0 { out = slices.Append(c.Run, out, core.NewString(c.Run, left)); break }; out = slices.Append(c.Run, out, core.NewString(c.Run, left[:cut])); left = left[cut+len(v[1].Text):] }; result := core.NewList(c.Run, out); freeValues(c, out); return eval.Result{Value: result} }
func opStrip(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; return eval.Result{Value: core.NewString(c.Run, strings.TrimSpace(value))} }
func opReplace(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; left, ok := text(v[0]); if !ok || v[1].Kind != core.String || v[2].Kind != core.String { return invalid() }; value := strings.ReplaceAll(c.Run, left, v[1].Text, v[2].Text); return eval.Result{Value: core.Value{Kind: core.String, Text: value}} }
func opIncludes(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; left, ok := text(v[0]); if !ok || v[1].Kind != core.String { return invalid() }; return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.Contains(left, v[1].Text)}} }
func opStarts(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; left, ok := text(v[0]); if !ok || v[1].Kind != core.String { return invalid() }; return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasPrefix(left, v[1].Text)}} }
func opEnds(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; left, ok := text(v[0]); if !ok || v[1].Kind != core.String { return invalid() }; return eval.Result{Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(left, v[1].Text)}} }
func opUppercase(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToUpper(c.Run, value)}} }
func opLowercase(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; return eval.Result{Value: core.Value{Kind: core.String, Text: strings.ToLower(c.Run, value)}} }

func opBasename(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; value, ok := text(v[0]); if !ok { return invalid() }; return eval.Result{Value: core.NewString(c.Run, path.Base(value))} }
func opDirname(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; result := path.Dir(c.Run, value); return eval.Result{Value: core.Value{Kind: core.String, Text: result}} }
func extension(value string) string { base := path.Base(value); if len(base) != 0 && base[0] == '.' && strings.Index(base[1:], ".") < 0 { return "" }; return path.Ext(value) }
func opSplitext(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; suffix := extension(value); root := value[:len(value)-len(suffix)]; result := []core.Value{core.NewString(c.Run, root), core.NewString(c.Run, suffix)}; out := core.NewList(c.Run, result); freeValues(c, result); return eval.Result{Value: out} }
func opExt(c *eval.Context, s any, v []core.Value) eval.Result { _, _ = c, s; value, ok := text(v[0]); if !ok { return invalid() }; return eval.Result{Value: core.NewString(c.Run, extension(value))} }
func opJoinpath(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; var values []string; for i := range v { if v[i].Kind != core.String { slices.Free(c.Run, values); return invalid() }; values = slices.Append(c.Run, values, v[i].Text) }; joined := path.Join(c.Run, values...); slices.Free(c.Run, values); return eval.Result{Value: core.Value{Kind: core.String, Text: joined}} }
func opAbspath(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; value, ok := text(v[0]); if !ok { return invalid() }; if path.IsAbs(value) { return eval.Result{Value: core.Value{Kind: core.String, Text: path.Clean(c.Run, value)}} }; return eval.Result{Value: core.Value{Kind: core.String, Text: path.Join(c.Run, c.Cwd, value)}} }
func opRelpath(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s; target, ok := text(v[0]); if !ok || v[1].Kind != core.String { return invalid() }; base := path.Join(c.Run, c.Cwd, v[1].Text); absolute := path.Join(c.Run, c.Cwd, target); defer mem.FreeString(c.Run, base); defer mem.FreeString(c.Run, absolute); start := commonPathPrefix(base, absolute); b := strings.NewBuilder(c.Run); defer b.Free(); for i := start; i < len(base); { for i < len(base) && base[i] == '/' { i++ }; if i == len(base) { break }; for i < len(base) && base[i] != '/' { i++ }; if b.Len() != 0 { b.WriteByte('/') }; b.WriteString("..") }; suffix := absolute[start:]; for len(suffix) != 0 && suffix[0] == '/' { suffix = suffix[1:] }; if suffix != "" { if b.Len() != 0 { b.WriteByte('/') }; b.WriteString(suffix) }; if b.Len() == 0 { b.WriteByte('.') }; return eval.Result{Value: core.NewString(c.Run, b.String())}
}
func commonPathPrefix(left string, right string) int { i, last := 0, 0; for i < len(left) && i < len(right) && left[i] == right[i] { if left[i] == '/' { last = i + 1 }; i++ }; if i == len(left) && i == len(right) { return i }; return last }

func request(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	completion := c.TakeCompletion()
	if completion.RequestID != 0 {
		// Resume does not submit, so the freshly built payload is still owned here.
		payload.Free(c.Run)
		if completion.Diagnostic.Code != "" { result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}; completion.Diagnostic.Free(c.Run); return result }
		if !completion.HasValue { return invalid() }
		result := eval.Result{Value: completion.Value.Clone(c.Run)}
		completion.Value.Free(c.Run)
		return result
	}
	id := c.Submit(kind, payload)
	payload.Free(c.Run)
	if id == 0 { return eval.Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}} }
	return eval.Result{Waiting: true}
}
func dependency(c *eval.Context, kind core.ResourceKind, name string) bool { key := core.NewResourceKey(c.Run, kind, name); current := c.Dependency(key); key.Free(c.Run); return current }
func fileRequest(c *eval.Context, op string, value core.Value) eval.Result {
	if value.Kind != core.String { return invalid() }
	kind := core.ResourceFile
	if op == host.OpWildcard { kind = core.ResourceGlob }
	if !dependency(c, kind, value.Text) { return eval.Result{Waiting: true} }
	return request(c, host.RequestReadFile, host.FilePayload(c.Run, op, value.Text))
}
func opRead(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return fileRequest(c, host.OpRead, v[0]) }
func opExists(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return fileRequest(c, host.OpExists, v[0]) }
func opStat(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return fileRequest(c, host.OpStat, v[0]) }
func opWildcard(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return fileRequest(c, host.OpWildcard, v[0]) }
func opWrite(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.String || (v[1].Kind != core.String && v[1].Kind != core.Bytes) { return invalid() }; data := v[1].Bytes; if v[1].Kind == core.String { data = []byte(v[1].Text) }; if c.Phase == eval.RenderingPhase { c.EmitWrite(v[0].Text, data); return eval.Result{Value: core.Value{Kind: core.Nil}} }; if c.Phase == eval.PlanningPhase { c.MarkPhaseInvalid(); return eval.Result{Diagnostic: core.Diagnostic{Code: "PHASE_INVALID"}} }; return request(c, host.RequestWriteFile, host.WritePayload(c.Run, v[0].Text, data)) }
func opEnv(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if v[0].Kind != core.String { return invalid() }; if !dependency(c, core.ResourceEnvironment, v[0].Text) { return eval.Result{Waiting: true} }; return request(c, host.RequestEnvironment, core.NewString(c.Run, v[0].Text)) }
func opShell(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; if c.Phase != eval.EvaluatePhase { c.MarkPhaseInvalid(); return eval.Result{Diagnostic: core.Diagnostic{Code: "PHASE_INVALID"}} }; if v[0].Kind != core.String || (len(v) == 2 && v[1].Kind != core.Record) { return invalid() }; return request(c, host.RequestProcess, host.ProcessPayload(c.Run, v[0].Text)) }

func opOut(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return effect(c, eval.EffectOut, v) }
func opErr(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return effect(c, eval.EffectErr, v) }
func opYield(c *eval.Context, s any, v []core.Value) eval.Result { _ = s; return effect(c, eval.EffectYield, v) }
func effect(c *eval.Context, kind eval.EffectKind, v []core.Value) eval.Result { for i := range v { if v[i].Kind == core.String { c.Emit(kind, []byte(v[i].Text)) } else if v[i].Kind == core.Bytes { c.Emit(kind, v[i].Bytes) } else { return invalid() } }; return eval.Result{Value: core.Value{Kind: core.Nil}} }
func freeValues(c *eval.Context, values []core.Value) { for i := range values { values[i].Free(c.Run) }; slices.Free(c.Run, values) }
