package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"strconv"
)

type generator struct {
	program    *Program
	stream     *core.OwnedStream
	scope      *Scope
	body       *expr.Expr
	args       []core.Value
	path       string
	consumer   int64
	node       int64
	generation int64
	effects    int
	writes     int
	closeError diagnostic.Diagnostic
}

func (p *Program) newGenerator(body *expr.Expr, scope *Scope, c *Context) Result {
	if c.Phase != EvaluatePhase || c.PurePredicate || c.pureValueBlock {
		return failure(c.Run, "PHASE_INVALID", body.Span, "generator creation requires an action phase")
	}
	p.NextScriptGroup++
	g := &generator{
		program: p, scope: scope, body: expr.Clone(p.Alloc, body),
		path: "/generator-" + strconv.FormatInt(p.NextScriptGroup, 10),
		args: make([]core.Value, len(c.Args)),
	}
	scope.Retain()
	for i, arg := range c.Args {
		g.args[i] = borrowValue(p.Alloc, arg)
	}
	if p.generators == nil {
		p.generators = make(map[*generator]struct{})
	}
	p.generators[g] = struct{}{}
	value := core.NewStream(g, g.releaseCapture)
	g.stream = value.Stream
	return Result{Value: value}
}

func (g *generator) releaseCapture() {
	if g.scope == nil {
		return
	}
	if !g.stream.InvocationState.Terminal() {
		g.stream.InvocationState = core.StreamCancelled
	}
	scope, args, body := g.scope, g.args, g.body
	g.scope, g.args, g.body = nil, nil, nil
	delete(g.program.generators, g)
	g.closeError.Free(g.program.Alloc)
	freeValuesWithCallables(g.program.Alloc, args)
	expr.Free(g.program.Alloc, body)
	scope.Free()
}

func (g *generator) claim(c *Context, consumer int64) Result {
	if g.consumer == 0 {
		g.consumer = consumer
		g.node, g.generation = c.Engine.NodeID(), c.Engine.Generation()
		if c.generator != nil {
			g.path = c.generator.path + g.path
		}
	} else if g.consumer != consumer || g.node != c.Engine.NodeID() || g.generation != c.Engine.Generation() {
		return failure(c.Run, "EXPR_INVALID", c.Span, "stream already has a consumer")
	}
	return Result{}
}

func (g *generator) pull(c *Context, consumer int64) Result {
	if r := g.claim(c, consumer); r.Diagnostic.Code != "" {
		return r
	}
	if g.stream.InvocationState.Terminal() {
		return Result{}
	}
	g.stream.InvocationState = core.StreamRunning

	previousGenerator := c.generator
	path, start, end := c.CallPath, c.operationStart, c.operationEnd
	origin, entry, source := c.SourceOrigin, c.EntrySource, c.Source
	effects, paths := c.Effects, c.WritePaths
	c.generator, c.CallPath = g, g.path
	c.operationStart, c.operationEnd = g.body.Span.Start, g.body.Span.End
	c.SourceOrigin, c.EntrySource = g.body.SourceOrigin, g.body.EntrySource
	if c.EntrySource != nil {
		c.Source = c.EntrySource.Name
	} else if c.SourceOrigin > 0 && g.program.Script != nil {
		c.Source = g.program.Script.Source.Name
	}
	c.Effects, c.WritePaths = nil, nil
	r := g.program.invokeKashBody(nil, g.body, g.scope, g.args, c)
	// The invocation replays completed effects on resume. Transfer only the
	// newly reached suffix to the consumer, which owns its own replay history.
	if !r.Waiting && r.Diagnostic.Code == "" {
		effectPrefix, pathPrefix := min(g.effects, len(c.Effects)), min(g.writes, len(c.WritePaths))
		effects = append(effects, c.Effects[effectPrefix:]...)
		paths = append(paths, c.WritePaths[pathPrefix:]...)
		g.effects, g.writes = len(c.Effects), len(c.WritePaths)
		c.Effects, c.WritePaths = c.Effects[:effectPrefix], c.WritePaths[:pathPrefix]
	}
	FreeEffects(c.Run, c.Effects)
	for _, discarded := range c.WritePaths {
		mem.FreeString(c.Run, discarded)
	}
	mem.FreeSlice(nil, c.WritePaths)
	c.Effects, c.WritePaths = effects, paths
	c.generator, c.CallPath = previousGenerator, path
	c.operationStart, c.operationEnd = start, end
	c.SourceOrigin, c.EntrySource, c.Source = origin, entry, source
	switch {
	case r.Waiting:
		// This pull is still active while its host work is pending.
	case r.Diagnostic.Cancelled:
		g.finish(c, core.StreamCancelled)
	case r.Diagnostic.Code != "":
		g.finish(c, core.StreamFailed)
	case r.Yielded:
		g.stream.InvocationState = core.StreamSuspended
	default:
		g.finish(c, core.StreamCompleted)
	}
	return r
}

func (g *generator) stop(c *Context) Result {
	if g.stream.InvocationState.Terminal() {
		return Result{}
	}
	g.stream.InvocationState = core.StreamRunning
	p := g.program
	for _, scope := range p.DeferScopes {
		if scope.Owner == g.node && callPathWithin(scope.CallPath, g.path) {
			p.cancelScriptGroup(scope.Group)
		}
	}
	for i := len(p.DeferScopes) - 1; i >= 0; i-- {
		scope := p.DeferScopes[i]
		if scope.Owner != g.node || scope.Observed || !callPathWithin(scope.CallPath, g.path) {
			continue
		}
		result := p.finishDefers(scope, c, true)
		if result.Waiting {
			return result
		}
		if g.closeError.Code == "" {
			g.closeError = result.Diagnostic.Clone(p.Alloc)
		}
		result.Free(c.Run)
	}
	result := Result{Diagnostic: g.closeError.Clone(c.Run)}
	g.finish(c, core.StreamCancelled)
	return result
}

func (g *generator) finish(c *Context, state core.StreamInvocationState) {
	g.stream.InvocationState = state
	g.program.retireProgress(g.node, g.path, c.Engine)
	for nested := range g.program.generators {
		if nested != g && nested.node == g.node && callPathWithin(nested.path, g.path) {
			nested.releaseCapture()
		}
	}
	g.releaseCapture()
}
