package eval

import (
	"kame/core"
	"kame/internal/mem"
)

type streamCollection struct {
	source   core.Value
	consumer int64
	limit    int64
	batch    core.Value
	effects  *kashControlState
	done     bool
}

func freeStreamCollection(a mem.Allocator, value any) {
	state := value.(*streamCollection)
	state.source.Free(a)
	freeCallables(a, &state.batch)
	state.batch.Free(a)
	freeKashControlState(a, state.effects)
}

// CollectStream explicitly consumes a generator into one list. The optional
// leading count closes the generator as soon as the requested prefix is ready.
func CollectStream(c *Context, _ any, args []core.Value) Result {
	defer discardCallValues(c.Run, args)
	if c.Engine == nil || c.Phase != EvaluatePhase || c.pureValueBlock {
		c.MarkPhaseInvalid()
		return failure(c.Run, "PHASE_INVALID", c.Span, "collect! requires runtime execution")
	}
	if c.Program.DryRun {
		return Result{Completed: true}
	}
	p := c.Program
	state, _ := c.OperationState().(*streamCollection)
	if state == nil {
		limit := int64(-1)
		if len(args) == 2 {
			if args[0].Kind != core.Int || args[0].Int < 0 {
				return c.InvalidArgument(0, "non-negative int", args[0].Kind)
			}
			limit = args[0].Int
		}
		index := len(args) - 1
		value := args[index]
		g := streamGenerator(value)
		if g == nil {
			return c.InvalidArgument(index, "stream", value.Kind)
		}
		p.NextScriptGroup++
		if r := g.claim(c, p.NextScriptGroup); r.Diagnostic.Code != "" {
			return r
		}
		state = &streamCollection{
			source: value.Clone(p.Alloc), consumer: p.NextScriptGroup, limit: limit,
			batch:   core.Value{Kind: core.List},
			effects: mem.Alloc[kashControlState](p.Alloc),
		}
		c.SetOperationState(state, freeStreamCollection)
	}
	replayControlEffects(state.effects, c)
	g := streamGenerator(state.source)
	for !state.done {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		if int64(len(state.batch.List)) == state.limit {
			r := g.stop(c)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			p.retainControlEffects(state.effects, c, c.Scope, before, beforePaths)
			state.done = true
			break
		}
		r := g.pull(c, state.consumer)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		p.retainControlEffects(state.effects, c, c.Scope, before, beforePaths)
		if r.Yielded {
			state.batch.List = append(state.batch.List, borrowValue(p.Alloc, r.Value))
		} else {
			state.done = true
		}
		r.Free(c.Run)
	}
	state.source.Free(p.Alloc)
	state.batch.Share(p.Alloc)
	return Result{Value: borrowValue(c.Run, state.batch)}
}

func streamGenerator(value core.Value) *generator {
	if value.Kind != core.Stream || value.Stream == nil {
		return nil
	}
	g, _ := value.Stream.State.(*generator)
	return g
}
