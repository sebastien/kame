package operations

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func opMatch(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Pattern {
		return invalidArgument(c, values, 0, "match pattern")
	}
	parsed := expr.ParsePatternText(c.Run, values[0].Text, 0)
	invalid := len(parsed.Diagnostics) != 0 || parsed.Pattern.Matchers == 0 || parsed.Pattern.References != 0
	slices.Free(c.Run, parsed.Diagnostics)
	if invalid {
		parsed.Pattern.Free(c.Run)
		discardArgCallables(c, values)
		return ownedFailure(c.Run, "PAT_INVALID", "match? needs a matcher group and no expansion references")
	}
	if len(values) == 1 {
		function := mem.Alloc[eval.Function](c.Run)
		function.Kind = eval.FunctionTemporary
		function.Arity = 1
		function.Native = parsed.Pattern
		function.NativeCall = matchCall
		function.NativeFree = matchFree
		return eval.Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	}
	if values[1].Kind != core.String {
		parsed.Pattern.Free(c.Run)
		return invalidArgument(c, values, 1, "string")
	}
	result := matchCall(c, parsed.Pattern, values[1:])
	parsed.Pattern.Free(c.Run)
	return result
}

func matchFree(a mem.Allocator, state any) {
	state.(*expr.Pattern).Free(a)
}

func matchCall(c *eval.Context, state any, values []core.Value) eval.Result {
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "string")
	}
	pattern := state.(*expr.Pattern)
	matched := pattern.MatchText(c.Run, values[0].Text)
	slices.Free(c.Run, matched.Captures)
	if matched.Limited {
		return ownedFailure(c.Run, "PAT_LIMIT", "regular-expression match exceeded its step budget")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: matched.Matched}}
}
