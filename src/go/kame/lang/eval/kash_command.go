package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"strings"
)

type commandHeads struct {
	typed     map[int]*core.Value
	emitters  map[int]*Function
	callables map[int]*core.Value
}

// localCommand handles command heads that resolve inside the evaluator. Only
// unresolved ordinary names are left for executable lookup.
func (p *Program) localCommand(graph *expr.Expr, c *Context) (Result, bool, commandHeads) {
	var heads commandHeads
	for index, stage := range graph.Items {
		head := expr.CommandHead(stage)
		if head == nil {
			continue
		}
		var callee *expr.Expr
		single := len(graph.Items) == 1 && !graph.Topology
		if literalSetupWord(head) {
			spelling := head.Parts[0].Text
			imported, _ := p.findImport(head.SourceOrigin, spelling)
			lexical := c.Scope.lookupResolved(spelling, head.LetBinding) != nil || imported != nil ||
				p.moduleImport(head.SourceOrigin, spelling).module != nil
			if !lexical {
				if spelling == "source" && len(stage.Items) < 5 {
					return p.nativeSourceCommand(graph, stage, c), true, heads
				}
				if r, handled := p.foreignShellCommand(graph, stage, spelling, c); handled {
					return r, true, heads
				}
			}
			if !lexical && spelling == "cd" {
				return p.directoryCommand(graph, stage, head, nil, c), true, heads
			}
			if !lexical && spelling == "wait" {
				return p.waitCommand(graph, stage, c), true, heads
			}
			if !lexical && spelling == "kill" {
				return p.killCommand(graph, stage, c), true, heads
			}
			if !lexical && p.CommandAlias != nil {
				alias := p.CommandAlias(p.CommandAliasState, spelling)
				if len(alias) != 0 && alias[0] == "cd" {
					return p.directoryCommand(graph, stage, head, alias[1:], c), true, heads
				}
			}
			if strings.HasPrefix(spelling, "x/") || (!lexical && spelling != "out!" && spelling != "err!" && !strings.HasPrefix(spelling, "std/")) {
				continue
			}
			callee = &expr.Expr{Kind: expr.Name, Text: spelling, LetBinding: head.LetBinding, Span: head.Span, Kash: true, EntrySource: head.EntrySource, SourceOrigin: head.SourceOrigin}
		} else if head.Bool && !strings.HasPrefix(head.Parts[0].Form, "$...") {
			callee = head.Parts[0].Expr
		} else {
			continue
		}
		if c.Engine == nil {
			return failure(c.Run, "HOST_FAIL", head.Span, "command execution requires an execution host"), true, heads
		}
		value, r := p.resolveCommandCallee(callee, head, c)
		if value == nil {
			return r, true, heads
		}
		if value.Kind != core.Callable {
			if !literalSetupWord(head) {
				if heads.typed == nil {
					heads.typed = make(map[int]*core.Value)
				}
				heads.typed[index] = value
				continue
			}
			return failure(c.Run, "EXPR_INVALID", head.Span, "command head must be callable"), true, heads
		}
		function := value.Callable.(*Function)
		emitter := function.Kind == FunctionOperation && (function.OperationName == "out!" || function.OperationName == "err!")
		generator := function.Expression != nil && function.Expression.Kind == expr.KashFunction && function.Expression.Bool
		functionCommand := !generator && single && c.PrepareDeferred == nil && commandOnlyWords(stage) &&
			(graph.Kind == expr.CommandCapture || graph.Async || graph.AcceptExit ||
				(c.asyncHandle != nil && c.asyncHandle.task != nil && c.asyncHandle.task.Graph == graph))
		if function.Kind != FunctionOperation && function.Kind != FunctionInterpreter && functionCommand {
			if graph.Async {
				return p.startAsyncCapture(graph, c), true, heads
			}
			return p.captureFunctionCommand(function, graph, stage, c), true, heads
		}
		if generator || !single || graph.Kind != expr.CommandGraph || graph.Async || graph.AcceptExit || len(graph.Body) != 0 || c.PrepareDeferred != nil || !commandOnlyWords(stage) {
			if emitter {
				if heads.emitters == nil {
					heads.emitters = make(map[int]*Function)
				}
				heads.emitters[index] = function
				continue
			}
			if function.Kind != FunctionOperation && function.Kind != FunctionInterpreter && !stage.Each {
				if heads.callables == nil {
					heads.callables = make(map[int]*core.Value)
				}
				heads.callables[index] = value
				continue
			}
			return failure(c.Run, "FEATURE_UNSUP", head.Span, "this command form requires callable process stages"), true, heads
		}
		scope := c.Scope
		before := len(c.Effects)
		r = p.call(function, commandArguments(stage), c, graph.Span)
		c.Scope = scope
		// Direct emitters finish writing to their sink at the command boundary.
		if emitter && !r.Waiting && r.Diagnostic.Code == "" && c.hasControlOutputSink() {
			p.streamControlOutput(c, before)
		}
		freeCallables(c.Run, &r.Value)
		r.Value.Free(c.Run)
		r.Value = core.Value{}
		return r, true, heads
	}
	return Result{}, false, heads
}

func commandArguments(stage *expr.Expr) []*expr.Expr {
	arguments := make([]*expr.Expr, 0, len(stage.Items)-1)
	for _, word := range stage.Items[1:] {
		argument := word
		if word.Bool && strings.HasPrefix(word.Parts[0].Form, "$...") {
			argument = &expr.Expr{Kind: expr.ListSpread, Span: word.Span, Items: []*expr.Expr{word}}
		}
		arguments = append(arguments, argument)
	}
	return arguments
}

type waitCommandState struct {
	operands *kashControlState
	handles  []*ProcessHandle
}

func freeWaitCommandState(a mem.Allocator, value any) {
	state := value.(*waitCommandState)
	freeKashControlState(a, state.operands)
}

func (p *Program) waitCommand(graph, stage *expr.Expr, c *Context) Result {
	if len(graph.Items) != 1 || graph.Topology || graph.Async || len(graph.Body) != 0 || c.PrepareDeferred != nil || !commandOnlyWords(stage) {
		return failure(c.Run, "FEATURE_UNSUP", stage.Span, "this wait form requires callable process stages")
	}
	start, end, path := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, path+"/wait"
	defer func() {
		c.operationStart, c.operationEnd, c.CallPath = start, end, path
	}()
	state, _ := c.OperationState().(*waitCommandState)
	if state == nil {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		handles, r := p.waitOperands(stage, c)
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r
		}
		state = &waitCommandState{handles: handles, operands: mem.Alloc[kashControlState](p.Alloc)}
		p.retainControlEffects(state.operands, c, c.Scope, before, beforePaths)
		c.SetOperationState(state, freeWaitCommandState)
	} else {
		replayControlEffects(state.operands, c)
	}
	outcome := graph.ActionPrefix == "$?("
	for i, handle := range state.handles {
		r := p.waitProcess(handle, graph, c, outcome)
		if r.Waiting || r.Diagnostic.Code != "" || (outcome && i == len(state.handles)-1) {
			return r
		}
		r.Free(c.Run)
	}
	if outcome {
		if p.DryRun {
			return captureOutcome(c, graph, core.Value{}, core.Value{})
		}
		return captureOutcome(c, graph, core.Value{Kind: core.Record}, core.Value{})
	}
	if graph.Kind == expr.CommandCapture {
		return Result{Value: core.NewString(c.Run, "")}
	}
	return Result{}
}

func (p *Program) waitOperands(stage *expr.Expr, c *Context) ([]*ProcessHandle, Result) {
	operands := p.argumentValues(c.Scope, stage.Items[1:], c, stage.Span)
	if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
		return nil, operands.Result
	}
	defer freeValuesWithCallables(c.Run, operands.Values)
	handles, r := p.processOperands("wait", operands.Values, stage.Items[1:], c)
	if r.Diagnostic.Code != "" {
		return nil, r
	}
	if len(operands.Values) == 0 {
		for _, task := range p.Processes {
			if !task.Observed && task.Handle != c.asyncHandle && p.ownsProcess(task.Handle, c) {
				handles = append(handles, task.Handle)
			}
		}
	}
	return handles, Result{}
}

func (p *Program) processOperands(command string, values []core.Value, words []*expr.Expr, c *Context) ([]*ProcessHandle, Result) {
	var handles []*ProcessHandle
	for i, value := range values {
		if p.DryRun && value.Kind == core.Process && value.Process == p {
			continue
		}
		handle, _ := value.Process.(*ProcessHandle)
		if value.Kind != core.Process || !p.ownsProcess(handle, c) {
			return nil, failure(c.Run, "EXPR_INVALID", words[i].Span, command+" requires an owned process handle")
		}
		handles = append(handles, handle)
	}
	return handles, Result{}
}

type commandCalleeState struct {
	head    kashControlState
	attempt int64
}

func freeCommandCalleeState(a mem.Allocator, value any) {
	state := value.(*commandCalleeState)
	freeCallables(a, &state.head.Value)
	state.head.Value.Free(a)
	FreeEffects(a, state.head.Effects)
	for _, name := range state.head.WritePaths {
		mem.FreeString(a, name)
	}
	mem.Free(a, state)
}

// Resolve the head once before evaluating argv. A resumed call or process
// preparation reuses the same value and replays only its retained effects.
func (p *Program) resolveCommandCallee(e, head *expr.Expr, c *Context) (*core.Value, Result) {
	previousStart, previousEnd, previousPath := c.operationStart, c.operationEnd, c.CallPath
	key := previousPath + "/command-head"
	c.operationStart, c.operationEnd, c.CallPath = head.Span.Start, head.Span.End, key
	state, _ := c.OperationState().(*commandCalleeState)
	c.operationStart, c.operationEnd, c.CallPath = previousStart, previousEnd, previousPath
	if state != nil {
		if state.attempt != c.Engine.Attempt() {
			replayControlEffects(&state.head, c)
			state.attempt = c.Engine.Attempt()
		}
		return &state.head.Value, Result{}
	}
	before, beforePaths := len(c.Effects), len(c.WritePaths)
	r := p.evaluate(c.Engine, c.Scope, e, c)
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return nil, r
	}
	state = mem.Alloc[commandCalleeState](p.Alloc)
	state.head.Value = borrowValue(p.Alloc, r.Value)
	state.attempt = c.Engine.Attempt()
	r.Free(c.Run)
	for _, effect := range c.Effects[before:] {
		state.head.Effects = append(state.head.Effects, effect.clone())
	}
	for _, name := range c.WritePaths[beforePaths:] {
		state.head.WritePaths = append(state.head.WritePaths, owned(p.Alloc, name))
	}
	c.operationStart, c.operationEnd, c.CallPath = head.Span.Start, head.Span.End, key
	c.SetOperationState(state, freeCommandCalleeState)
	c.operationStart, c.operationEnd, c.CallPath = previousStart, previousEnd, previousPath
	return &state.head.Value, Result{}
}

func commandOnlyWords(stage *expr.Expr) bool {
	for _, word := range stage.Items {
		if word.Kind != expr.CommandWord {
			return false
		}
	}
	return true
}

func (p *Program) prepareEmittedStage(function *Function, state *captureState, c *Context, stage *expr.Expr) Result {
	operation := p.operationForCallable(function)
	if operation == nil {
		return failure(c.Run, "EXPR_INVALID", stage.Span, "output operation is unavailable")
	}
	if r := p.prepareOperation(operation, len(state.Argv), c, stage.Span); r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	values := state.Argv
	state.Argv = nil
	before := len(c.Effects)
	r := p.invokeOperation(operation, values, nil, c, stage.Span)
	attachNamedFrame(&r, c, stage.Span, "operation", operation.Name)
	freeValues(state.Alloc, values)
	FreeEffects(c.Run, c.Effects[before:])
	c.Effects = c.Effects[:before]
	if r.Diagnostic.Code != "" || r.Waiting || r.Completed || r.Value.Kind == core.Bytes {
		return r
	}
	data := core.NewBytes(c.Run, []byte(r.Value.Text))
	r.Free(c.Run)
	return Result{Value: data}
}
