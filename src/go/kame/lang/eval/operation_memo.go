package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"slices"
)

// Retain completed operands only while their application is suspended. This is
// evaluation progress, not a cache of arbitrary operation results.
type operationMemo struct {
	Alloc      mem.Allocator
	NodeID     int64
	Generation int64
	Span       source.Span
	Source     string
	CallPath   string
	Operation  string
	Scope      core.Signature
	Values     []core.Value
	Next       int
	// Facts is how many accepted observations the retained operands consumed;
	// Epoch is the node's observation epoch when they were retained.
	Facts      int
	Epoch      int64
	Effects    []Effect
	WritePaths []string
}

type argumentResult struct {
	Values           []core.Value
	Result           Result
	Facts            int
	ReplacementIndex int
}

func (p *Program) freeOperationMemo(state *operationMemo) {
	_ = p
	a := state.Alloc
	freeValuesWithCallables(a, state.Values)
	FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	mem.FreeSlice(nil, state.WritePaths)
	mem.FreeString(a, state.Source)
	mem.FreeString(a, state.CallPath)
	mem.FreeString(a, state.Operation)
	mem.Free(a, state)
}

func (p *Program) memoScope(scope *Scope, c *Context) core.Signature {
	if c.DisableMemo || c.Engine == nil {
		return core.Signature{}
	}
	d := core.NewDigest()
	d.Text(c.Cwd)
	d.Text(c.Source)
	d.Uint64(uint64(c.Phase))
	d.Write(c.DefinitionNamespace[:])
	if c.HasEnvironment {
		d.Text("snapshot")
	} else {
		d.Text("ambient")
	}
	d.Uint64(uint64(len(c.Grants)))
	for i := range c.Grants {
		d.Uint64(uint64(c.Grants[i].Capability))
		d.Uint64(uint64(len(c.Grants[i].Names)))
		for j := range c.Grants[i].Names {
			d.Text(c.Grants[i].Names[j])
		}
	}
	environment := p.environmentDigest(c.CommandEnvironment())
	d.Write(environment.Digest[:])
	d.Uint64(uint64(len(c.RenderStack)))
	for i := range c.RenderStack {
		d.Text(c.RenderStack[i])
	}
	if c.sequencedOperand {
		// Reached let/exit operands are execution progress, even when their
		// arguments contain unhashable callables. CallPath identifies the call.
		d.Text("sequenced-args")
		d.Uint64(uint64(c.ScriptGroup))
	} else {
		args := core.ValueSignature(core.Value{Kind: core.List, List: c.Args})
		if !args.Equal(args) {
			return core.Signature{}
		}
		d.Write(args.Digest[:])
	}
	if c.HasArgs {
		d.Text("args")
	}
	d.Uint64(uint64(len(c.RuleFrames)))
	for i := range c.RuleFrames {
		frame := c.RuleFrames[i]
		if frame.ServiceRule {
			d.Text("service")
		}
		inputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Inputs})
		outputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Outputs})
		newer := core.ValueSignature(core.Value{Kind: core.List, List: frame.NewerInputs})
		if !inputs.Equal(inputs) || !outputs.Equal(outputs) || !newer.Equal(newer) {
			return core.Signature{}
		}
		d.Write(inputs.Digest[:])
		d.Write(outputs.Digest[:])
		d.Write(newer.Digest[:])
		if len(frame.Captures) != 0 {
			captures := core.ValueSignature(core.Value{Kind: core.List, List: frame.Captures})
			if !captures.Equal(captures) {
				return core.Signature{}
			}
			d.Text("captures")
			d.Write(captures.Digest[:])
		}
	}
	for current := scope; current != nil; current = current.Parent {
		if current == p.Scope {
			break
		}
		if c.sequencedOperand {
			// The node, authored span, and CallPath already identify this retained
			// statement/iteration. Do not demand lazy declarations or turn reserved
			// eager slots/callables into unavailable progress identities. Within an
			// sequenced operand, mutations are reached work, not a reason to replay.
			d.Text("sequenced")
			d.Uint64(uint64(c.ScriptGroup))
			continue
		}
		d.Uint64(uint64(len(current.Bindings)))
		// ponytail: unhashable local functions/definitions bypass retention;
		// give them stable lexical identities only if a profile warrants it.
		for i := range current.Bindings {
			item := current.Bindings[i]
			if item.Kind != bindingValue {
				return core.Signature{}
			}
			signature := core.ValueSignature(item.Value)
			if !signature.Equal(signature) {
				return core.Signature{}
			}
			d.Text(item.Name)
			d.Write(signature.Digest[:])
		}
		if current.Section != nil {
			signature := core.ValueSignature(core.Value{Kind: core.List, List: current.Section})
			if !signature.Equal(signature) {
				return core.Signature{}
			}
			d.Write(signature.Digest[:])
		}
	}
	signature := core.Signature{Mode: core.SignatureContent}
	d.Sum(signature.Digest[:])
	return signature
}

func (p *Program) findArgumentMemo(c *Context, span source.Span, operation string) *operationMemo {
	if c.Engine == nil {
		return nil
	}
	for i := len(p.OperationMemos) - 1; i >= 0; i-- {
		state := p.OperationMemos[i]
		if state.NodeID == c.Engine.NodeID() && state.Generation != c.Engine.Generation() {
			p.discardArgumentMemo(state)
		}
	}
	for i := range p.OperationMemos {
		state := p.OperationMemos[i]
		if state.NodeID == c.Engine.NodeID() && state.Span.Start == span.Start && state.Span.End == span.End && state.Source == c.Source && state.CallPath == c.CallPath && state.Operation == operation {
			return state
		}
	}
	return nil
}

func (p *Program) discardArgumentMemo(state *operationMemo) {
	for i := range p.OperationMemos {
		if p.OperationMemos[i] == state {
			copy(p.OperationMemos[i:], p.OperationMemos[i+1:])
			p.OperationMemos = p.OperationMemos[:len(p.OperationMemos)-1]
			if !slices.ContainsFunc(p.OperationMemos, func(other *operationMemo) bool { return other.NodeID == state.NodeID }) {
				delete(p.memoValidations, state.NodeID)
			}
			p.freeOperationMemo(state)
			return
		}
	}
}

// memoValidation records that a node generation's first Facts accepted
// observations were valid when the engine's change stamp was Changes.
type memoValidation struct {
	Generation, Epoch, Changes int64
	Facts                      int
}

func (p *Program) memoCurrent(state *operationMemo, c *Context, scope *Scope) bool {
	if state.Generation != c.Engine.Generation() || state.Epoch != c.Engine.ObservationEpoch() || state.Alloc != c.Run || !state.Scope.Equal(p.memoScope(scope, c)) {
		return false
	}
	facts := c.Engine.AcceptedObservations()
	if len(facts) < state.Facts {
		return false
	}
	// Facts only grow within an epoch, and nothing can falsify a validated one
	// without changing the stamp, so only facts past the validated prefix are
	// checked. Each resumption would otherwise recheck every earlier read.
	node := c.Engine.NodeID()
	validated := p.memoValidations[node]
	if validated.Generation != state.Generation || validated.Epoch != state.Epoch || validated.Changes != c.Engine.Changes() {
		validated = memoValidation{Generation: state.Generation, Epoch: state.Epoch, Changes: c.Engine.Changes()}
	}
	for i := validated.Facts; i < state.Facts; i++ {
		if !p.factCurrent(facts[i], c) {
			return false
		}
		validated.Facts = i + 1
	}
	if p.memoValidations == nil {
		p.memoValidations = map[int64]memoValidation{}
	}
	p.memoValidations[node] = validated
	return true
}

// factCurrent reports whether an accepted observation still holds.
func (p *Program) factCurrent(item core.Observation, c *Context) bool {
	if !item.Signature.Equal(item.Signature) || item.Aspect >= core.ObservationMetadata {
		return false
	}
	node := p.Engine.Lookup(item.Key)
	if node == nil {
		if item.Key.Kind == core.ResourceEnvironment {
			return true
		}
		if item.Key.Kind == core.ResourceOperation {
			// Invocation-owned host results are retained within this generation,
			// never used as a proof for another invocation or generation.
			if item.Key.Name == UntrackedHostRead {
				return true
			}
			operation := p.Registry.lookup(item.Key.Name)
			if operation != nil && item.Signature.Equal(core.ValueSignature(core.Value{Kind: core.String, Text: operation.Version})) {
				return true
			}
		}
		return false
	}
	if !node.Current || node.ValidationPending {
		return false
	}
	if item.Key.Kind != core.ResourceEnvironment && !c.Engine.HasDependency(item.Key) {
		return false
	}
	signature := node.Signature
	if item.Aspect == core.ObservationExistence {
		signature = core.ValueSignature(core.Value{Kind: core.Bool, Bool: node.Latest.Kind != core.Nil})
	}
	return item.Signature.Equal(signature)
}

func (p *Program) argumentValues(scope *Scope, arguments []*expr.Expr, c *Context, span source.Span) argumentResult {
	return p.argumentValuesMode(scope, arguments, nil, c, span, false, "")
}

func (p *Program) argumentValuesMode(scope *Scope, arguments []*expr.Expr, lexical []*Scope, c *Context, span source.Span, deferReplacement bool, operation string) argumentResult {
	effects, paths := len(c.Effects), len(c.WritePaths)
	// Lowered wrappers (e.g. template-cat and render) can share one source span.
	state := p.findArgumentMemo(c, span, operation)
	var values []core.Value
	next := 0
	facts := -1
	if state != nil {
		if p.memoCurrent(state, c, scope) && len(state.Values) == len(arguments) {
			values, next = state.Values, state.Next
			if next == len(arguments) {
				facts = state.Facts
			}
			state.Values = nil
			for i := range state.Effects {
				effect := state.Effects[i]
				effect.Data = slices.Clone(effect.Data)
				c.Effects = append(c.Effects, effect)
			}
			for i := range state.WritePaths {
				c.WritePaths = append(c.WritePaths, owned(c.Run, state.WritePaths[i]))
			}
		}
		p.discardArgumentMemo(state)
	}
	if values == nil {
		values = make([]core.Value, len(arguments))
	}
	position, replacementIndex := 0, -1
	firstPattern := false
	for i, argument := range arguments {
		spread := argument.Kind == expr.ListSpread
		if deferReplacement && position == 1 && firstPattern && !spread {
			replacementIndex = i
			position++
			continue
		}
		if i >= next {
			facts, completedEffects, completedPaths := 0, len(c.Effects), len(c.WritePaths)
			if c.Engine != nil {
				facts = len(c.Engine.AcceptedObservations())
			}
			operandScope := scope
			if lexical != nil {
				operandScope = lexical[i]
			}
			r := p.evaluate(c.Engine, operandScope, argument, c)
			if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
				if r.Waiting && i != 0 && p.retainArguments(scope, c, span, operation, values, i, facts, effects, completedEffects, paths, completedPaths) {
					return argumentResult{Result: r}
				}
				freeValuesWithCallables(c.Run, values)
				return argumentResult{Result: r}
			}
			values[i] = r.Value
		}
		if !deferReplacement {
			continue
		}
		first, count := values[i], 1
		if spread {
			count = len(first.List)
			if count == 0 {
				continue
			}
			first = first.List[0]
		}
		if position == 0 && first.Kind == core.PathTemplate {
			freeValuesWithCallables(c.Run, values)
			return argumentResult{Result: failure(c.Run, "PAT_INVALID", argument.Span, "substitution requires a matching pattern")}
		}
		if position == 0 && (first.Kind == core.Pattern || first.Kind == core.PathPattern) {
			firstPattern = true
			if i >= next {
				var pattern expr.PatternParse
				if first.Kind == core.PathPattern {
					pattern = expr.ParsePathPatternText(c.Run, first.Text)
				} else {
					pattern = expr.ParsePatternText(c.Run, first.Text, argument.Span.Start)
				}
				invalid := len(pattern.Diagnostics) != 0 || (pattern.Pattern.Matchers == 0 && pattern.Pattern.Globs == 0) || pattern.Pattern.References != 0
				pattern.Pattern.Free(c.Run)
				mem.FreeSlice(nil, pattern.Diagnostics)
				if invalid {
					freeValuesWithCallables(c.Run, values)
					return argumentResult{Result: failure(c.Run, "PAT_INVALID", argument.Span, "match pattern needs a matcher group")}
				}
			}
		}
		position += count
	}
	if facts < 0 && c.Engine != nil {
		facts = len(c.Engine.AcceptedObservations())
	}
	return argumentResult{Values: values, Facts: facts, ReplacementIndex: replacementIndex}
}

func (p *Program) retainArguments(scope *Scope, c *Context, span source.Span, operation string, values []core.Value, next int, facts int, effects int, completedEffects int, paths int, completedPaths int) bool {
	for i := 0; i < next; i++ {
		// Sequenced evaluation retains its authored AST and lexical scopes;
		// the memo can own callable operands until resumption or cancellation.
		if values[i].HasCallable() && !c.sequencedOperand {
			return false
		}
	}
	signature := p.memoScope(scope, c)
	if signature.Mode == core.SignatureUnavailable {
		return false
	}
	state := mem.Alloc[operationMemo](c.Run)
	state.Alloc, state.NodeID, state.Generation, state.Span = c.Run, c.Engine.NodeID(), c.Engine.Generation(), span
	state.Source, state.CallPath = owned(c.Run, c.Source), owned(c.Run, c.CallPath)
	state.Operation = owned(c.Run, operation)
	state.Scope, state.Values, state.Next = signature, values, next
	// ponytail: a conservative fact prefix avoids introducing expression nodes;
	// collect precise per-operand provenance only if invalidation cost warrants it.
	state.Facts, state.Epoch = facts, c.Engine.ObservationEpoch()
	for i := effects; i < completedEffects; i++ {
		effect := c.Effects[i]
		effect.Data = slices.Clone(effect.Data)
		state.Effects = append(state.Effects, effect)
	}
	for i := paths; i < completedPaths; i++ {
		state.WritePaths = append(state.WritePaths, owned(c.Run, c.WritePaths[i]))
	}
	p.OperationMemos = append(p.OperationMemos, state)
	return true
}

// environmentDigest fingerprints an environment list for memo scopes. Contexts
// share their environment slice and never mutate it in place, so the digest
// is reused while the same backing array and length are presented.
func (p *Program) environmentDigest(environment []string) core.Signature {
	if len(environment) != 0 && p.environmentDigestLen == len(environment) && p.environmentDigestBase == &environment[0] {
		return p.environmentDigestValue
	}
	d := core.NewDigest()
	d.Uint64(uint64(len(environment)))
	for i := range environment {
		d.Text(environment[i])
	}
	signature := core.Signature{Mode: core.SignatureContent}
	d.Sum(signature.Digest[:])
	if len(environment) != 0 {
		p.environmentDigestBase, p.environmentDigestLen, p.environmentDigestValue = &environment[0], len(environment), signature
	}
	return signature
}
