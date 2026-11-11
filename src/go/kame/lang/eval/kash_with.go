package eval

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"slices"
	"strings"
)

type kashWithState struct {
	Index       int
	Cwd         string
	Environment []string
	Effects     []Effect
	Paths       []string
}

func freeKashWith(a mem.Allocator, value any) {
	s := value.(*kashWithState)
	mem.FreeString(a, s.Cwd)
	for i := range s.Environment {
		mem.FreeString(a, s.Environment[i])
	}
	mem.FreeSlice(nil, s.Environment)
	FreeEffects(a, s.Effects)
	for i := range s.Paths {
		mem.FreeString(a, s.Paths[i])
	}
	mem.FreeSlice(nil, s.Paths)
	mem.Free(a, s)
}

func (p *Program) kashWith(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.Phase != EvaluatePhase || c.PurePredicate {
		return failure(c.Run, "PHASE_INVALID", e.Span, "with requires runtime execution")
	}
	if p.DryRun {
		return Result{}
	}
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "with requires an execution engine")
	}
	start, end := c.operationStart, c.operationEnd
	// Header progress and the body invocation have separate operation-state keys.
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.Start+4
	var state *kashWithState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashWithState)
	}
	if state == nil {
		state = mem.Alloc[kashWithState](p.Alloc)
		state.Cwd = owned(p.Alloc, c.Cwd)
		for i := range c.Environment {
			state.Environment = append(state.Environment, owned(p.Alloc, c.Environment[i]))
		}
		c.SetOperationState(state, freeKashWith)
	}
	c.operationStart, c.operationEnd = start, end
	for i := range state.Effects {
		effect := state.Effects[i]
		effect.Data = slices.Clone(effect.Data)
		c.Effects = append(c.Effects, effect)
	}
	for i := range state.Paths {
		c.WritePaths = append(c.WritePaths, owned(c.Run, state.Paths[i]))
	}
	for state.Index < len(e.Items) {
		setup := e.Items[state.Index]
		before, paths := len(c.Effects), len(c.WritePaths)
		previous := c.sequencedOperand
		c.sequencedOperand = true
		r := p.commandWord(setup.Items[0], c, "with :"+setup.Text)
		c.sequencedOperand = previous
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r
		}
		if kind, _ := expr.ClassifySetupKey(setup.Text); kind == expr.SetupCwd {
			text, ok := processScalar(c.Run, r.Value)
			r.Free(c.Run)
			if !ok || text == "" {
				mem.FreeString(c.Run, text)
				return failure(c.Run, "EXPR_INVALID", setup.Span, "with cwd requires one nonempty scalar without NUL")
			}
			mem.FreeString(p.Alloc, state.Cwd)
			state.Cwd = canonicalPath(p.Alloc, c.Cwd, text)
			mem.FreeString(c.Run, text)
		} else {
			if !validProcessEnvironment(r.Value) {
				r.Free(c.Run)
				return failure(c.Run, "EXPR_INVALID", setup.Span, "with env requires a record of valid environment names and string values without NUL")
			}
			for i := range r.Value.Record {
				field := r.Value.Record[i]
				assignment := owned(p.Alloc, field.Key+"="+field.Value.Text)
				replaced := false
				for j := range state.Environment {
					if strings.HasPrefix(state.Environment[j], field.Key+"=") {
						mem.FreeString(p.Alloc, state.Environment[j])
						state.Environment[j], replaced = assignment, true
						break
					}
				}
				if !replaced {
					state.Environment = append(state.Environment, assignment)
				}
			}
			r.Free(c.Run)
		}
		if p.DefinitionEffectSink != nil {
			p.streamControlOutput(c, before)
		}
		for i := before; i < len(c.Effects); i++ {
			effect := c.Effects[i]
			effect.Data = slices.Clone(effect.Data)
			state.Effects = append(state.Effects, effect)
		}
		for i := paths; i < len(c.WritePaths); i++ {
			state.Paths = append(state.Paths, owned(p.Alloc, c.WritePaths[i]))
		}
		state.Index++
	}
	previousCwd, previousEnvironment := c.Cwd, c.Environment
	c.Cwd, c.Environment = state.Cwd, state.Environment
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	r := p.invokeKashBody(nil, e, scope, c.Args, c)
	c.Cwd, c.Environment = previousCwd, previousEnvironment
	c.operationStart, c.operationEnd = start, end
	return r
}
