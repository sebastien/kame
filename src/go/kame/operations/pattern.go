package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"

	"strconv"
	"strings"
)

// opPattern explicitly constructs a pattern value from validated runtime text.
// Strings are otherwise never reclassified as patterns after evaluation.
func opPattern(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "string")
	}
	parsed := expr.ParsePathPatternText(c.Run, values[0].Text)
	defer parsed.Pattern.Free(c.Run)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || strings.ContainsRune(values[0].Text, 0) {
		return ownedFailure(c.Run, "PAT_INVALID", "invalid runtime pattern")
	}
	kind := core.PathPattern
	pattern := parsed.Pattern
	if pattern.References != 0 {
		if pattern.Matchers != 0 || pattern.Globs != 0 {
			if _, ok := pattern.PromoteNamedCaptures(); !ok {
				return ownedFailure(c.Run, "PAT_INVALID", "positional reference is not valid in a path pattern")
			}
		} else {
			kind = core.PathTemplate
		}
	}
	return eval.Result{Value: core.Value{Kind: kind, Text: expr.CanonicalPathPattern(c.Run, pattern)}}
}

// replaceState holds the parsed patterns of one pattern replace. The state is
// owned by the allocator that parsed it and freed by freeReplaceState.
type replaceState struct {
	match              *expr.Pattern
	expand             *expr.Pattern
	expandsPath        bool
	constant           core.Value
	replacement        core.Value
	scope              *eval.Scope
	preserveNonmatches bool
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
	b = strings.Builder{}
	b.WriteString("pattern reference is missing: ")
	b.WriteString(name)
	message := cloneText(run, b.String())
	b.Reset()
	mem.FreeString(run, name)
	return eval.Result{Diagnostic: core.Diagnostic{Code: cloneText(run, "PAT_INVALID"), Severity: diagnostic.Error, Message: message, Owned: true}}
}

// parseReplaceState classifies and parses the match and expansion arguments.
// On success it sets *out and returns an empty result; otherwise it returns a
// failure and leaves *out nil.
func parseReplaceState(c *eval.Context, match core.Value, expansion core.Value, out **replaceState) eval.Result {
	parsed := parsePatternValue(c.Run, match)
	if len(parsed.Diagnostics) != 0 || (parsed.Pattern.Matchers == 0 && parsed.Pattern.Globs == 0) {
		parsed.Pattern.Free(c.Run)
		mem.FreeSlice(nil, parsed.Diagnostics)
		return ownedFailure(c.Run, "PAT_INVALID", "match pattern needs a matcher group")
	}
	state := mem.Alloc[replaceState](c.Run)
	state.match = parsed.Pattern
	state.constant = core.Value{Kind: core.Nil}
	mem.FreeSlice(nil, parsed.Diagnostics)
	if expansion.Kind == core.Callable {
		function := expansion.Callable.(*eval.Function)
		if function.DeferredReplacement {
			state.replacement = expansion
			state.scope = function.Scope
			*out = state
			return eval.Result{}
		}
	}
	if expansion.IsPathText() {
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
	if expansion.Kind == core.PathTemplate {
		expanded := parsePatternValue(c.Run, expansion)
		if len(expanded.Diagnostics) != 0 || expanded.Pattern.Matchers != 0 || expanded.Pattern.References == 0 {
			expanded.Pattern.Free(c.Run)
			mem.FreeSlice(nil, expanded.Diagnostics)
			freeReplaceState(c.Run, state)
			return ownedFailure(c.Run, "PAT_INVALID", "expansion pattern needs reference groups")
		}
		state.expand, state.expandsPath = expanded.Pattern, isExplicitPath(expansion.Text)
		mem.FreeSlice(nil, expanded.Diagnostics)
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
	return substitute(c, state.(*replaceState), values[0], 0)
}

// replaceSection returns a temporary callable of arity one applying the
// match and expansion to its argument.
func replaceSection(c *eval.Context, state *replaceState) eval.Result {
	function := mem.Alloc[eval.Function](c.Run)
	function.PayloadAlloc = c.Run
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

func substitute(c *eval.Context, state *replaceState, subject core.Value, position int) eval.Result {
	if subject.Kind != core.PathPattern {
		return replaceApply(c, state, subject)
	}
	if c.PurePredicate {
		return ownedFailure(c.Run, "KASH_PREDICATE_EFFECT", "stream predicates cannot discover paths")
	}
	var matches eval.Result
	if c.Phase == eval.PlanningPhase && c.DeclarationFiles != nil {
		matches = declarationWildcard(c, []core.Value{subject}, []int{position}, host.DiscoveryAll)
	} else {
		// Discovery and replacement each retain their own progress on suspension.
		previous := c.CallPath
		c.CallPath += "/subst-subject"
		matches = fileRequest(c, host.OpWildcard, subject)
		c.CallPath = previous
	}
	if matches.Waiting || matches.Diagnostic.Code != "" {
		return matches
	}
	defer matches.Free(c.Run)
	return replaceApply(c, state, matches.Value)
}

// replaceApply matches one string or path subject or every item of a list.
func replaceApply(c *eval.Context, state *replaceState, subject core.Value) eval.Result {
	if subject.Kind == core.Nil {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if subject.IsPathText() {
		return replaceOne(c, state, subject)
	}
	if subject.Kind == core.List {
		progress := callbackProgress(c)
		for progress.Index < len(subject.List) {
			i := progress.Index
			if !subject.List[i].IsPathText() {
				discardCallback(c, progress)
				c.DiscardCallables(&subject)
				return c.InvalidOperation("replace subject list must contain only strings or paths; got " + core.KindName(subject.List[i].Kind))
			}
			previous := c.CallPath

			identity := core.NewString(c.Run, previous+"/replace-item:"+strconv.Itoa(i))
			c.CallPath = identity.Text
			effects, paths := len(c.Effects), len(c.WritePaths)
			one := replaceOne(c, state, subject.List[i])
			c.CallPath = previous
			identity.Free(c.Run)
			if one.Waiting {
				suspendCallback(c, progress)
				return one
			}
			if one.Diagnostic.Code != "" {
				discardCallback(c, progress)
				c.DiscardCallables(&subject)
				return one
			}
			progress.Values = append(progress.Values, one.Value)
			rememberCallbackEffects(c, progress, effects, paths)
			progress.Index++
		}
		return eval.Result{Value: finishCallback(c, progress)}
	}
	// No ownership transfers on rejection; the caller frees storage only.
	c.DiscardCallables(&subject)
	expected := "replace subject expects a string, path, list of strings or paths, or nil"
	if state.preserveNonmatches {
		expected = "replace subject expects a string, path, path pattern, list of strings or paths, or nil"
	}
	return c.InvalidOperation(expected + "; got " + core.KindName(subject.Kind))
}

// replaceOne expands one subject; resub alone drops nonmatches.
func replaceOne(c *eval.Context, state *replaceState, subject core.Value) eval.Result {
	matched := state.match.MatchText(c.Run, subject.Text)
	if matched.Limited {
		mem.FreeSlice(nil, matched.Captures)
		return ownedFailure(c.Run, "PAT_LIMIT", "regular-expression match exceeded its step budget")
	}
	if !matched.Matched {
		mem.FreeSlice(nil, matched.Captures)
		if state.preserveNonmatches {
			return eval.Result{Value: subject.Clone(c.Run)}
		}
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if state.replacement.Kind == core.Callable {
		var fields []core.RecordField
		for i := range state.match.Names {
			if state.match.Names[i] != "" {
				fields = append(fields, core.RecordField{Key: state.match.Names[i], Value: core.NewString(c.Run, matched.Captures[i])})
			}
		}
		var positional []string
		for i := range matched.Captures {

			key := "_" + strconv.Itoa(i)
			duplicate := false
			for j := range fields {
				if fields[j].Key == key {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			key = core.NewString(c.Run, key).Text
			positional = append(positional, key)
			fields = append(fields, core.RecordField{Key: key, Value: core.NewString(c.Run, matched.Captures[i])})
		}
		captures := core.NewRecord(c.Run, fields)
		for i := range positional {
			mem.FreeString(c.Run, positional[i])
		}
		mem.FreeSlice(nil, positional)
		for i := range fields {
			fields[i].Value.Free(c.Run)
		}
		mem.FreeSlice(nil, fields)
		result := c.EvaluateReplacement(state.replacement, captures, state.scope)
		captures.Free(c.Run)
		if result.Waiting || result.Diagnostic.Code != "" {
			mem.FreeSlice(nil, matched.Captures)
			return result
		}
		if result.Value.Kind == core.PathTemplate {
			expandsPath := isExplicitPath(result.Value.Text)
			expansion := parsePatternValue(c.Run, result.Value)
			result.Free(c.Run)
			invalid := len(expansion.Diagnostics) != 0 || expansion.Pattern.Matchers != 0 || expansion.Pattern.References == 0
			mem.FreeSlice(nil, expansion.Diagnostics)
			if invalid {
				expansion.Pattern.Free(c.Run)
				mem.FreeSlice(nil, matched.Captures)
				return ownedFailure(c.Run, "PAT_INVALID", "expansion pattern needs reference groups")
			}
			expanded := expansion.Pattern.ExpandText(c.Run, state.match, matched.Captures)
			expansion.Pattern.Free(c.Run)
			mem.FreeSlice(nil, matched.Captures)
			if expanded.Missing != "" {
				return missingReferenceFailure(c.Run, expanded.Missing)
			}
			return eval.Result{Value: expansionValue(expanded.Text, expandsPath)}
		}
		mem.FreeSlice(nil, matched.Captures)
		if !result.Value.IsPathText() && result.Value.Kind != core.Int && result.Value.Kind != core.Float && result.Value.Kind != core.Bool {
			result.Free(c.Run)
			return ownedFailure(c.Run, "PAT_INVALID", "expansion must be a string, number, bool, or expansion pattern")
		}
		if result.Value.Kind == core.Path {
			return result
		}
		text, ok := stringValue(c.Run, result.Value, false)
		result.Free(c.Run)
		if !ok {
			return ownedFailure(c.Run, "PAT_INVALID", "invalid expansion")
		}
		return eval.Result{Value: core.Value{Kind: core.String, Text: text}}
	}
	if state.expand == nil {
		mem.FreeSlice(nil, matched.Captures)
		return eval.Result{Value: state.constant.Clone(c.Run)}
	}
	expanded := state.expand.ExpandText(c.Run, state.match, matched.Captures)
	mem.FreeSlice(nil, matched.Captures)
	if expanded.Missing != "" {
		return missingReferenceFailure(c.Run, expanded.Missing)
	}
	return eval.Result{Value: expansionValue(expanded.Text, state.expandsPath)}
}

func parsePatternValue(a mem.Allocator, value core.Value) expr.PatternParse {
	if value.Kind == core.Pattern {
		return expr.ParsePatternText(a, value.Text, 0)
	}
	return expr.ParsePathPatternText(a, value.Text)
}

func expansionValue(text string, path bool) core.Value {
	kind := core.String
	if path {
		kind = core.Path
	}
	return core.Value{Kind: kind, Text: text}
}
