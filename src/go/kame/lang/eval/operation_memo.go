package eval

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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
	Scope      core.Signature
	Values     []core.Value
	Next       int
	Inputs     []core.Observation
	Effects    []Effect
	WritePaths []string
}

type argumentResult struct {
	Values []core.Value
	Result Result
}

func (p *Program) freeOperationMemo(state *operationMemo) {
	_ = p
	a := state.Alloc
	freeValuesWithCallables(a, state.Values)
	core.FreeObservations(a, state.Inputs)
	FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	slices.Free(a, state.WritePaths)
	mem.FreeString(a, state.Source)
	mem.FreeString(a, state.CallPath)
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
	d.Uint64(uint64(len(c.Environment)))
	for i := range c.Environment {
		d.Text(c.Environment[i])
	}
	d.Uint64(uint64(len(c.RenderStack)))
	for i := range c.RenderStack {
		d.Text(c.RenderStack[i])
	}
	args := core.ValueSignature(core.Value{Kind: core.List, List: c.Args})
	if !args.Equal(args) {
		return core.Signature{}
	}
	d.Write(args.Digest[:])
	if c.HasArgs {
		d.Text("args")
	}
	d.Uint64(uint64(len(c.RuleFrames)))
	for i := range c.RuleFrames {
		frame := c.RuleFrames[i]
		if frame.ServiceRule { d.Text("service") }
		inputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Inputs})
		outputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Outputs})
		newer := core.ValueSignature(core.Value{Kind: core.List, List: frame.NewerInputs})
		if !inputs.Equal(inputs) || !outputs.Equal(outputs) || !newer.Equal(newer) {
			return core.Signature{}
		}
		d.Write(inputs.Digest[:])
		d.Write(outputs.Digest[:])
		d.Write(newer.Digest[:])
	}
	for current := scope; current != nil; current = current.Parent {
		if current.Mutable {
			return core.Signature{}
		}
		if current == p.Scope {
			break
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

func (p *Program) findArgumentMemo(c *Context, span source.Span) *operationMemo {
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
		if state.NodeID == c.Engine.NodeID() && state.Span.Start == span.Start && state.Span.End == span.End && state.Source == c.Source && state.CallPath == c.CallPath {
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
			p.freeOperationMemo(state)
			return
		}
	}
}

func (p *Program) memoCurrent(state *operationMemo, c *Context, scope *Scope) bool {
	if state.Generation != c.Engine.Generation() || state.Alloc != c.Run || !state.Scope.Equal(p.memoScope(scope, c)) {
		return false
	}
	facts := c.Engine.AcceptedObservations()
	if len(facts) < len(state.Inputs) {
		return false
	}
	for i := range state.Inputs {
		item, fact := state.Inputs[i], facts[i]
		if fact.Key.Kind != item.Key.Kind || fact.Key.Name != item.Key.Name || fact.Aspect != item.Aspect || !fact.Signature.Equal(item.Signature) {
			return false
		}
		if !item.Signature.Equal(item.Signature) || item.Aspect == core.ObservationMetadata {
			return false
		}
		node := p.Engine.Lookup(item.Key)
		if node == nil {
			if item.Key.Kind == core.ResourceEnvironment {
				continue
			}
			if item.Key.Kind == core.ResourceOperation {
				// Invocation-owned host results are retained within this generation,
				// never used as a proof for another invocation or generation.
				if item.Key.Name == UntrackedHostRead {
					continue
				}
				operation := p.Registry.lookup(item.Key.Name)
				if operation != nil && item.Signature.Equal(core.ValueSignature(core.Value{Kind: core.String, Text: operation.Version})) {
					continue
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
		if !item.Signature.Equal(signature) {
			return false
		}
	}
	return true
}

func (p *Program) argumentValues(scope *Scope, arguments []*expr.Expr, c *Context, span source.Span) argumentResult {
	effects, paths := len(c.Effects), len(c.WritePaths)
	state := p.findArgumentMemo(c, span)
	var values []core.Value
	next := 0
	if state != nil {
		if p.memoCurrent(state, c, scope) && len(state.Values) == len(arguments) {
			values, next = state.Values, state.Next
			state.Values = nil
			for i := range state.Effects {
				effect := state.Effects[i]
				effect.Data = slices.Clone(c.Run, effect.Data)
				c.Effects = slices.Append(c.Run, c.Effects, effect)
			}
			for i := range state.WritePaths {
				c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, state.WritePaths[i]))
			}
		}
		p.discardArgumentMemo(state)
	}
	if values == nil {
		values = slices.Make[core.Value](c.Run, len(arguments))
	}
	for i := next; i < len(arguments); i++ {
		facts, completedEffects, completedPaths := 0, len(c.Effects), len(c.WritePaths)
		if c.Engine != nil {
			facts = len(c.Engine.AcceptedObservations())
		}
		r := p.evaluate(c.Engine, scope, arguments[i], c)
		if r.Waiting || r.Diagnostic.Code != "" {
			if r.Waiting && i != 0 && p.retainArguments(scope, c, span, values, i, facts, effects, completedEffects, paths, completedPaths) {
				return argumentResult{Result: r}
			}
			freeValuesWithCallables(c.Run, values)
			return argumentResult{Result: r}
		}
		values[i] = r.Value
	}
	return argumentResult{Values: values}
}

func (p *Program) retainArguments(scope *Scope, c *Context, span source.Span, values []core.Value, next int, facts int, effects int, completedEffects int, paths int, completedPaths int) bool {
	for i := 0; i < next; i++ {
		if values[i].HasCallable() {
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
	state.Scope, state.Values, state.Next = signature, values, next
	// ponytail: a conservative fact prefix avoids introducing expression nodes;
	// collect precise per-operand provenance only if invalidation cost warrants it.
	for i := 0; i < facts; i++ {
		item := c.Engine.AcceptedObservations()[i]
		item.Key = item.Key.Clone(c.Run)
		state.Inputs = slices.Append(c.Run, state.Inputs, item)
	}
	for i := effects; i < completedEffects; i++ {
		effect := c.Effects[i]
		effect.Data = slices.Clone(c.Run, effect.Data)
		state.Effects = slices.Append(c.Run, state.Effects, effect)
	}
	for i := paths; i < completedPaths; i++ {
		state.WritePaths = slices.Append(c.Run, state.WritePaths, owned(c.Run, c.WritePaths[i]))
	}
	p.OperationMemos = slices.Append(p.Alloc, p.OperationMemos, state)
	return true
}
