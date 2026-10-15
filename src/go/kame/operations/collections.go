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
	Effects        []eval.Effect
	WritePaths     []string
}

func freeCallbackState(a mem.Allocator, value any) {
	// The state remembers its own allocator: the direct (c.Run),
	// ClearOperationState, and generation-mismatch (Program.Alloc) paths
	// each hand in a different allocator, so none of them is reliable here.
	_ = a
	state := value.(*callbackState)
	a = state.Alloc
	for i := range state.Values {
		eval.ReleaseCallable(a, &state.Values[i])
		state.Values[i].Free(a)
	}
	slices.Free(a, state.Values)
	eval.FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	slices.Free(a, state.WritePaths)
	if state.HasAccumulator {
		eval.ReleaseCallable(a, &state.Accumulator)
		state.Accumulator.Free(a)
	}
	mem.Free(a, state)
}
func callbackProgress(c *eval.Context) *callbackState {
	existing := c.OperationState()
	if existing != nil {
		state := existing.(*callbackState)
		for i := range state.Effects {
			effect := state.Effects[i]
			effect.Data = slices.Clone(c.Run, effect.Data)
			c.Effects = slices.Append(c.Run, c.Effects, effect)
		}
		for i := range state.WritePaths {
			c.WritePaths = slices.Append(c.Run, c.WritePaths, cloneText(c.Run, state.WritePaths[i]))
		}
		return state
	}
	state := mem.Alloc[callbackState](c.Run)
	state.Alloc = c.Run
	if c.Engine != nil {
		state.Tracked = true
		c.SetOperationState(state, freeCallbackState)
	}
	return state
}

func rememberCallbackEffects(c *eval.Context, state *callbackState, effects int, paths int) {
	for i := effects; i < len(c.Effects); i++ {
		effect := c.Effects[i]
		effect.Data = slices.Clone(c.Run, effect.Data)
		state.Effects = slices.Append(c.Run, state.Effects, effect)
	}
	for i := paths; i < len(c.WritePaths); i++ {
		state.WritePaths = slices.Append(c.Run, state.WritePaths, cloneText(c.Run, c.WritePaths[i]))
	}
}
func discardCallback(c *eval.Context, state *callbackState) {
	if state.Tracked {
		c.ClearOperationState()
	} else {
		freeCallbackState(c.Run, state)
	}
}
func suspendCallback(c *eval.Context, state *callbackState) {
	// Without an engine state slot, retries restart the collection operation.
	if !state.Tracked {
		freeCallbackState(c.Run, state)
	}
}
func finishCallback(c *eval.Context, state *callbackState) core.Value {
	result := core.NewList(c.Run, state.Values)
	// Callable leaves transferred into result; cleanup now owns storage only.
	for i := range state.Values {
		state.Values[i].Free(state.Alloc)
	}
	slices.Free(state.Alloc, state.Values)
	state.Values = nil
	discardCallback(c, state)
	return result
}
func transform(c *eval.Context, s any, v []core.Value, flatten bool) eval.Result {
	_ = s
	callback := &v[0]
	values := v[1].List
	if v[0].Kind != core.Callable {
		return invalidArgument(c, v, 0, "callable")
	}
	if v[1].Kind != core.List {
		return invalidArgument(c, v, 1, "list")
	}
	defer discardArgCallables(c, v)
	state := callbackProgress(c)
	for state.Index < len(values) {
		effects, paths := len(c.Effects), len(c.WritePaths)
		result := c.CallAt(*callback, values[state.Index:state.Index+1], state.Index)
		if result.Waiting {
			suspendCallback(c, state)
			return result
		}
		if result.Diagnostic.Code != "" {
			discardCallback(c, state)
			return result
		}
		if flatten {
			if result.Value.Kind != core.List {
				d := c.InvalidOperation("callback must return list; got " + core.KindName(result.Value.Kind))
				// No callback-result ownership transfers on this rejection.
				c.DiscardCallables(&result.Value)
				result.Value.Free(c.Run)
				discardCallback(c, state)
				return d
			}
			for j := range result.Value.List {
				state.Values = slices.Append(c.Run, state.Values, result.Value.List[j].Clone(c.Run))
			}
			result.Value.Free(c.Run)
		} else {
			state.Values = slices.Append(c.Run, state.Values, result.Value.Clone(c.Run))
			result.Value.Free(c.Run)
		}
		rememberCallbackEffects(c, state, effects, paths)
		state.Index++
	}
	return eval.Result{Value: finishCallback(c, state)}
}
func opFilter(c *eval.Context, s any, v []core.Value) eval.Result    { return filter(c, s, v, false) }
func opFilterOut(c *eval.Context, s any, v []core.Value) eval.Result { return filter(c, s, v, true) }
func freeOptionalCallable(c *eval.Context, callback *core.Value) {
	if callback != nil {
		c.FreeCallable(callback)
	}
}
func filter(c *eval.Context, s any, v []core.Value, invert bool) eval.Result {
	_ = s
	defer discardArgCallables(c, v)
	var callback *core.Value
	var values []core.Value
	var target *core.Value
	if v[1].Kind != core.List {
		return invalidArgument(c, v, 1, "list")
	}
	values = v[1].List
	if v[0].Kind == core.Callable {
		callback = &v[0]
	} else {
		target = &v[0]
		if _, ok := compareEq(*target, *target); !ok {
			return invalidArgument(c, v, 0, "equality-comparable value")
		}
	}
	state := callbackProgress(c)
	for state.Index < len(values) {
		var keep bool
		if target != nil {
			var ok bool
			keep, ok = compareEq(values[state.Index], *target)
			if !ok {
				result := equalityFailure(c, values[state.Index], *target)
				discardCallback(c, state)
				discardArgCallables(c, v)
				return result
			}
		} else {
			effects, paths := len(c.Effects), len(c.WritePaths)
			result := c.CallAt(*callback, values[state.Index:state.Index+1], state.Index)
			if result.Waiting {
				suspendCallback(c, state)
				freeOptionalCallable(c, callback)
				return result
			}
			if result.Diagnostic.Code != "" {
				discardCallback(c, state)
				freeOptionalCallable(c, callback)
				return result
			}
			keep = truth(result.Value)
			// The truth result is discarded, including nested callable leaves.
			c.FreeCallable(&result.Value)
			result.Value.Free(c.Run)
			rememberCallbackEffects(c, state, effects, paths)
		}
		if keep != invert {
			state.Values = slices.Append(c.Run, state.Values, c.BorrowValue(values[state.Index]))
		}
		state.Index++
	}
	value := finishCallback(c, state)
	freeOptionalCallable(c, callback)
	return eval.Result{Value: value}
}
func opReduce(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	subject := len(v)-1
	if v[0].Kind != core.Callable {
		return invalidArgument(c, v, 0, "callable")
	}
	if v[subject].Kind != core.List {
		return invalidArgument(c, v, subject, "list")
	}
	defer discardArgCallables(c, v)
	state := callbackProgress(c)
	if !state.HasAccumulator {
		state.HasAccumulator = true
		if len(v) == 3 {
			state.Accumulator = c.BorrowValue(v[1])
		} else if len(v[subject].List) != 0 {
			state.Accumulator, state.Index = c.BorrowValue(v[subject].List[0]), 1
		} else {
			state.Accumulator = core.Value{Kind: core.Nil}
		}
	}
	for state.Index < len(v[subject].List) {
		args := []core.Value{state.Accumulator, v[subject].List[state.Index]}
		effects, paths := len(c.Effects), len(c.WritePaths)
		result := c.CallAt(v[0], args, state.Index)
		if result.Waiting {
			suspendCallback(c, state)
			return result
		}
		c.FreeCallable(&state.Accumulator)
		state.Accumulator.Free(c.Run)
		if result.Diagnostic.Code != "" {
			discardCallback(c, state)
			return result
		}
		state.Accumulator = result.Value
		rememberCallbackEffects(c, state, effects, paths)
		state.Index++
	}
	result := state.Accumulator.Clone(c.Run)
	state.Accumulator.Free(c.Run)
	state.HasAccumulator = false
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
	subject := len(v)-1
	if v[0].Kind != core.Int {
		return invalidArgument(c, v, 0, "int")
	}
	if len(v) == 3 && v[1].Kind != core.Int {
		return invalidArgument(c, v, 1, "int")
	}
	length := 0
	if v[subject].Kind == core.List {
		length = len(v[subject].List)
	} else if v[subject].Kind == core.String {
		length = utf8.RuneCountInString(v[subject].Text)
	} else {
		return invalidArgument(c, v, subject, "list or string")
	}
	// Normalize and validate int64 values before pointer-width conversion.
	wideStart, wideEnd := v[0].Int, int64(length)
	if len(v) == 3 {
		wideEnd = v[1].Int
	}
	if wideStart < 0 {
		wideStart += int64(length)
	}
	if wideEnd < 0 {
		wideEnd += int64(length)
	}
	if wideStart < 0 || wideEnd < wideStart || wideEnd > int64(length) {
		discardArgCallables(c, v)
		return c.InvalidOperation("slice bounds must satisfy 0 <= start <= end <= length after resolving negative indices")
	}
	start, end := int(wideStart), int(wideEnd)
	if v[subject].Kind == core.List {
		for i := range v[subject].List {
			if i < start || i >= end {
				c.FreeCallable(&v[subject].List[i])
			}
		}
		return eval.Result{Value: core.NewList(c.Run, v[subject].List[start:end])}
	}
	return eval.Result{Value: core.NewString(c.Run, v[subject].Text[runeOffset(v[subject].Text, start):runeOffset(v[subject].Text, end)])}
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
		return invalidArgument(c, v, 0, "list")
	}
	out := core.NewList(c.Run, v[0].List)
	for i := 1; i < len(out.List); i++ {
		for j := i; j > 0; j-- {
			order := compare(out.List[j-1], out.List[j])
			if order == 2 {
				out.Free(c.Run)
				discardArgCallables(c, v)
				return c.InvalidOperation("argument 1 must contain comparable values of the same kind (nil, bool, int, float, or string)")
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
		return invalidArgument(c, v, 0, "list")
	}
	var out []core.Value
	for i := range v[0].List {
		found := false
		for j := range out {
			order := compare(out[j], v[0].List[i])
			if order == 2 {
				freeValues(c, out)
				discardArgCallables(c, v)
				return c.InvalidOperation("argument 1 must contain comparable values of the same kind (nil, bool, int, float, or string)")
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
