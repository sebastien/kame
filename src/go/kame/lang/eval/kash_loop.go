package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
)

type kashLoopState struct {
	Program     *Program
	ReaderID    int64
	RecordStage *functionStage
	RequestID   int64
	Path        string
	Delimiter   int
	Stdin       bool
	Control     *kashControlState
	Budget      kashInvocation
	Iterable    core.Value
	Ready       bool
	InBody      bool
	Done        bool
	Iteration   int
	Consumer    int64
	Closing     bool
	Terminal    Result
}

func freeKashLoop(a mem.Allocator, value any) {
	state := value.(*kashLoopState)
	closeKashReader(state)
	mem.FreeString(a, state.Path)
	freeKashControlState(a, state.Control)
	freeCallables(a, &state.Iterable)
	state.Iterable.Free(a)
	state.Terminal.Free(a)
	mem.Free(a, state)
}

func (p *Program) kashLoop(scope *Scope, e *expr.Expr, c *Context) Result {
	if (c.Phase != EvaluatePhase || c.PurePredicate) && (!c.pureValueBlock || e.Kind == expr.KashRecords) {
		return failure(c.Run, "PHASE_INVALID", e.Span, "Kash iteration requires runtime execution")
	}
	if p.DryRun && !c.pureValueBlock {
		return Result{}
	}
	if c.Engine == nil && !c.pureValueBlock {
		return failure(c.Run, "HOST_FAIL", e.Span, "Kash iteration requires an execution engine")
	}
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *kashLoopState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashLoopState)
	}
	if state == nil {
		state = mem.Alloc[kashLoopState](p.Alloc)
		state.Program = p
		state.Control = mem.Alloc[kashControlState](p.Alloc)
		state.Budget.TimedIndex = -1
		state.Budget.TimeoutLimitMS = c.TimeoutMS
		if c.Engine == nil {
			defer freeKashLoop(p.Alloc, state)
		} else {
			c.SetOperationState(state, freeKashLoop)
		}
	}
	c.operationStart, c.operationEnd = start, end
	replayControlEffects(state.Control, c)
	if state.Done {
		return Result{}
	}
	if e.Kind == expr.KashRecords && !state.Ready {
		r := p.kashRecordSource(scope, e, state, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if state.Done {
			return Result{}
		}
	}
	if e.Kind == expr.KashFor && !state.Ready {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, scope, e.Items[0].Items[0], c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if r.Value.Kind != core.List && r.Value.Kind != core.Stream {
			r.Free(c.Run)
			return failure(c.Run, "EXPR_INVALID", e.Span, "for iterable must be a list or stream")
		}
		state.Iterable, state.Ready = borrowValue(p.Alloc, r.Value), true
		if r.Value.Kind == core.Stream {
			p.NextScriptGroup++
			state.Consumer = p.NextScriptGroup
		}
		freeCallables(c.Run, &r.Value)
		r.Free(c.Run)
		p.retainControlEffects(state.Control, c, scope, before, beforePaths)
	}
	previousPath, previousTimeout := c.CallPath, c.TimeoutMS
	c.loopDepth++
	var r Result
	for {
		c.CallPath = invocationPath(c.Run, previousPath, e.Span.Start, state.Iteration)
		r = p.advanceKashLoop(scope, e, state, c)
		mem.FreeString(c.Run, c.CallPath)
		if !c.pureValueBlock || state.Done || r.Waiting || r.Yielded || r.Returned || r.Diagnostic.Code != "" {
			break
		}
		r.Free(c.Run)
	}
	if e.Kind == expr.KashRecords && (state.Done || r.Returned || r.Diagnostic.Code != "") {
		closeKashReader(state)
	}
	c.loopDepth--
	c.CallPath, c.TimeoutMS = previousPath, previousTimeout
	return r
}

func (p *Program) advanceKashLoop(scope *Scope, e *expr.Expr, state *kashLoopState, c *Context) Result {
	var stream *generator
	if state.Iterable.Kind == core.Stream {
		if c.Engine == nil || c.pureValueBlock {
			return failure(c.Run, "PHASE_INVALID", e.Span, "stream iteration requires runtime execution")
		}
		stream = streamGenerator(state.Iterable)
		if stream == nil {
			return failure(c.Run, "EXPR_INVALID", e.Span, "unsupported stream source")
		}
	}
	if state.Closing {
		return closeGeneratorLoop(state, stream, c)
	}
	if e.Kind == expr.KashFor && stream == nil && state.Iteration >= len(state.Iterable.List) {
		state.Done = true
		return Result{}
	}
	// One clock boundary per iteration yields to the host even for pure infinite
	// loops, so interruption and deadlines remain serviceable on native and WASM.
	if !c.pureValueBlock {
		clock := p.kashBudget(&state.Budget, c)
		if clock.Waiting || clock.Diagnostic.Code != "" {
			return clock
		}
		if state.Budget.TimeoutLimitMS > 0 {
			c.TimeoutMS = state.Budget.RemainingMS
		}
	}
	control, branch := state.Control, e.Items[0]
	if !state.InBody {
		if e.Kind == expr.KashRecords {
			r := p.kashRecordNext(e, state, c)
			if r.Waiting || r.Diagnostic.Code != "" || state.Done {
				return r
			}
		}
		if e.Kind == expr.KashWhile {
			before, beforePaths := len(c.Effects), len(c.WritePaths)
			r := p.evaluate(c.Engine, scope, branch.Items[0], c)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			selected := r.Value.Kind != core.Nil && !(r.Value.Kind == core.Bool && !r.Value.Bool)
			freeCallables(c.Run, &r.Value)
			r.Free(c.Run)
			p.retainControlEffects(control, c, scope, before, beforePaths)
			if !selected {
				state.Done = true
				return Result{}
			}
		}
		if c.pureValueBlock {
			// Pure phases do not submit clock requests between iterations.
			const maxPureLoopIterations = 100000
			if c.pureLoopIterations >= maxPureLoopIterations || state.Iteration >= maxPureLoopIterations {
				return failure(c.Run, "EVAL_LIMIT", e.Span, "pure value blocks exceeded the loop iteration limit")
			}
			c.pureLoopIterations++
		}
		control.Scope = newScope(p.Alloc, scope)
		if e.Kind == expr.KashFor || e.Kind == expr.KashRecords {
			value := state.Iterable
			if stream != nil {
				before, beforePaths := len(c.Effects), len(c.WritePaths)
				next := stream.pull(c, state.Consumer)
				if next.Waiting || next.Diagnostic.Code != "" || !next.Yielded {
					control.Scope.Free()
					control.Scope = nil
					if !next.Waiting && next.Diagnostic.Code == "" {
						p.retainControlEffects(control, c, scope, before, beforePaths)
						state.Done = true
					}
					return next
				}
				p.retainControlEffects(control, c, scope, before, beforePaths)
				defer next.Free(c.Run)
				value = next.Value
			} else if e.Kind == expr.KashFor {
				value = state.Iterable.List[state.Iteration]
			}
			item := borrowValue(p.Alloc, value)
			control.Scope.setValue(e.Text, item)
			item.Free(p.Alloc)
		}
		initialized := p.kashBodyDefinitions(control.Scope, branch.Body, c)
		if initialized.Diagnostic.Code != "" {
			return initialized
		}
		state.InBody = true
		state.Budget.Index++
	}
	previousBody := c.RecordBody
	if e.Kind == expr.KashRecords {
		c.RecordBody = true
	}
	var budget *kashInvocation
	if !c.pureValueBlock {
		budget = &state.Budget
	}
	r := p.kashBodyBudget(control, branch.Body, c, budget)
	c.RecordBody = previousBody
	if r.Waiting || r.Yielded {
		return r
	}
	if stream != nil && (r.Returned || r.Diagnostic.Code != "" || r.LoopTransfer == expr.KashBreak) {
		state.Closing = true
		state.Terminal = Result{Value: borrowValue(p.Alloc, r.Value), Returned: r.Returned, Diagnostic: r.Diagnostic.Clone(p.Alloc)}
		r.Free(c.Run)
		return closeGeneratorLoop(state, stream, c)
	}
	if r.Returned || r.Diagnostic.Code != "" {
		return r
	}
	transfer := r.LoopTransfer
	r.Free(c.Run)
	control.Scope.Free()
	control.Scope = nil
	control.Value.Free(p.Alloc)
	control.Index = 0
	state.InBody = false
	if e.Kind == expr.KashRecords {
		state.Iterable.Free(p.Alloc)
	}
	// A completed iteration is never replayed: the loop state alone resumes
	// later ones. Retiring its progress keeps operation-state lookups bounded
	// by one iteration rather than growing with every completed one.
	if c.Engine != nil {
		p.retireRecordProgress(c)
	}
	if transfer == expr.KashBreak {
		state.Done = true
		return Result{}
	}
	state.Iteration++
	state.Budget.Index++
	if e.Kind == expr.KashFor && stream == nil && state.Iteration == len(state.Iterable.List) {
		state.Done = true
		return Result{}
	}
	// Submit the next iteration's clock now, with no recursive evaluation.
	if !c.pureValueBlock {
		return p.kashBudget(&state.Budget, c)
	}
	return Result{}
}

func closeGeneratorLoop(state *kashLoopState, stream *generator, c *Context) Result {
	cleanup := stream.stop(c)
	if cleanup.Waiting {
		return cleanup
	}
	terminal := Result{
		Value: borrowValue(c.Run, state.Terminal.Value), Returned: state.Terminal.Returned,
		Diagnostic: state.Terminal.Diagnostic.Clone(c.Run),
	}
	state.Terminal.Free(stream.program.Alloc)
	state.Done = true
	if cleanup.Diagnostic.Code != "" && terminal.Diagnostic.Code != "" {
		terminal.Diagnostic.Notes = append(terminal.Diagnostic.Notes, owned(c.Run, "generator cleanup failed: "+cleanup.Diagnostic.Message))
	} else if cleanup.Diagnostic.Code != "" {
		terminal.Free(c.Run)
		return cleanup
	}
	cleanup.Free(c.Run)
	return terminal
}
