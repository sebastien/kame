package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type finalizerBodyState struct {
	Scope   *Scope
	Index   int
	Done    bool
	Outcome Result
	Effects []Effect
	Paths   []string
}

func freeFinalizerBody(a mem.Allocator, value any) {
	s := value.(*finalizerBodyState)
	s.Scope.Free()
	s.Outcome.Free(a)
	FreeEffects(a, s.Effects)
	for i := range s.Paths {
		mem.FreeString(a, s.Paths[i])
	}
	slices.Free(a, s.Paths)
	mem.Free(a, s)
}

// Unlike expression replay, a finalizer invocation must not repeat completed
// statements or consume a later host completion at an earlier write! site.
// Keep its lexical scope as well: preceding def bindings survive suspension.
func (p *Program) finalizerBody(scope *Scope, body []*expr.Expr, c *Context) Result {
	path, start, end := c.CallPath, c.operationStart, c.operationEnd
	c.CallPath = invocationPath(c.Run, path, -2, body[0].Span.Start)
	c.operationStart, c.operationEnd = body[0].Span.Start, body[len(body)-1].Span.End
	var s *finalizerBodyState
	if stored := c.OperationState(); stored != nil {
		s = stored.(*finalizerBodyState)
	}
	if s == nil {
		s = mem.Alloc[finalizerBodyState](p.Alloc)
		s.Scope = scope
		s.Scope.Retain()
		c.SetOperationState(s, freeFinalizerBody)
	}
	mem.FreeString(c.Run, c.CallPath)
	c.CallPath, c.operationStart, c.operationEnd = path, start, end
	if s.Done {
		if s.Outcome.Diagnostic.Code == "" {
			replayFinalizerBodyEffects(s, c)
		}
		return cloneFinalizerResult(c.Run, &s.Outcome)
	}
	for s.Index < len(body) {
		before, paths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, s.Scope, body[s.Index], c)
		if r.Waiting || r.Diagnostic.Code != "" {
			for i := before; i < len(c.Effects); i++ {
				c.Effects[i].Free(c.Run)
			}
			c.Effects = c.Effects[:before]
			for i := paths; i < len(c.WritePaths); i++ {
				mem.FreeString(c.Run, c.WritePaths[i])
			}
			c.WritePaths = c.WritePaths[:paths]
			if !r.Waiting {
				s.Done, s.Outcome = true, cloneFinalizerResult(p.Alloc, &r)
			}
			return r
		}
		for i := before; i < len(c.Effects); i++ {
			s.Effects = slices.Append(p.Alloc, s.Effects, c.Effects[i])
		}
		c.Effects = c.Effects[:before]
		for i := paths; i < len(c.WritePaths); i++ {
			s.Paths = slices.Append(p.Alloc, s.Paths, c.WritePaths[i])
		}
		c.WritePaths = c.WritePaths[:paths]
		s.Index++
		if s.Index == len(body) {
			replayFinalizerBodyEffects(s, c)
			s.Done, s.Outcome = true, cloneFinalizerResult(p.Alloc, &r)
			return r
		}
		r.Free(c.Run)
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

func replayFinalizerBodyEffects(s *finalizerBodyState, c *Context) {
	for i := range s.Effects {
		effect := s.Effects[i]
		effect.Data = slices.Clone(c.Run, effect.Data)
		c.Effects = slices.Append(c.Run, c.Effects, effect)
	}
	for i := range s.Paths {
		c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, s.Paths[i]))
	}
}
