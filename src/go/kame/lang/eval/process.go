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
	Value  core.Value
}

func freeCaptureState(a mem.Allocator, value any) {
	s := value.(*captureState)
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
	mem.Free(a, s)
}

func (p *Program) capture(e *expr.Expr, c *Context) Result {
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
	if state.Stage == len(e.Items) && state.Input != "" && !c.DirectHostRequests {
		key := core.NewResourceKey(c.Run, core.ResourceFile, state.Input)
		ready := c.Dependency(key)
		key.Free(c.Run)
		if !ready { return Result{Waiting: true} }
	}
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
			return r
		}
		if !completion.HasValue {
			return failure(c.Run, "HOST_FAIL", e.Span, "process completion omitted its result")
		}
		if e.Kind == expr.CommandGraph {
			// A statement streams bytes; only status is interpreted, never stdout.
			for i := range completion.Value.Record {
				if completion.Value.Record[i].Key == "stdout" { completion.Value.Record[i].Value.Free(c.Run); completion.Value.Record[i].Value = core.NewString(c.Run, "") }
				if completion.Value.Record[i].Key == "stdoutTruncated" { completion.Value.Record[i].Value.Bool = false }
			}
		}
		r := captureValue(c.Run, completion.Value, e.Span)
		if r.Diagnostic.Code == "RECIPE_FAIL" {
			stages := host.PayloadStages(completion.Value)
			for i := range stages {
				if i >= len(e.Items) {
					break
				}
				for j := range stages[i].Record {
					field := stages[i].Record[j]
					if (field.Key == "status" || field.Key == "signal") && field.Value.Int != 0 {
						r.Diagnostic.Span.Start, r.Diagnostic.Span.End = e.Items[i].Span.Start, e.Items[i].Span.End
					}
				}
			}
		}
		completion.Value.Free(c.Run)
		if r.Diagnostic.Code == "" {
			state.Value, state.Done = r.Value.Clone(state.Alloc), true
		}
		return r
	}
	for state.Stage < len(e.Items) {
		stage := e.Items[state.Stage]
		for state.Index < len(stage.Items) {
			word := stage.Items[state.Index]
			if word.Kind == expr.CommandSetup {
				r := p.commandWord(word.Items[0], c)
				if r.Waiting || r.Diagnostic.Code != "" { return r }
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
				r := p.commandWord(word.Items[0], c)
				if r.Waiting || r.Diagnostic.Code != "" { return r }
				text, ok := processScalar(c.Run, r.Value)
				r.Free(c.Run)
				if !ok || text == "" { mem.FreeString(c.Run, text); return failure(c.Run, "EXPR_INVALID", word.Span, "redirection requires one nonempty scalar path without NUL bytes") }
				if word.Text == "<" { state.Input = owned(state.Alloc, text) } else { state.Output, state.Append = owned(state.Alloc, text), word.Text == ">>" }
				mem.FreeString(c.Run, text)
				state.Index++
				continue
			}
			r := p.commandWord(word, c)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			if r.Value.Kind == core.List {
				if len(state.Argv) == 0 {
					r.Free(c.Run)
					return failure(c.Run, "EXPR_INVALID", word.Span, "executable cannot be a list")
				}
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
		if strings.IndexByte(state.Argv[0].Text, '/') >= 0 {
			resolved := canonicalPath(state.Alloc, cwd, state.Argv[0].Text)
			state.Argv[0].Free(state.Alloc)
			state.Argv[0] = core.Value{Kind: core.String, Text: resolved}
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
		state.Setups = slices.Append(state.Alloc, state.Setups, host.StageSetupPayload(state.Alloc, state.Cwd, state.Timeout, state.Environment))
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
	if state.Input != "" && !c.DirectHostRequests {
		key := core.NewResourceKey(c.Run, core.ResourceFile, state.Input)
		ready := c.Dependency(key)
		key.Free(c.Run)
		if !ready { return Result{Waiting: true} }
	}
	configured := false
	for i := range e.Items { for j := range e.Items[i].Items { if e.Items[i].Items[j].Kind == expr.CommandSetup { configured = true } } }
	payload := host.ArgvPayload(c.Run, state.Stages[0].List)
	if configured || len(state.Stages) > 1 || state.Input != "" || state.Output != "" || e.Kind == expr.CommandGraph {
		payload.Free(c.Run)
		payload = host.PipelinePayload(c.Run, state.Stages)
	}
	if state.Input != "" || state.Output != "" { host.ConfigureRedirections(c.Run, &payload, state.Input, state.Output, state.Append) }
	if configured && e.Kind != expr.CommandGraph { host.ConfigureStages(c.Run, &payload, state.Setups) }
	if e.Kind == expr.CommandGraph {
		payload.Record = slices.Append(c.Run, payload.Record, core.RecordField{Key: owned(c.Run, "stream"), Value: core.Value{Kind: core.Bool, Bool: true}})
		host.ConfigureStages(c.Run, &payload, state.Setups)
	}
	state.ID = c.Submit(host.RequestProcess, payload)
	payload.Free(c.Run)
	if state.ID == 0 {
		return failure(c.Run, "HOST_FAIL", e.Span, "host request was not accepted")
	}
	return Result{Waiting: true}
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
	state.Argv = slices.Append(state.Alloc, state.Argv, core.Value{Kind: core.String, Text: text})
	return true
}

func (p *Program) commandWord(e *expr.Expr, c *Context) Result {
	if e.Bool {
		return p.evaluate(c.Engine, c.Scope, e.Parts[0].Expr, c)
	}
	b := strings.NewBuilder(c.Run)
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr == nil {
			b.WriteString(part.Text)
			continue
		}
		r := p.evaluate(c.Engine, c.Scope, part.Expr, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			b.Free()
			return r
		}
		if r.Value.Kind != core.String && r.Value.Kind != core.Pattern && r.Value.Kind != core.Int && r.Value.Kind != core.Float && r.Value.Kind != core.Bool {
			r.Free(c.Run)
			b.Free()
			return failure(c.Run, "EXPR_INVALID", part.Span, "quoted or mixed command word requires a scalar")
		}
		text, ok := Stringify(c.Run, r.Value)
		r.Free(c.Run)
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

func captureValue(a mem.Allocator, value core.Value, span source.Span) Result {
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
	if status != 0 || signal != 0 {
		if signal != 0 && status == 0 {
			status = 128 + signal
		}
		var buffer [strconv.MaxIntBase10Len]byte
		r := failure(a, "RECIPE_FAIL", span, "command substitution exited unsuccessfully (status "+strconv.FormatInt(buffer[:], status, 10)+")")
		r.Diagnostic.Cause = diagnostic.Cause{Kind: owned(a, "process"), Status: int(status), HasStatus: true, Signal: int(signal), HasSignal: signal != 0}
		return r
	}
	if output.Kind == core.String {
		return captureValue(a, output, span)
	}
	if output.Kind != core.Bytes {
		return failure(a, "HOST_FAIL", span, "process completion omitted stdout")
	}
	if !utf8.Valid(output.Bytes) {
		return failure(a, "CAPTURE_ENCODING", span, "command substitution output is not valid UTF-8")
	}
	return Result{Value: core.NewString(a, string(output.Bytes))}
}
