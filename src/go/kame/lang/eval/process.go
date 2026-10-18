package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

// Progress is generation-owned: replaying an outer application must neither
// relaunch an earlier substitution nor let it consume a later completion.
type captureState struct {
	Program *Program
	PredicateScope *Scope
	Predicates []*expr.Expr
	OperandSlots []core.Value
	Alloc  mem.Allocator
	Index  int
	Stage  int
	Stages []core.Value
	Argv   []core.Value
	Input string
	Output string
	Append bool
	Setups []core.Value
	Environment []core.Value
	Cwd string
	Timeout int64
	ID     int64
	Done   bool
	Async bool
	Recovering bool
	Value  core.Value
	Effects []Effect
	WritePaths []string
}

func freeCaptureState(a mem.Allocator, value any) {
	s := value.(*captureState)
	if s.Program != nil { for i := range s.Program.StreamStates { if s.Program.StreamStates[i] == s { copy(s.Program.StreamStates[i:], s.Program.StreamStates[i+1:]); s.Program.StreamStates = s.Program.StreamStates[:len(s.Program.StreamStates)-1]; break } } }
	if s.PredicateScope != nil { s.PredicateScope.Free() }
	for i := range s.Predicates { expr.Free(a, s.Predicates[i]) }
	slices.Free(a, s.Predicates)
	for i := range s.OperandSlots { s.OperandSlots[i].Free(a) }
	slices.Free(a, s.OperandSlots)
	for i := range s.Argv {
		s.Argv[i].Free(a)
	}
	slices.Free(a, s.Argv)
	for i := range s.Stages {
		s.Stages[i].Free(a)
	}
	slices.Free(a, s.Stages)
	mem.FreeString(a, s.Input)
	mem.FreeString(a, s.Output)
	mem.FreeString(a, s.Cwd)
	for i := range s.Setups { s.Setups[i].Free(a) }
	slices.Free(a, s.Setups)
	for i := range s.Environment { s.Environment[i].Free(a) }
	slices.Free(a, s.Environment)
	s.Value.Free(a)
	FreeEffects(a, s.Effects)
	for i := range s.WritePaths { mem.FreeString(a, s.WritePaths[i]) }
	slices.Free(a, s.WritePaths)
	mem.Free(a, s)
}

func (p *Program) capture(e *expr.Expr, c *Context) Result {
	if c.PurePredicate { return failure(c.Run, "KASH_PREDICATE_EFFECT", e.Span, "stream predicates cannot execute processes") }
	if !e.Topology && hasProcessTopology(e) {
		return p.captureTopology(e, c)
	}
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		return failure(c.Run, "PHASE_INVALID", e.Span, "process execution is invalid in this phase")
	}
	if !c.allowed(Run) {
		return failure(c.Run, "CAP_DENIED", e.Span, "command substitution requires run capability")
	}
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	r := p.captureRequest(e, c)
	c.operationStart, c.operationEnd = previousStart, previousEnd
	return r
}

func (p *Program) captureRequest(e *expr.Expr, c *Context) Result {
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "process execution requires an execution host")
	}
	var state *captureState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*captureState)
	}
	if state == nil {
		state = mem.Alloc[captureState](p.Alloc)
		state.Alloc = p.Alloc
		c.SetOperationState(state, freeCaptureState)
	}
	if e.Topology && state.Program == nil {
		state.Program, state.PredicateScope = p, c.Scope
		c.Scope.Retain()
		for i := range e.Items {
			var predicate *expr.Expr
			if e.Items[i].Kind == expr.CommandMux && e.Items[i].Text == "filter" { predicate = expr.Clone(state.Alloc, e.Items[i].Items[0]) }
			state.Predicates = slices.Append(state.Alloc, state.Predicates, predicate)
		}
		p.StreamStates = slices.Append(p.Alloc, p.StreamStates, state)
	}
	for i := range state.Effects { effect := state.Effects[i]; c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)}) }
	for i := range state.WritePaths { c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, state.WritePaths[i])) }
	if e.Topology {
		for i := range state.Setups {
			output := host.PayloadText(state.Setups[i], host.FieldOutput)
			if output != "" { c.Emit(EffectProcessWrite, []byte(output)) }
			input := host.PayloadText(state.Setups[i], host.FieldInput)
			if !demandProcessInput(c, input) { return Result{Waiting: true} }
		}
	}
	if state.Recovering {
		if state.Output != "" { c.Emit(EffectProcessWrite, []byte(state.Output)) }
		return p.capture(e.Body[0], c)
	}
	if state.Stage == len(e.Items) && !demandProcessInput(c, state.Input) { return Result{Waiting: true} }
	if state.Done {
		if state.Output != "" { c.Emit(EffectProcessWrite, []byte(state.Output)) }
		return Result{Value: state.Value.Clone(c.Run)}
	}
	if state.ID != 0 {
		if state.Output != "" { c.Emit(EffectProcessWrite, []byte(state.Output)) }
		if c.Completion().RequestID != state.ID {
			return Result{Waiting: true}
		}
		completion := c.TakeCompletion()
		if completion.Diagnostic.Code != "" {
			r := Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			if r.Diagnostic.Span.Start == 0 && r.Diagnostic.Span.End == 0 {
				r.Diagnostic.Span.Start, r.Diagnostic.Span.End = e.Span.Start, e.Span.End
			}
			return p.captureRecovery(e, c, state, r)
		}
		if !completion.HasValue {
			return failure(c.Run, "HOST_FAIL", e.Span, "process completion omitted its result")
		}
		if e.Kind == expr.CommandGraph || e.Kind == expr.CommandTest || e.Kind == expr.CommandResult {
			// A statement streams bytes; only status is interpreted, never stdout.
			for i := range completion.Value.Record {
				if completion.Value.Record[i].Key == "stdout" { completion.Value.Record[i].Value.Free(c.Run); completion.Value.Record[i].Value = core.NewString(c.Run, "") }
				if completion.Value.Record[i].Key == "stdoutTruncated" { completion.Value.Record[i].Value.Bool = false }
			}
		}
		r := captureValue(c.Run, completion.Value, e.Span, e.AcceptExit || e.Kind == expr.CommandTest)
		if e.Topology && r.Diagnostic.Code == "RECIPE_FAIL" {
			stages := host.PayloadStages(completion.Value)
			for i := range stages {
				if i < len(e.Items) && e.Items[i].Kind == expr.CommandMux && host.PayloadInt(stages[i], "status") == host.PayloadInt(completion.Value, "status") {
					status := int(host.PayloadInt(stages[i], "status"))
					if (status >= StreamEncoding && status <= StreamPredicateFailure) || status == StreamBufferLimit { r.Free(c.Run); r = streamFailure(c, status, e.Items[i].Span); break }
				}
			}
		}
		if (e.Kind == expr.CommandResult || e.Kind == expr.CommandGraph) && r.Diagnostic.Code == "" {
			r.Value.Free(c.Run)
			r = processResult(c.Run, completion.Value, e.Span)
		}
		if e.Kind == expr.CommandTest && r.Diagnostic.Code == "" {
			r.Value.Free(c.Run)
			r.Value = core.Value{Kind: core.Bool, Bool: host.PayloadInt(completion.Value, "status") == 0 && host.PayloadInt(completion.Value, "signal") == 0}
		}
		if r.Diagnostic.Code == "RECIPE_FAIL" && !r.Diagnostic.Protected {
			c.handledFinalizerFailure()
			stages := host.PayloadStages(completion.Value)
			for i := range stages {
				if i >= len(e.Items) {
					break
				}
				for j := range stages[i].Record {
					field := stages[i].Record[j]
					if (field.Key == "status" || field.Key == "signal") && field.Value.Int != 0 {
						if e.Topology && field.Value.Int != host.PayloadInt(completion.Value, field.Key) { continue }
						r.Diagnostic.Span.Start, r.Diagnostic.Span.End = e.Items[i].Span.Start, e.Items[i].Span.End
					}
				}
			}
		}
		completion.Value.Free(c.Run)
		if r.Diagnostic.Code == "" {
			state.Value, state.Done = r.Value.Clone(state.Alloc), true
		}
		return p.captureRecovery(e, c, state, r)
	}
	for state.Stage < len(e.Items) {
		stage := e.Items[state.Stage]
		if stage.Kind == expr.CommandMux {
			state.Stages = slices.Append(state.Alloc, state.Stages, core.NewList(state.Alloc, nil))
			state.Setups = slices.Append(state.Alloc, state.Setups, host.StageSetupPayload(state.Alloc, "", 0, nil))
			state.Stage++
			continue
		}
		for state.Index < len(stage.Items) {
			word := stage.Items[state.Index]
			if word.Kind == expr.CommandOperand {
				if len(state.Argv) == 0 { return failure(c.Run, "EXPR_INVALID", word.Span, "live stream operand cannot be an executable") }
				slot := []core.Value{{Kind: core.Int, Int: int64(state.Index+1)}, {Kind: core.Int, Int: int64(len(state.Argv))}}
				state.OperandSlots = slices.Append(state.Alloc, state.OperandSlots, core.NewList(state.Alloc, slot))
				state.Argv = slices.Append(state.Alloc, state.Argv, core.NewString(state.Alloc, "<stream>"))
				state.Index++
				continue
			}
			if word.Kind == expr.CommandSetup {
				// Computed environments may fetch credentials. A dry-run defers
				// this runtime boundary instead of demanding or fabricating a record.
				if word.Text == "env" && p.DryRun { state.Index++; continue }
				before := len(c.Effects)
				beforePaths := len(c.WritePaths)
				r := p.commandWord(word.Items[0], c, "setup :"+word.Text)
				if r.Waiting || r.Diagnostic.Code != "" { return r }
				rememberArgumentEffects(state, c, before, beforePaths)
				if word.Text == "env" {
					if r.Value.Kind != core.Record { r.Free(c.Run); return failure(c.Run, "EXPR_INVALID", word.Span, "stage env requires a record of valid environment names and string values without NUL") }
					for i := range r.Value.Record {
						field := r.Value.Record[i]
						if !processSetupKey(field.Key) || field.Value.Kind != core.String || strings.IndexByte(field.Value.Text, 0) >= 0 {
							r.Free(c.Run)
							return failure(c.Run, "EXPR_INVALID", word.Span, "stage env requires a record of valid environment names and string values without NUL")
						}
					}
					for i := range r.Value.Record {
						field := r.Value.Record[i]
						state.Environment = slices.Append(state.Alloc, state.Environment, core.NewString(state.Alloc, field.Key+"="+field.Value.Text))
					}
					r.Free(c.Run)
					state.Index++
					continue
				}
				if word.Text == "async" {
					if r.Value.Kind != core.Bool { r.Free(c.Run); return failure(c.Run, "EXPR_INVALID", word.Span, "async setup requires a boolean") }
					async := r.Value.Bool
					r.Free(c.Run)
					state.Async = async
					state.Index++
					continue
				}
				text, ok := processScalar(c.Run, r.Value)
				r.Free(c.Run)
				if !ok { return failure(c.Run, "EXPR_INVALID", word.Span, "stage setup requires one scalar without NUL bytes") }
				if word.Text == "cwd" {
					if text == "" { mem.FreeString(c.Run, text); return failure(c.Run, "EXPR_INVALID", word.Span, "stage cwd must not be empty") }
					state.Cwd = canonicalPath(state.Alloc, c.Cwd, text)
				} else if word.Text == "timeout" {
					seconds, err := strconv.ParseFloat(text, 64)
					if err != nil || !(seconds > 0 && seconds * 1000 < 9007199254740991) { mem.FreeString(c.Run, text); return failure(c.Run, "EXPR_INVALID", word.Span, "stage timeout requires positive finite seconds") }
					state.Timeout = int64(seconds * 1000)
					if float64(state.Timeout) < seconds * 1000 { state.Timeout++ }
				} else {
					state.Environment = slices.Append(state.Alloc, state.Environment, core.NewString(state.Alloc, word.Text+"="+text))
				}
				mem.FreeString(c.Run, text)
				state.Index++
				continue
			}
			if word.Kind == expr.CommandRedirection {
				before := len(c.Effects)
				beforePaths := len(c.WritePaths)
				r := p.commandWord(word.Items[0], c, "redirection")
				if r.Waiting || r.Diagnostic.Code != "" { return r }
				rememberArgumentEffects(state, c, before, beforePaths)
				text, ok := processScalar(c.Run, r.Value)
				r.Free(c.Run)
				if !ok || text == "" { mem.FreeString(c.Run, text); return failure(c.Run, "EXPR_INVALID", word.Span, "redirection requires one nonempty scalar path without NUL bytes") }
				if word.Text == "<" { state.Input = owned(state.Alloc, text) } else { state.Output, state.Append = owned(state.Alloc, text), word.Text == ">>" }
				mem.FreeString(c.Run, text)
				state.Index++
				continue
			}
			before := len(c.Effects)
			beforePaths := len(c.WritePaths)
			destination := "argument"
			if len(state.Argv) == 0 { destination = "command head" }
			r := p.commandWord(word, c, destination)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			rememberArgumentEffects(state, c, before, beforePaths)
			spread := word.Bool && strings.HasPrefix(word.Parts[0].Form, "$...")
			if spread && r.Value.Kind != core.List {
				r.Free(c.Run)
				return failure(c.Run, "EXPR_INVALID", word.Span, "list spread requires a flat list of scalar arguments")
			}
			if len(state.Argv) == 0 {
				executable := r.Value
				if spread {
					if len(r.Value.List) == 0 {
						r.Free(c.Run)
						return failure(c.Run, "EXPR_INVALID", word.Span, "command list spread requires a nonempty list")
					}
					executable = r.Value.List[0]
				}
				if executable.Kind != core.String || executable.Text == "" || strings.IndexByte(executable.Text, 0) >= 0 {
					r.Free(c.Run)
					return failure(c.Run, "EXPR_INVALID", word.Span, "executable requires one nonempty string or path without NUL")
				}
			}
			if r.Value.Kind == core.List {
				for i := range r.Value.List {
					if !appendArgument(state, r.Value.List[i]) {
						r.Free(c.Run)
						return failure(c.Run, "EXPR_INVALID", word.Span, "command argument requires a scalar without NUL bytes")
					}
				}
			} else if !appendArgument(state, r.Value) {
				r.Free(c.Run)
				return failure(c.Run, "EXPR_INVALID", word.Span, "command argument requires a scalar without NUL bytes")
			}
			r.Free(c.Run)
			state.Index++
		}
		if len(state.Argv) == 0 || state.Argv[0].Text == "" {
			return failure(c.Run, "EXPR_INVALID", stage.Span, "expected nonempty executable")
		}
		cwd := state.Cwd
		if cwd == "" { cwd = c.Cwd }
		if state.Argv[0].ToolName == "" && strings.IndexByte(state.Argv[0].Text, '/') >= 0 {
			resolved := canonicalPath(state.Alloc, cwd, state.Argv[0].Text)
			state.Argv[0].Free(state.Alloc)
			state.Argv[0] = core.Value{Kind: core.String, Text: resolved}
		}
		headSpan := stage.Span
		for i := range stage.Items { if stage.Items[i].Kind == expr.CommandWord { headSpan = stage.Items[i].Span; break } }
		if !p.DryRun {
			toolName := state.Argv[0].ToolName
			if toolName == "" { toolName = state.Argv[0].Text }
			tool := c.TrackedTool(toolName, headSpan)
			if tool.Waiting { return tool }
			if tool.Diagnostic.Code != "" { return p.captureRecovery(e, c, state, tool) }
			state.Argv[0].Free(state.Alloc)
			state.Argv[0] = tool.Value
		}
		if !authorizeExecutable(c, state.Argv[0].Text) {
			return failure(c.Run, "CAP_DENIED", stage.Span, "executable access denied; PATH lookup requires unrestricted run capability")
		}
		for i := range stage.Items {
			word := stage.Items[i]
			if word.Kind != expr.CommandRedirection { continue }
			text, capability := state.Output, Write
			if word.Text == "<" { text, capability = state.Input, Read }
			resolved := canonicalPath(state.Alloc, cwd, text)
			if !c.Allows(capability, resolved) { mem.FreeString(state.Alloc, resolved); return failure(c.Run, "CAP_DENIED", word.Span, "redirection access denied") }
			mem.FreeString(state.Alloc, text)
			if word.Text == "<" { state.Input = resolved } else { state.Output = resolved }
		}
		if e.Topology && !demandProcessInput(c, state.Input) { return Result{Waiting: true} }
		var environment []core.Value
		for i := range c.Environment { environment = slices.Append(state.Alloc, environment, core.NewString(state.Alloc, c.Environment[i])) }
		for i := range state.Environment {
			assignment := state.Environment[i]
			equal := strings.IndexByte(assignment.Text, '=')
			replaced := false
			for j := range environment {
				if equal > 0 && strings.HasPrefix(environment[j].Text, assignment.Text[:equal+1]) {
					environment[j].Free(state.Alloc)
					environment[j] = assignment.Clone(state.Alloc)
					replaced = true
					break
				}
			}
			if !replaced { environment = slices.Append(state.Alloc, environment, assignment.Clone(state.Alloc)) }
		}
		timeout := state.Timeout
		if timeout == 0 { timeout = c.TimeoutMS }
		state.Setups = slices.Append(state.Alloc, state.Setups, host.StageSetupPayload(state.Alloc, state.Cwd, timeout, environment))
		if len(state.OperandSlots) != 0 {
			setup := &state.Setups[len(state.Setups)-1]
			setup.Record = slices.Append(state.Alloc, setup.Record, core.RecordField{Key: owned(state.Alloc, "operandSlots"), Value: core.NewList(state.Alloc, state.OperandSlots)})
			for i := range state.OperandSlots { state.OperandSlots[i].Free(state.Alloc) }; slices.Free(state.Alloc, state.OperandSlots); state.OperandSlots = nil
		}
		if e.Topology {
			host.ConfigureRedirections(state.Alloc, &state.Setups[len(state.Setups)-1], state.Input, state.Output, state.Append)
			mem.FreeString(state.Alloc, state.Input); mem.FreeString(state.Alloc, state.Output)
			state.Input, state.Output, state.Append = "", "", false
		}
		for i := range environment { environment[i].Free(state.Alloc) }
		slices.Free(state.Alloc, environment)
		mem.FreeString(state.Alloc, state.Cwd)
		state.Cwd, state.Timeout = "", 0
		for i := range state.Environment { state.Environment[i].Free(state.Alloc) }
		slices.Free(state.Alloc, state.Environment)
		state.Environment = nil
		state.Stages = slices.Append(state.Alloc, state.Stages, core.NewList(state.Alloc, state.Argv))
		for i := range state.Argv {
			state.Argv[i].Free(state.Alloc)
		}
		slices.Free(state.Alloc, state.Argv)
		state.Argv = nil
		state.Stage++
		state.Index = 0
	}
	if p.DryRun {
		if state.Async || e.Async { return Result{Value: core.Value{Kind: core.Process, Process: p}} }
		if e.Kind == expr.CommandTest { return Result{Value: core.Value{Kind: core.Bool, Bool: false}} }
		if e.Kind == expr.CommandResult || e.Kind == expr.CommandGraph {
			fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int}}}
			completion := core.NewRecord(c.Run, fields)
			r := processResult(c.Run, completion, e.Span)
			completion.Free(c.Run)
			return r
		}
		return Result{Value: core.NewString(c.Run, "")}
	}
	if !demandProcessInput(c, state.Input) { return Result{Waiting: true} }
	configured := false
	for i := range e.Items { for j := range e.Items[i].Items { if e.Items[i].Items[j].Kind == expr.CommandSetup { configured = true } } }
	var payload core.Value
	if e.Topology || configured || len(c.Environment) != 0 || c.TimeoutMS != 0 || len(state.Stages) > 1 || state.Input != "" || state.Output != "" || e.Kind == expr.CommandGraph || e.Kind == expr.CommandTest || e.Kind == expr.CommandResult || e.AcceptExit {
		payload = host.PipelinePayload(c.Run, state.Stages)
	} else { payload = host.ArgvPayload(c.Run, state.Stages[0].List) }
	if e.Topology {
		var kinds, edges, framed []core.Value
		for i := range e.Items { kind := ""; if e.Items[i].Kind == expr.CommandMux { kind = e.Items[i].Text }; kinds = slices.Append(c.Run, kinds, core.NewString(c.Run, kind)) }
		for i := range e.StreamKinds { framed = slices.Append(c.Run, framed, core.Value{Kind: core.Bool, Bool: e.StreamKinds[i] == 1}) }
		payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "framed"), Value: core.NewList(c.Run, framed)})
		slices.Free(c.Run, framed)
		for i := range e.Edges {
			connection := e.Edges[i]
			edge := []core.Value{{Kind: core.Int, Int: int64(connection.From)}, {Kind: core.Int, Int: int64(connection.To)}}
			value := core.NewList(c.Run, edge)
			if connection.Argument != 0 {
				actual := 0
				slots := host.PayloadList(state.Setups[connection.To], "operandSlots")
				for j := range slots { if len(slots[j].List) == 2 && slots[j].List[0].Int == int64(connection.Argument) { actual = int(slots[j].List[1].Int) } }
				if actual == 0 { value.Free(c.Run); payload.Free(c.Run); for j := range kinds { kinds[j].Free(c.Run) }; slices.Free(c.Run, kinds); for j := range edges { edges[j].Free(c.Run) }; slices.Free(c.Run, edges); return failure(c.Run, "KASH_DOUBLE_CONSUME", e.Span, "live operand lost its expanded argument slot") }
				value.List = slices.Append(c.Run, value.List, core.Value{Kind: core.Int, Int: int64(actual)})
			}
			edges = slices.Append(c.Run, edges, value)
		}
		host.ConfigureTopology(c.Run, &payload, kinds, edges)
		var buffers, overflows, itemLimits []core.Value
		configuredBuffer, lossy := false, false
		for i := range e.Items {
			budget := int64(0)
			if e.Items[i].Kind == expr.CommandMux { budget = e.Items[i].Int }
			if budget != 0 { configuredBuffer = true }
			buffers = slices.Append(c.Run, buffers, core.Value{Kind: core.Int, Int: budget})
			overflows = slices.Append(c.Run, overflows, core.Value{Kind: core.Int, Int: int64(e.Items[i].StreamOverflow)})
			itemLimits = slices.Append(c.Run, itemLimits, core.Value{Kind: core.Int, Int: int64(e.Items[i].BufferItems)})
			if e.Items[i].StreamOverflow != 0 { lossy = true }
		}
		if configuredBuffer { payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "buffers"), Value: core.NewList(c.Run, buffers)}) }
		if lossy {
			payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "overflows"), Value: core.NewList(c.Run, overflows)})
			payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "bufferItems"), Value: core.NewList(c.Run, itemLimits)})
		}
		slices.Free(c.Run, buffers)
		slices.Free(c.Run, overflows); slices.Free(c.Run, itemLimits)
		for i := range kinds { kinds[i].Free(c.Run) }; slices.Free(c.Run, kinds)
		for i := range edges { edges[i].Free(c.Run) }; slices.Free(c.Run, edges)
	}
	if state.Input != "" || state.Output != "" { host.ConfigureRedirections(c.Run, &payload, state.Input, state.Output, state.Append) }
	if e.Kind == expr.CommandGraph || e.Kind == expr.CommandTest || e.Kind == expr.CommandResult {
		payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "stream"), Value: core.Value{Kind: core.Bool, Bool: true}})
	}
	if e.AcceptExit { payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "acceptExit"), Value: core.Value{Kind: core.Bool, Bool: true}}) }
	if e.Topology || configured || len(c.Environment) != 0 || c.TimeoutMS != 0 || e.Kind == expr.CommandGraph || e.Kind == expr.CommandTest || e.Kind == expr.CommandResult || e.AcceptExit { host.ConfigureStages(c.Run, &payload, state.Setups) }
	if state.Async || e.Async {
		r := p.startProcess(e, c, state)
		payload.Free(c.Run)
		if r.Diagnostic.Code == "" { state.Value, state.Done = r.Value.Clone(state.Alloc), true }
		return r
	}
	if e.Kind == expr.CommandCapture { c.UnvalidatedRead() }
	state.ID = c.Submit(host.RequestProcess, payload)
	payload.Free(c.Run)
	if state.ID == 0 {
		return failure(c.Run, "HOST_FAIL", e.Span, "host request was not accepted")
	}
	return Result{Waiting: true}
}

// Consume the published artifact signature as well as demanding its producer.
// An ordering edge alone does not persist a generated input's content identity.
func demandProcessInput(c *Context, name string) bool {
	if name == "" || c.DirectHostRequests { return true }
	key := core.NewResourceKey(c.Run, core.ResourceFile, name)
	ready := c.Dependency(key)
	if ready { c.Value(key) }
	key.Free(c.Run)
	return ready
}

// Completed argument values are not reevaluated after a later argument waits.
// Retain their declarative effects alongside the cached argv progress.
func rememberArgumentEffects(state *captureState, c *Context, before int, beforePaths int) {
	for i := before; i < len(c.Effects); i++ { effect := c.Effects[i]; state.Effects = slices.Append(state.Alloc, state.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(state.Alloc, effect.Data)}) }
	for i := beforePaths; i < len(c.WritePaths); i++ { state.WritePaths = slices.Append(state.Alloc, state.WritePaths, owned(state.Alloc, c.WritePaths[i])) }
}

// Only a completed process failure selects the fallback. Argument evaluation,
// authorization, and waiting never reach this recovery boundary.
func (p *Program) captureRecovery(e *expr.Expr, c *Context, state *captureState, r Result) Result {
	if r.Diagnostic.Protected || len(e.Body) == 0 || (r.Diagnostic.Code != "RECIPE_FAIL" && r.Diagnostic.Code != "HOST_FAIL" && r.Diagnostic.Code != "TOOL_MISSING" && r.Diagnostic.Code != "RECIPE_TIMEOUT" && !strings.HasPrefix(r.Diagnostic.Code, "KASH_PREDICATE_") && r.Diagnostic.Code != "KASH_RECORD_LIMIT" && r.Diagnostic.Code != "KASH_BUFFER_LIMIT" && r.Diagnostic.Code != "KASH_FRAME_ENCODING") { return r }
	c.handledFinalizerFailure()
	state.Recovering = true
	r.Free(c.Run)
	return p.capture(e.Body[0], c)
}

// Scalar conversion is shared with argv, but redirections never splice lists.
func processScalar(a mem.Allocator, value core.Value) (string, bool) {
	if value.Kind != core.String && value.Kind != core.Pattern && value.Kind != core.Int && value.Kind != core.Float && value.Kind != core.Bool { return "", false }
	text, ok := Stringify(a, value)
	if !ok { return "", false }
	if strings.IndexByte(text, 0) >= 0 { mem.FreeString(a, text); return "", false }
	return text, true
}

func authorizeExecutable(c *Context, executable string) bool {
	// A lexical relative path grant must not accidentally authorize a different
	// executable discovered through PATH. Scoped grants require an explicit path.
	if strings.IndexByte(executable, '/') < 0 {
		unrestricted := false
		for i := range c.Grants {
			if c.Grants[i].Capability == Run && len(c.Grants[i].Names) == 0 {
				unrestricted = true
			}
		}
		if !unrestricted {
			return false
		}
	}
	return c.Allows(Run, executable)
}

func appendArgument(state *captureState, value core.Value) bool {
	if value.Kind != core.String && value.Kind != core.Pattern && value.Kind != core.Int && value.Kind != core.Float && value.Kind != core.Bool {
		return false
	}
	text, ok := Stringify(state.Alloc, value)
	if !ok {
		return false
	}
	if strings.IndexByte(text, 0) >= 0 {
		mem.FreeString(state.Alloc, text)
		return false
	}
	argument := core.Value{Kind: core.String, Text: text}
	if len(state.Argv) == 0 && value.ToolName != "" { argument.ToolName = owned(state.Alloc, value.ToolName) }
	state.Argv = slices.Append(state.Alloc, state.Argv, argument)
	return true
}

func (p *Program) commandWord(e *expr.Expr, c *Context, destination string) Result {
	if e.Bool {
		return p.evaluate(c.Engine, c.Scope, e.Parts[0].Expr, c)
	}
	var children []*expr.Expr
	for i := range e.Parts {
		if e.Parts[i].Expr != nil { children = slices.Append(c.Run, children, e.Parts[i].Expr) }
	}
	operands := p.argumentValues(c.Scope, children, c, e.Span)
	slices.Free(c.Run, children)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" { return operands.Result }
	defer freeValuesWithCallables(c.Run, operands.Values)
	b := strings.NewBuilder(c.Run)
	next := 0
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr == nil {
			b.WriteString(part.Text)
			continue
		}
		value := operands.Values[next]
		next++
		if value.Kind != core.String && value.Kind != core.Pattern && value.Kind != core.Int && value.Kind != core.Float && value.Kind != core.Bool {
			b.Free()
			return failure(c.Run, "EXPR_INVALID", part.Span, destination+" word requires a scalar fragment; got "+core.KindName(value.Kind)+"; list splicing is only valid in standalone unquoted argv substitutions")
		}
		text, ok := Stringify(c.Run, value)
		if !ok {
			b.Free()
			return failure(c.Run, "EXPR_INVALID", part.Span, "invalid command word")
		}
		b.WriteString(text)
		mem.FreeString(c.Run, text)
	}
	value := core.NewString(c.Run, b.String())
	b.Free()
	return Result{Value: value}
}

func captureValue(a mem.Allocator, value core.Value, span source.Span, acceptExit bool) Result {
	// A host may complete a successful capture as text directly, avoiding JSON
	// escaping and extra copies for large strings. Failures retain exit metadata.
	if value.Kind == core.String {
		if !utf8.Valid([]byte(value.Text)) {
			return failure(a, "CAPTURE_ENCODING", span, "command substitution output is not valid UTF-8")
		}
		return Result{Value: value.Clone(a)}
	}
	if value.Kind != core.Record {
		return failure(a, "HOST_FAIL", span, "invalid process completion")
	}
	var output core.Value
	status, signal, hasStatus := int64(0), int64(0), false
	for i := range value.Record {
		field := value.Record[i]
		if field.Key == "status" && field.Value.Kind == core.Int {
			status, hasStatus = field.Value.Int, true
		}
		if field.Key == "signal" {
			signal = field.Value.Int
		}
		if field.Key == "stdout" {
			output = field.Value
		}
		if field.Key == "stdoutTruncated" && field.Value.Bool {
			return failure(a, "CAPTURE_LIMIT", span, "command substitution exceeded capture limit")
		}
	}
	if !hasStatus {
		return failure(a, "HOST_FAIL", span, "process completion omitted status")
	}
	if !acceptExit && (status != 0 || signal != 0) {
		if signal != 0 && status == 0 {
			status = 128 + signal
		}
		var buffer [strconv.MaxIntBase10Len]byte
		r := failure(a, "RECIPE_FAIL", span, "command substitution exited unsuccessfully (status "+strconv.FormatInt(buffer[:], status, 10)+")")
		r.Diagnostic.Cause = diagnostic.Cause{Kind: owned(a, "process"), Status: int(status), HasStatus: true, Signal: int(signal), HasSignal: signal != 0}
		return r
	}
	if output.Kind == core.String {
		return captureValue(a, output, span, acceptExit)
	}
	if output.Kind != core.Bytes {
		return failure(a, "HOST_FAIL", span, "process completion omitted stdout")
	}
	if !utf8.Valid(output.Bytes) {
		return failure(a, "CAPTURE_ENCODING", span, "command substitution output is not valid UTF-8")
	}
	return Result{Value: core.NewString(a, string(output.Bytes))}
}
