package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func opMap(c *eval.Context, s any, v []core.Value) eval.Result     { return transform(c, s, v, false) }
func opFlatMap(c *eval.Context, s any, v []core.Value) eval.Result { return transform(c, s, v, true) }

type callbackState struct {
	Alloc          mem.Allocator
	Index          int
	Values         []core.Value
	Accumulator    core.Value
	HasAccumulator bool
	Tracked        bool
}

func freeCallbackState(a mem.Allocator, value any) {
	// The state remembers its own allocator: the direct (c.Run),
	// ClearOperationState, and generation-mismatch (Program.Alloc) paths
	// each hand in a different allocator, so none of them is reliable here.
	_ = a
	state := value.(*callbackState)
	a = state.Alloc
	for i := range state.Values {
		state.Values[i].Free(a)
	}
	slices.Free(a, state.Values)
	if state.HasAccumulator {
		state.Accumulator.Free(a)
	}
	mem.Free(a, state)
}
func callbackProgress(c *eval.Context) *callbackState {
	existing := c.OperationState()
	if existing != nil {
		return existing.(*callbackState)
	}
	state := mem.Alloc[callbackState](c.Run)
	state.Alloc = c.Run
	if c.Engine != nil {
		state.Tracked = true
		c.SetOperationState(state, freeCallbackState)
	}
	return state
}
func discardCallback(c *eval.Context, state *callbackState) {
	if state.Tracked {
		c.ClearOperationState()
	} else {
		freeCallbackState(c.Run, state)
	}
}
func finishCallback(c *eval.Context, state *callbackState) core.Value {
	result := core.NewList(c.Run, state.Values)
	discardCallback(c, state)
	return result
}
func transform(c *eval.Context, s any, v []core.Value, flatten bool) eval.Result {
	_ = s
	callback := &v[0]
	values := v[1].List
	if v[0].Kind != core.Callable {
		if v[1].Kind != core.Callable {
			freeArgCallables(c, v)
			return invalid()
		}
		callback = &v[1]
		if v[0].Kind == core.List { values = v[0].List } else { values = v[:1] }
	} else if v[1].Kind != core.List {
		freeArgCallables(c, v)
		return invalid()
	}
	defer c.FreeCallable(callback)
	state := callbackProgress(c)
	for state.Index < len(values) {
		result := c.Call(*callback, values[state.Index:state.Index+1])
		if result.Waiting {
			return result
		}
		if result.Diagnostic.Code != "" {
			discardCallback(c, state)
			return result
		}
		if flatten {
			if result.Value.Kind != core.List {
				// The callback returned a scalar: nothing shares it, so
				// release a bare wrapper before the shallow free.
				if result.Value.Kind == core.Callable {
					c.FreeCallable(&result.Value)
				}
				result.Value.Free(c.Run)
				discardCallback(c, state)
				return invalid()
			}
			for j := range result.Value.List {
				state.Values = slices.Append(c.Run, state.Values, result.Value.List[j].Clone(c.Run))
			}
			result.Value.Free(c.Run)
		} else {
			state.Values = slices.Append(c.Run, state.Values, result.Value.Clone(c.Run))
			result.Value.Free(c.Run)
		}
		state.Index++
	}
	return eval.Result{Value: finishCallback(c, state)}
}
func opFilter(c *eval.Context, s any, v []core.Value) eval.Result    { return filter(c, s, v, false) }
func opFilterOut(c *eval.Context, s any, v []core.Value) eval.Result { return filter(c, s, v, true) }
func filter(c *eval.Context, s any, v []core.Value, invert bool) eval.Result {
	_ = s
	callback := &v[0]
	values := v[1].List
	if v[0].Kind != core.Callable {
		if v[0].Kind != core.List || v[1].Kind != core.Callable {
			freeArgCallables(c, v)
			return invalid()
		}
		callback, values = &v[1], v[0].List
	} else if v[1].Kind != core.List {
		freeArgCallables(c, v)
		return invalid()
	}
	defer c.FreeCallable(callback)
	state := callbackProgress(c)
	for state.Index < len(values) {
		result := c.Call(*callback, values[state.Index:state.Index+1])
		if result.Waiting {
			return result
		}
		if result.Diagnostic.Code != "" {
			discardCallback(c, state)
			return result
		}
		keep := truth(result.Value)
		// truth discards nothing: release a bare callable wrapper here so
		// the shallow free below does not leak its scope retain.
		if result.Value.Kind == core.Callable {
			c.FreeCallable(&result.Value)
		}
		result.Value.Free(c.Run)
		if keep != invert {
			state.Values = slices.Append(c.Run, state.Values, values[state.Index].Clone(c.Run))
		}
		state.Index++
	}
	return eval.Result{Value: finishCallback(c, state)}
}
func opReduce(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.Callable || v[1].Kind != core.List {
		freeArgCallables(c, v)
		return invalid()
	}
	defer c.FreeCallable(&v[0])
	state := callbackProgress(c)
	if !state.HasAccumulator {
		state.HasAccumulator = true
		if len(v) == 3 {
			state.Accumulator = v[2].Clone(c.Run)
		} else if len(v[1].List) != 0 {
			state.Accumulator, state.Index = v[1].List[0].Clone(c.Run), 1
		} else {
			state.Accumulator = core.Value{Kind: core.Nil}
		}
	}
	for state.Index < len(v[1].List) {
		args := []core.Value{state.Accumulator, v[1].List[state.Index]}
		result := c.Call(v[0], args)
		if result.Waiting {
			return result
		}
		state.Accumulator.Free(c.Run)
		if result.Diagnostic.Code != "" {
			discardCallback(c, state)
			return result
		}
		state.Accumulator = result.Value
		state.Index++
	}
	result := state.Accumulator.Clone(c.Run)
	discardCallback(c, state)
	return eval.Result{Value: result}
}
func opConcat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	var out []core.Value
	for i := range v {
		if v[i].Kind == core.List {
			for j := range v[i].List {
				out = slices.Append(c.Run, out, v[i].List[j].Clone(c.Run))
			}
		} else {
			out = slices.Append(c.Run, out, v[i].Clone(c.Run))
		}
	}
	result := core.NewList(c.Run, out)
	freeValues(c, out)
	return eval.Result{Value: result}
}
func opSlice(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[1].Kind != core.Int || (len(v) == 3 && v[2].Kind != core.Int) {
		freeArgCallables(c, v)
		return invalid()
	}
	length := 0
	if v[0].Kind == core.List {
		length = len(v[0].List)
	} else if v[0].Kind == core.String {
		length = utf8.RuneCountInString(v[0].Text)
	} else {
		freeArgCallables(c, v)
		return invalid()
	}
	start, end := int(v[1].Int), length
	if len(v) == 3 {
		end = int(v[2].Int)
	}
	if start < 0 {
		start += length
	}
	if end < 0 {
		end += length
	}
	if start < 0 || end < start || end > length {
		return invalid()
	}
	if v[0].Kind == core.List {
		return eval.Result{Value: core.NewList(c.Run, v[0].List[start:end])}
	}
	return eval.Result{Value: core.NewString(c.Run, v[0].Text[runeOffset(v[0].Text, start):runeOffset(v[0].Text, end)])}
}
func compare(left core.Value, right core.Value) int {
	if left.Kind != right.Kind {
		return 2
	}
	if left.Kind == core.Nil {
		return 0
	}
	if left.Kind == core.Bool {
		if left.Bool == right.Bool {
			return 0
		}
		if !left.Bool {
			return -1
		}
		return 1
	}
	if left.Kind == core.Int {
		if left.Int < right.Int {
			return -1
		}
		if left.Int > right.Int {
			return 1
		}
		return 0
	}
	if left.Kind == core.Float {
		if left.Float < right.Float {
			return -1
		}
		if left.Float > right.Float {
			return 1
		}
		return 0
	}
	if left.Kind == core.String {
		return strings.Compare(left.Text, right.Text)
	}
	return 2
}
func opSorted(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.List {
		freeArgCallables(c, v)
		return invalid()
	}
	out := core.NewList(c.Run, v[0].List)
	for i := 1; i < len(out.List); i++ {
		for j := i; j > 0; j-- {
			order := compare(out.List[j-1], out.List[j])
			if order == 2 {
				out.Free(c.Run)
				return invalid()
			}
			if order <= 0 {
				break
			}
			out.List[j-1], out.List[j] = out.List[j], out.List[j-1]
		}
	}
	return eval.Result{Value: out}
}
func opUnique(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.List {
		freeArgCallables(c, v)
		return invalid()
	}
	var out []core.Value
	for i := range v[0].List {
		found := false
		for j := range out {
			order := compare(out[j], v[0].List[i])
			if order == 2 {
				freeValues(c, out)
				return invalid()
			}
			if order == 0 {
				found = true
				break
			}
		}
		if !found {
			out = slices.Append(c.Run, out, v[0].List[i].Clone(c.Run))
		}
	}
	result := core.NewList(c.Run, out)
	freeValues(c, out)
	return eval.Result{Value: result}
}
