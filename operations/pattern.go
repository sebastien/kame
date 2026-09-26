package operations

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// replaceState holds the parsed patterns of one pattern replace. The state is
// owned by the allocator that parsed it and freed by freeReplaceState.
type replaceState struct {
	match    *expr.Pattern
	expand   *expr.Pattern
	constant core.Value
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
	if expansion.Kind == core.String {
		state.constant = expansion.Clone(c.Run)
		*out = state
		return eval.Result{}
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
	return ownedFailure(c.Run, "PAT_INVALID", "expansion must be a string or an expansion pattern")
}

func freeReplaceState(a mem.Allocator, state *replaceState) {
	state.match.Free(a)
	if state.expand != nil {
		state.expand.Free(a)
	}
	state.constant.Free(a)
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
		out := slices.Make[core.Value](c.Run, len(subject.List))
		for i := range subject.List {
			if subject.List[i].Kind != core.String {
				freeValues(c, out)
				return invalid()
			}
			one := replaceOne(c, state, subject.List[i].Text)
			if one.Diagnostic.Code != "" {
				freeValues(c, out)
				return one
			}
			out[i] = one.Value
		}
		return eval.Result{Value: core.Value{Kind: core.List, List: out}}
	}
	return invalid()
}

// replaceOne expands one subject. A subject without a match yields nil.
func replaceOne(c *eval.Context, state *replaceState, subject string) eval.Result {
	matched := state.match.MatchText(c.Run, subject)
	if !matched.Matched {
		slices.Free(c.Run, matched.Captures)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
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
