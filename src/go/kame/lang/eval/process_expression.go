package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"slices"
	"strings"
)

// Lower syntax, not values: pipe must validate its complete shape before any
// stage is evaluated or launched. The shared executor caches evaluated argv.
func (p *Program) processExpression(e *expr.Expr, c *Context) Result {
	graph := mem.Alloc[expr.Expr](c.Run)
	graph.Kind, graph.Span = expr.CommandResult, e.Span
	defer expr.Free(c.Run, graph)
	if e.Items[0].Text == "run!" || e.Items[0].Text == "capture!" {
		graph.TypedCapture = e.Items[0].Text == "capture!"
		if graph.TypedCapture && len(e.Items) >= 3 {
			// The policy is part of the capture invocation, not a stage setup.
			graph.CapturePolicy = processExpressionWord(c.Run, e.Items[1])
		}
		graph.Items = append(graph.Items, mem.Alloc[expr.Expr](c.Run))
		r := lowerProcessStage(c, graph.Items[0], e, false)
		if r.Diagnostic.Code != "" {
			return r
		}
	} else {
		if len(e.Items) < 3 {
			return failure(c.Run, "EXPR_INVALID", e.Span, "pipe! requires at least two run! stages")
		}
		for i := 1; i < len(e.Items); i++ {
			stage := mem.Alloc[expr.Expr](c.Run)
			graph.Items = append(graph.Items, stage)
			r := lowerProcessStage(c, stage, e.Items[i], true)
			if r.Diagnostic.Code != "" {
				return r
			}
		}
	}
	if graph.TypedCapture {
		return p.withCaptureUnit(graph, c, p.capture)
	}
	return p.capture(graph, c)
}

func lowerProcessStage(c *Context, stage *expr.Expr, e *expr.Expr, pipeline bool) Result {
	stage.Kind, stage.Span = expr.CommandStage, e.Span
	e = expr.Ungroup(e)
	if e.Kind != expr.Application || len(e.Items) == 0 || e.Items[0].Kind != expr.Name || (e.Items[0].Text != "run!" && (pipeline || e.Items[0].Text != "capture!")) {
		return failure(c.Run, "EXPR_INVALID", e.Span, "pipe! stages must be run! applications")
	}
	i := 1
	capture := e.Items[0].Text == "capture!"
	if capture {
		if len(e.Items) < 3 {
			return failure(c.Run, "EXPR_INVALID", e.Span, "capture! requires a policy and executable")
		}
		i++
	}
	for i < len(e.Items) && e.Items[i].Kind == expr.Symbol {
		key := e.Items[i]
		kind, valid := expr.ClassifySetupKey(key.Text)
		if !valid || ((pipeline || capture) && kind == expr.SetupAsync) {
			return failure(c.Run, "EXPR_INVALID", key.Span, "invalid process setup key")
		}
		for j := range stage.Items {
			if stage.Items[j].Text == key.Text {
				return failure(c.Run, "EXPR_INVALID", key.Span, "duplicate process setup key")
			}
		}
		if i+1 == len(e.Items) {
			return failure(c.Run, "EXPR_INVALID", key.Span, "process setup requires a value")
		}
		setup := mem.Alloc[expr.Expr](c.Run)
		setup.Kind, setup.Text, setup.Span = expr.CommandSetup, key.Text, key.Span
		setup.Items = append(setup.Items, processExpressionWord(c.Run, e.Items[i+1]))
		stage.Items = append(stage.Items, setup)
		i += 2
	}
	if i == len(e.Items) {
		return failure(c.Run, "EXPR_INVALID", e.Span, "run! requires an executable")
	}
	for ; i < len(e.Items); i++ {
		stage.Items = append(stage.Items, processExpressionWord(c.Run, e.Items[i]))
	}
	return Result{}
}

func processExpressionWord(a mem.Allocator, e *expr.Expr) *expr.Expr {
	word := mem.Alloc[expr.Expr](a)
	word.Kind, word.Bool, word.Span = expr.CommandWord, true, e.Span
	word.Parts = append(word.Parts, expr.StringPart{Expr: expr.Clone(a, e), Span: e.Span})
	return word
}

func validProcessEnvironment(value core.Value) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		field := value.Record[i]
		if !expr.ValidEnvironmentName(field.Key) || field.Value.Kind != core.String || strings.IndexByte(field.Value.Text, 0) >= 0 {
			return false
		}
	}
	return true
}

// IsProcessResult recognizes the completion record built by processResult,
// whose output has already been delivered as the command ran.
func IsProcessResult(value core.Value) bool {
	if value.Kind != core.Record || len(value.Record) != 7 {
		return false
	}
	keys := [...]string{"status", "signal", "stages", "stdoutCaptured", "stderrCaptured", "effectiveStatus", "effectiveSignal"}
	for i := range keys {
		if value.Record[i].Key != keys[i] {
			return false
		}
	}
	return true
}

// Results retain completion metadata, never implicit stdout/stderr buffers.
// A record is deliberately not an argv scalar; capture remains explicit.
func processResult(a mem.Allocator, completion core.Value, span source.Span) Result {
	if completion.Kind != core.Record {
		return failure(a, "HOST_FAIL", span, "process completion omitted its result")
	}
	status, signal := host.PayloadInt(completion, "status"), host.PayloadInt(completion, "signal")
	if status == 0 && signal != 0 {
		status = 128 + signal
	}
	var stages []core.Value
	completed := host.PayloadStages(completion)
	for i := range completed {
		stage := completed[i]
		parts := []core.RecordField{
			{Key: "status", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "status")}},
			{Key: "signal", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "signal")}},
			{Key: "outcome", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "outcome")}},
		}
		stages = append(stages, core.NewRecord(a, parts))
		v := &stages[len(stages)-1]
		v.Record = append(v.Record, core.RecordField{Key: owned(a, "effectiveStatus"), Value: core.Value{Kind: core.Int, Int: host.EffectiveStatus(stage)}})
		for _, field := range stage.Record {
			if field.Key == "state" || field.Key == "error" {
				v.Record = append(v.Record, core.RecordField{Key: owned(a, field.Key), Value: field.Value.Clone(a)})
			}
		}
	}
	fields := []core.RecordField{
		{Key: "status", Value: core.Value{Kind: core.Int, Int: status}},
		{Key: "signal", Value: core.Value{Kind: core.Int, Int: signal}},
		{Key: "stages", Value: core.NewList(a, stages)},
		{Key: "stdoutCaptured", Value: core.Value{Kind: core.Bool}},
		{Key: "stderrCaptured", Value: core.Value{Kind: core.Bool}},
	}
	value := core.NewRecord(a, fields)
	value.Record = append(value.Record, core.RecordField{Key: owned(a, "effectiveStatus"), Value: core.Value{Kind: core.Int, Int: host.EffectiveStatus(completion)}})
	value.Record = append(value.Record, core.RecordField{Key: owned(a, "effectiveSignal"), Value: core.Value{Kind: core.Int, Int: host.EffectiveSignal(completion)}})
	fields[2].Value.Free(a)
	for i := range stages {
		stages[i].Free(a)
	}
	mem.FreeSlice(nil, stages)
	return Result{Value: value}
}

// captureOutcome exposes process status without turning an ordinary nonzero
// exit into an evaluator failure. The stdout value has passed capture checks.
func captureOutcome(c *Context, action *expr.Expr, completion, stdout core.Value) Result {
	a := c.Run
	span := action.Span
	if completion.Kind == core.String {
		completion = core.Value{Kind: core.Record}
	}
	r := processResult(a, completion, span)
	if r.Diagnostic.Code != "" {
		return r
	}
	state, problem := "completed", core.Value{}
	effectiveStatus, effectiveSignal := host.EffectiveStatus(completion), host.EffectiveSignal(completion)
	if action.AcceptExit && effectiveSignal == 0 {
		effectiveStatus = 0
	}
	if effectiveStatus != 0 || effectiveSignal != 0 {
		state = "failed"
		fields := []core.RecordField{
			{Key: "code", Value: core.Value{Kind: core.String, Text: "RECIPE_FAIL"}},
			{Key: "message", Value: core.Value{Kind: core.String, Text: "process exited unsuccessfully"}},
			{Key: "source", Value: core.Value{Kind: core.String, Text: c.Source}},
			{Key: "start", Value: core.Value{Kind: core.Int, Int: int64(span.Start)}},
			{Key: "end", Value: core.Value{Kind: core.Int, Int: int64(span.End)}},
		}
		problem = core.NewRecord(a, fields)
	}
	for i := range r.Value.Record {
		switch r.Value.Record[i].Key {
		case "stdoutCaptured":
			r.Value.Record[i].Value.Bool = true
		case "effectiveStatus":
			r.Value.Record[i].Value.Int = effectiveStatus
		}
	}
	status, signal := host.PayloadInt(completion, "status"), host.PayloadInt(completion, "signal")
	stages := host.PayloadStages(r.Value)
	for i, stage := range host.PayloadStages(completion) {
		for _, field := range stage.Record {
			if field.Key == "value" {
				stages[i].Record = append(stages[i].Record, core.RecordField{Key: owned(a, "value"), Value: borrowValue(a, field.Value)})
				break
			}
		}
	}
	r.Value.Record = append(r.Value.Record,
		core.RecordField{Key: owned(a, "state"), Value: core.NewSymbol(a, state)},
		core.RecordField{Key: owned(a, "ok"), Value: core.Value{Kind: core.Bool, Bool: state == "completed" && status == 0 && signal == 0}},
		core.RecordField{Key: owned(a, "value"), Value: borrowValue(a, stdout)},
		core.RecordField{Key: owned(a, "error"), Value: problem},
	)
	if !action.Topology {
		r.Value.Record = append(r.Value.Record, core.RecordField{Key: owned(a, "graph")})
	}
	return r
}

func captureGraphExit(c *Context, action *expr.Expr, completion core.Value, argv []core.Value, exited Result) Result {
	// A sibling can be killed while the capture unwinds. The requested exit
	// controls the capture status while each stage keeps its own outcome.
	completion.Record = slices.Clone(completion.Record)
	for i := range completion.Record {
		switch completion.Record[i].Key {
		case "status", "effectiveStatus":
			completion.Record[i].Value = core.Value{Kind: core.Int, Int: int64(exited.Diagnostic.Exit.Status)}
		case "signal", "effectiveSignal":
			completion.Record[i].Value = core.Value{Kind: core.Int}
		}
	}
	outcome := captureExitOutcome(c, action, completion, exited.Value, exited.Diagnostic)
	if action.Topology && outcome.Diagnostic.Code == "" {
		outcome = processGraphResult(c.Run, outcome, action, completion, argv)
	}
	return outcome
}

func captureExitOutcome(c *Context, action *expr.Expr, completion, stdout core.Value, d diagnostic.Diagnostic) Result {
	if d.ExitOnly() {
		return captureOutcome(c, action, completion, stdout)
	}
	failed := *action
	failed.AcceptExit = false
	return setOutcomeError(c, captureOutcome(c, &failed, completion, stdout), d)
}

func processFailureCanBeInspected(d diagnostic.Diagnostic) bool {
	if d.Protected || d.Cancelled || d.Exit.Requested {
		return false
	}
	return eligibleCommandExit(d) || d.Code == "HOST_FAIL" || d.Code == "TOOL_MISSING" || d.Code == "RECIPE_TIMEOUT"
}

func processFailureOutcome(c *Context, action *expr.Expr, d diagnostic.Diagnostic, stdout core.Value) Result {
	if !processFailureCanBeInspected(d) {
		return Result{Diagnostic: d.Clone(c.Run)}
	}
	status := int64(1)
	if d.Cause.HasStatus {
		status = int64(d.Cause.Status)
	}
	if d.Source == "" {
		d.Source = c.Source
	}
	completion := core.Value{Kind: core.Record, Record: []core.RecordField{
		{Key: "status", Value: core.Value{Kind: core.Int, Int: status}},
		{Key: "signal", Value: core.Value{Kind: core.Int, Int: int64(d.Cause.Signal)}},
	}}
	failed := *action
	failed.AcceptExit, failed.Topology = false, false
	return setOutcomeError(c, captureOutcome(c, &failed, completion, stdout), d)
}

func setOutcomeError(c *Context, r Result, d diagnostic.Diagnostic) Result {
	for i := range r.Value.Record {
		if field := &r.Value.Record[i]; field.Key == "error" {
			field.Value.Free(c.Run)
			field.Value = outcomeErrorRecord(c.Run, d)
			break
		}
	}
	return r
}
