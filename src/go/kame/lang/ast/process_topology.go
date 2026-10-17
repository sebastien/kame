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
			e.EndObject()
		}
		e.EndArray()
	}
	e.EndObject()
}
