package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// A rendered recipe owns its deferred effects across host write suspensions.
// Effects advance once, in authored order, before its shell recipe can start.
type forwardEffectsState struct {
	Commands   string
	Effects    []eval.Effect
	WritePaths []string
	Yielded    []byte
	Index      int
	WriteIndex int
	Pending    bool
	YieldSent  bool
	HasYield   bool
}

func (p *Program) freeForwardEffects(state *forwardEffectsState) {
	if state == nil {
		return
	}
	mem.FreeString(p.Alloc, state.Commands)
	eval.FreeEffects(p.Alloc, state.Effects)
	freeStrings(p.Alloc, state.WritePaths)
	slices.Free(p.Alloc, state.Yielded)
	mem.Free(p.Alloc, state)
}

func (p *Program) continueForwardEffects(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	state := entry.ForwardEffects
	if state.Pending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		state.Pending = false
		completion.Value.Free(p.Alloc)
		if completion.Diagnostic.Code != "" {
			p.freeForwardEffects(state)
			entry.ForwardEffects = nil
			p.failRule(c, index, completion.Diagnostic)
			return core.ProducerFailed
		}
	}
	for state.Index < len(state.Effects) {
		effect := state.Effects[state.Index]
		state.Index++
		span := diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}
		p.emit(Event{Kind: Effect, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, RequestID: entry.Node.HostRequestID, Span: span, Data: slices.Clone(p.Alloc, effect.Data), Effect: effectName(effect.Kind)})
		if effect.Kind == eval.EffectOut || effect.Kind == eval.EffectErr {
			kind := Stdout
			if effect.Kind == eval.EffectErr {
				kind = Stderr
			}
			p.emitNode(entry.Node, entry.Plan.Target, kind, span, effect.Data)
			if entry.Rule.Kind == rule.CachedTaskRule {
				if kind == Stdout {
					p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, effect.Data, p.Options.CacheRetainBytes)
				} else {
					p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, effect.Data, p.Options.CacheRetainBytes)
				}
			}
		} else if effect.Kind == eval.EffectYield {
			for i := range effect.Data {
				state.Yielded = slices.Append(p.Alloc, state.Yielded, effect.Data[i])
			}
		} else if effect.Kind == eval.EffectWrite {
			if state.WriteIndex >= len(state.WritePaths) {
				p.freeForwardEffects(state)
				entry.ForwardEffects = nil
				p.failRule(c, index, failure(p.Alloc, "FS_ERR", "missing write path"))
				return core.ProducerFailed
			}
			name := state.WritePaths[state.WriteIndex]
			state.WriteIndex++
			p.submitEffectWrite(c, name, effect.Data)
			state.Pending = true
			return core.ProducerSubmitted
		}
	}
	if state.HasYield && !state.YieldSent {
		state.YieldSent = true
		state.Pending = true
		p.submitEffectWrite(c, entry.Plan.Outputs[0], state.Yielded)
		return core.ProducerSubmitted
	}
	commands, yielded := state.Commands, state.HasYield
	state.Commands = ""
	p.freeForwardEffects(state)
	entry.ForwardEffects = nil
	return p.finishRecipe(c, index, commands, yielded)
}

func (p *Program) submitEffectWrite(c *core.EngineContext, name string, data []byte) {
	name = p.canonicalTarget(name, true)
	fields := []core.RecordField{{Key: host.FieldPath, Value: core.NewString(p.Alloc, name)}, {Key: host.FieldData, Value: core.NewBytes(p.Alloc, data)}}
	mem.FreeString(p.Alloc, name)
	payload := core.NewRecord(p.Alloc, fields)
	for i := range fields {
		fields[i].Value.Free(p.Alloc)
	}
	id := p.Eval.Requests.Submit(c.NodeID(), c.Generation(), c.Attempt(), host.RequestWriteFile, payload)
	payload.Free(p.Alloc)
	c.Submit(id)
}
