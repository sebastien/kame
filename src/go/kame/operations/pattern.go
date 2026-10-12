package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// opPattern explicitly constructs a pattern value from validated runtime text.
// Strings are otherwise never reclassified as patterns after evaluation.
func opPattern(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "string")
	}
	parsed := expr.ParsePatternText(c.Run, values[0].Text, 0)
	defer parsed.Pattern.Free(c.Run)
	defer slices.Free(c.Run, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || (parsed.Pattern.Matchers != 0 && parsed.Pattern.References != 0) {
		return ownedFailure(c.Run, "PAT_INVALID", "invalid runtime pattern")
	}
	return eval.Result{Value: core.NewPattern(c.Run, values[0].Text)}
}

// replaceState holds the parsed patterns of one pattern replace. The state is
// owned by the allocator that parsed it and freed by freeReplaceState.
type replaceState struct {
	match    *expr.Pattern
	expand   *expr.Pattern
	constant core.Value
	replacement core.Value
	scope *eval.Scope
}

func ownedFailure(run mem.Allocator, code string, message string) eval.Result {
	return eval.Result{Diagnostic: core.Diagnostic{Code: cloneText(run, code), Severity: diagnostic.Error, Message: cloneText(run, message), Owned: true}}
}

func cloneText(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// missingReferenceFailure reports one expansion reference that no capture
// satisfied. It takes ownership of name.
func missingReferenceFailure(run mem.Allocator, name string) eval.Result {
	var b strings.Builder
	b = strings.NewBuilder(run)
	b.WriteString("pattern reference is missing: ")
	b.WriteString(name)
	message := cloneText(run, b.String())
	b.Free()
	mem.FreeString(run, name)
	return eval.Result{Diagnostic: core.Diagnostic{Code: cloneText(run, "PAT_INVALID"), Severity: diagnostic.Error, Message: message, Owned: true}}
}

// parseReplaceState classifies and parses the match and expansion arguments.
// On success it sets *out and returns an empty result; otherwise it returns a
// failure and leaves *out nil.
func parseReplaceState(c *eval.Context, match core.Value, expansion core.Value, out **replaceState) eval.Result {
	parsed := expr.ParsePatternText(c.Run, match.Text, 0)
	if len(parsed.Diagnostics) != 0 || !expr.HasGroups(parsed.Pattern) || parsed.Pattern.Matchers == 0 {
		parsed.Pattern.Free(c.Run)
		slices.Free(c.Run, parsed.Diagnostics)
		return ownedFailure(c.Run, "PAT_INVALID", "match pattern needs a matcher group")
	}
	state := mem.Alloc[replaceState](c.Run)
	state.match = parsed.Pattern
	state.constant = core.Value{Kind: core.Nil}
	slices.Free(c.Run, parsed.Diagnostics)
	if expansion.Kind == core.Callable {
		function := expansion.Callable.(*eval.Function)
		if function.DeferredReplacement {
			state.replacement = expansion
			state.scope = function.Scope
			*out = state
			return eval.Result{}
		}
	}
	if expansion.Kind == core.String {
		state.constant = expansion.Clone(c.Run)
		*out = state
		return eval.Result{}
	}
	if expansion.Kind == core.Int || expansion.Kind == core.Float || expansion.Kind == core.Bool {
		text, ok := stringValue(c.Run, expansion, false)
		if ok {
			state.constant = core.Value{Kind: core.String, Text: text}
			*out = state
			return eval.Result{}
		}
	}
	if expansion.Kind == core.Pattern {
		expanded := expr.ParsePatternText(c.Run, expansion.Text, 0)
		if len(expanded.Diagnostics) != 0 || expanded.Pattern.Matchers != 0 || expanded.Pattern.References == 0 {
			expanded.Pattern.Free(c.Run)
			slices.Free(c.Run, expanded.Diagnostics)
			freeReplaceState(c.Run, state)
			return ownedFailure(c.Run, "PAT_INVALID", "expansion pattern needs reference groups")
		}
		state.expand = expanded.Pattern
		slices.Free(c.Run, expanded.Diagnostics)
		*out = state
		return eval.Result{}
	}
	freeReplaceState(c.Run, state)
	return ownedFailure(c.Run, "PAT_INVALID", "expansion must be a string, number, bool, or expansion pattern")
}

func freeReplaceState(a mem.Allocator, state *replaceState) {
	state.match.Free(a)
	if state.expand != nil {
		state.expand.Free(a)
	}
	state.constant.Free(a)
	eval.ReleaseCallable(a, &state.replacement)
	mem.Free(a, state)
}

// replaceNativeFree adapts state cleanup for temporary section functions.
func replaceNativeFree(a mem.Allocator, state any) {
	freeReplaceState(a, state.(*replaceState))
}

// replaceCall applies a replace section to one evaluated subject.
func replaceCall(c *eval.Context, state any, values []core.Value) eval.Result {
	return replaceApply(c, state.(*replaceState), values[0])
}

// replaceSection returns a temporary callable of arity one applying the
// match and expansion to its argument.
func replaceSection(c *eval.Context, state *replaceState) eval.Result {
	function := mem.Alloc[eval.Function](c.Run)
	function.Kind = eval.FunctionTemporary
	function.Arity = 1
	function.Native = state
	function.NativeCall = replaceCall
	function.NativeFree = replaceNativeFree
	if state.replacement.Kind == core.Callable {
		operand := state.replacement.Callable.(*eval.Function)
		// Transfer the scope hold to the returned callable so lexical cycle
		// breaking and borrowed wrappers can see its ownership.
		function.Scope = operand.Scope
		operand.Scope = nil
	}
	return eval.Result{Value: core.Value{Kind: core.Callable, Callable: function}}
}

// replaceApply matches one string subject or every item of a list subject.
func replaceApply(c *eval.Context, state *replaceState, subject core.Value) eval.Result {
	if subject.Kind == core.Nil {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if subject.Kind == core.String {
		return replaceOne(c, state, subject.Text)
	}
	if subject.Kind == core.List {
		progress := callbackProgress(c)
		for progress.Index < len(subject.List) {
			i := progress.Index
			if subject.List[i].Kind != core.String {
				discardCallback(c, progress)
				c.DiscardCallables(&subject)
				return c.InvalidOperation("replace subject list must contain only strings; got " + core.KindName(subject.List[i].Kind))
			}
			previous := c.CallPath
			var digits [32]byte
			identity := core.NewString(c.Run, previous + "/replace-item:" + strconv.Itoa(digits[:], i))
			c.CallPath = identity.Text
			effects, paths := len(c.Effects), len(c.WritePaths)
			one := replaceOne(c, state, subject.List[i].Text)
			c.CallPath = previous
			identity.Free(c.Run)
			if one.Waiting { suspendCallback(c, progress); return one }
			if one.Diagnostic.Code != "" {
				discardCallback(c, progress)
				c.DiscardCallables(&subject)
				return one
			}
			progress.Values = slices.Append(c.Run, progress.Values, one.Value)
			rememberCallbackEffects(c, progress, effects, paths)
			progress.Index++
		}
		return eval.Result{Value: finishCallback(c, progress)}
	}
	// No ownership transfers on rejection; the caller frees storage only.
	c.DiscardCallables(&subject)
	return c.InvalidOperation("replace subject expects string, list of strings, or nil; got " + core.KindName(subject.Kind))
}

// replaceOne expands one subject. A subject without a match yields nil.
func replaceOne(c *eval.Context, state *replaceState, subject string) eval.Result {
	matched := state.match.MatchText(c.Run, subject)
	if matched.Limited {
		slices.Free(c.Run, matched.Captures)
		return ownedFailure(c.Run, "PAT_LIMIT", "regular-expression match exceeded its step budget")
	}
	if !matched.Matched {
		slices.Free(c.Run, matched.Captures)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if state.replacement.Kind == core.Callable {
		var fields []core.RecordField
		for i := range state.match.Names {
			if state.match.Names[i] != "" { fields = slices.Append(c.Run, fields, core.RecordField{Key: state.match.Names[i], Value: core.NewString(c.Run, matched.Captures[i])}) }
		}
		captures := core.NewRecord(c.Run, fields)
		for i := range fields { fields[i].Value.Free(c.Run) }
		slices.Free(c.Run, fields)
		result := c.EvaluateReplacement(state.replacement, captures, state.scope)
		captures.Free(c.Run)
		if result.Waiting || result.Diagnostic.Code != "" { slices.Free(c.Run, matched.Captures); return result }
		if result.Value.Kind == core.Pattern {
			expansion := expr.ParsePatternText(c.Run, result.Value.Text, 0)
			result.Free(c.Run)
			invalid := len(expansion.Diagnostics) != 0 || expansion.Pattern.Matchers != 0 || expansion.Pattern.References == 0
			slices.Free(c.Run, expansion.Diagnostics)
			if invalid {
				expansion.Pattern.Free(c.Run)
				slices.Free(c.Run, matched.Captures)
				return ownedFailure(c.Run, "PAT_INVALID", "expansion pattern needs reference groups")
			}
			expanded := expansion.Pattern.ExpandText(c.Run, state.match, matched.Captures)
			expansion.Pattern.Free(c.Run)
			slices.Free(c.Run, matched.Captures)
			if expanded.Missing != "" { return missingReferenceFailure(c.Run, expanded.Missing) }
			return eval.Result{Value: core.Value{Kind: core.String, Text: expanded.Text}}
		}
		slices.Free(c.Run, matched.Captures)
		if result.Value.Kind != core.String && result.Value.Kind != core.Int && result.Value.Kind != core.Float && result.Value.Kind != core.Bool {
			result.Free(c.Run)
			return ownedFailure(c.Run, "PAT_INVALID", "expansion must be a string, number, bool, or expansion pattern")
		}
		text, ok := stringValue(c.Run, result.Value, false)
		result.Free(c.Run)
		if !ok { return ownedFailure(c.Run, "PAT_INVALID", "invalid expansion") }
		return eval.Result{Value: core.Value{Kind: core.String, Text: text}}
	}
	if state.expand == nil {
		slices.Free(c.Run, matched.Captures)
		return eval.Result{Value: state.constant.Clone(c.Run)}
	}
	expanded := state.expand.ExpandText(c.Run, state.match, matched.Captures)
	slices.Free(c.Run, matched.Captures)
	if expanded.Missing != "" {
		return missingReferenceFailure(c.Run, expanded.Missing)
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: expanded.Text}}
}
