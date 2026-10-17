package ast

import (
	"kame/lang/eval"
	"kame/lang/expr"
	"solod.dev/so/mem"
)

// Additive parse inspection, using exactly the runtime's static graph lowering.
// Invalid topology stays inspectable without demanding any embedded expression.
func (e *astEncoder) processTopology(value *expr.Expr) {
	flat := mem.Alloc[expr.Expr](mem.System)
	defer expr.Free(mem.System, flat)
	r := eval.InspectProcessTopology(mem.System, value, flat)
	defer r.Free(mem.System)
	e.Str("topology"); e.BeginObject()
	if len(value.Channels) != 0 {
		e.Str("channels"); e.BeginArray()
		for i := range value.Channels { e.Str(value.Channels[i]) }
		e.EndArray()
	}
	if r.Diagnostic.Code != "" {
		e.Str("diagnostic"); e.Str(r.Diagnostic.Code)
		e.Str("span"); e.span(flat.Span)
	} else {
		e.Str("nodes"); e.BeginArray()
		for i := range flat.Items {
			node := flat.Items[i]
			e.BeginObject()
			e.Str("id"); e.Int(int64(i))
			e.Str("kind"); if node.Kind == expr.CommandStage { e.Str("process") } else { e.Str(node.Text) }
			e.Str("span"); e.span(node.Span)
			e.Str("outputKind"); if flat.StreamKinds[i] == 1 { e.Str("lines") } else { e.Str("bytes") }
			if node.Kind == expr.CommandMux {
				inputs, outputs := 0, 0
				for j := range flat.Edges { if flat.Edges[j].To == i { inputs++ }; if flat.Edges[j].From == i { outputs++ } }
				e.Str("inputCount"); e.Int(int64(inputs))
				e.Str("outputCount"); e.Int(int64(outputs))
				budget := node.Int
				if budget == 0 { budget = 16384; if flat.StreamKinds[i] == 1 || node.Text == "encode" { budget = 65536 } }
				e.Str("bufferBytes"); e.Int(budget)
				e.Str("bufferScope"); e.Str("per-input")
				e.Str("overflow"); e.Str("block")
				e.Str("failure"); e.Str("fail-fast")
				if node.Text == "balance" { e.Str("strategy"); e.Str("round-robin") }
				if node.Text == "merge" { e.Str("ordering"); e.Str("per-input"); e.Str("strategy"); e.Str("round-robin-ready") }
			}
			if node.Kind == expr.CommandMux && node.Text == "filter" { e.Str("predicateSpan"); e.span(node.Items[0].Span) }
			e.EndObject()
		}
		e.EndArray()
		e.Str("edges"); e.BeginArray()
		for i := range flat.Edges {
			edge := flat.Edges[i]
			e.BeginObject()
			e.Str("from"); e.Int(int64(edge.From))
			e.Str("to"); e.Int(int64(edge.To))
			if edge.Argument != 0 { e.Str("argumentItem"); e.Int(int64(edge.Argument)) }
			e.EndObject()
		}
		e.EndArray()
	}
	e.EndObject()
}
