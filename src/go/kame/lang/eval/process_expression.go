package eval

import (
	"kame/core"
	"kame/host"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Lower syntax, not values: pipe must validate its complete shape before any
// stage is evaluated or launched. The shared executor caches evaluated argv.
func (p *Program) processExpression(e *expr.Expr, c *Context) Result {
	graph := mem.Alloc[expr.Expr](c.Run)
	graph.Kind, graph.Span = expr.CommandResult, e.Span
	defer expr.Free(c.Run, graph)
	if e.Items[0].Text == "run" {
		graph.Items = slices.Append(c.Run, graph.Items, mem.Alloc[expr.Expr](c.Run))
		r := lowerProcessStage(c, graph.Items[0], e, false)
		if r.Diagnostic.Code != "" { return r }
	} else {
		if len(e.Items) < 3 { return failure(c.Run, "EXPR_INVALID", e.Span, "pipe requires at least two run stages") }
		for i := 1; i < len(e.Items); i++ {
			stage := mem.Alloc[expr.Expr](c.Run)
			graph.Items = slices.Append(c.Run, graph.Items, stage)
			r := lowerProcessStage(c, stage, e.Items[i], true)
			if r.Diagnostic.Code != "" { return r }
		}
	}
	return p.capture(graph, c)
}

func lowerProcessStage(c *Context, stage *expr.Expr, e *expr.Expr, pipeline bool) Result {
	stage.Kind, stage.Span = expr.CommandStage, e.Span
	if e.Kind != expr.Application || len(e.Items) == 0 || e.Items[0].Kind != expr.Name || e.Items[0].Text != "run" {
		return failure(c.Run, "EXPR_INVALID", e.Span, "pipe stages must be run applications")
	}
	i := 1
	for i < len(e.Items) && e.Items[i].Kind == expr.Symbol {
		key := e.Items[i]
		if !processSetupKey(key.Text) || (pipeline && key.Text == "async") {
			return failure(c.Run, "EXPR_INVALID", key.Span, "invalid process setup key")
		}
		for j := range stage.Items { if stage.Items[j].Text == key.Text { return failure(c.Run, "EXPR_INVALID", key.Span, "duplicate process setup key") } }
		if i+1 == len(e.Items) { return failure(c.Run, "EXPR_INVALID", key.Span, "process setup requires a value") }
		setup := mem.Alloc[expr.Expr](c.Run)
		setup.Kind, setup.Text, setup.Span = expr.CommandSetup, key.Text, key.Span
		setup.Items = slices.Append(c.Run, setup.Items, processExpressionWord(c.Run, e.Items[i+1]))
		stage.Items = slices.Append(c.Run, stage.Items, setup)
		i += 2
	}
	if i == len(e.Items) { return failure(c.Run, "EXPR_INVALID", e.Span, "run requires an executable") }
	for ; i < len(e.Items); i++ { stage.Items = slices.Append(c.Run, stage.Items, processExpressionWord(c.Run, e.Items[i])) }
	return Result{}
}

func processExpressionWord(a mem.Allocator, e *expr.Expr) *expr.Expr {
	word := mem.Alloc[expr.Expr](a)
	word.Kind, word.Bool, word.Span = expr.CommandWord, true, e.Span
	word.Parts = slices.Append(a, word.Parts, expr.StringPart{Expr: expr.Clone(a, e), Span: e.Span})
	return word
}

func processSetupKey(key string) bool {
	if key == "" { return false }
	for i := 0; i < len(key); i++ {
		b := key[i]
		if b != '_' && !(b >= 'a' && b <= 'z') && !(b >= 'A' && b <= 'Z') && !(i > 0 && b >= '0' && b <= '9') { return false }
	}
	return true
}

// Results retain completion metadata, never implicit stdout/stderr buffers.
// A record is deliberately not an argv scalar; capture remains explicit.
func processResult(a mem.Allocator, completion core.Value, span source.Span) Result {
	if completion.Kind != core.Record { return failure(a, "HOST_FAIL", span, "process completion omitted its result") }
	status, signal := host.PayloadInt(completion, "status"), host.PayloadInt(completion, "signal")
	if status == 0 && signal != 0 { status = 128 + signal }
	var stages []core.Value
	completed := host.PayloadStages(completion)
	for i := range completed {
		stage := completed[i]
		parts := []core.RecordField{
			{Key: "status", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "status")}},
			{Key: "signal", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "signal")}},
			{Key: "outcome", Value: core.Value{Kind: core.Int, Int: host.PayloadInt(stage, "outcome")}},
		}
		stages = slices.Append(a, stages, core.NewRecord(a, parts))
	}
	fields := []core.RecordField{
		{Key: "status", Value: core.Value{Kind: core.Int, Int: status}},
		{Key: "signal", Value: core.Value{Kind: core.Int, Int: signal}},
		{Key: "stages", Value: core.NewList(a, stages)},
		{Key: "stdoutCaptured", Value: core.Value{Kind: core.Bool}},
		{Key: "stderrCaptured", Value: core.Value{Kind: core.Bool}},
	}
	value := core.NewRecord(a, fields)
	fields[2].Value.Free(a)
	for i := range stages { stages[i].Free(a) }
	slices.Free(a, stages)
	return Result{Value: value}
}
