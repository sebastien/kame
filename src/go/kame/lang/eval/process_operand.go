package eval

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Argument ports initially name authored item slots, not expanded argv offsets.
// Evaluation resolves them after ordinary list splicing, without treating a live
// input as text or executing an anonymous producer as a separate invocation.
func lowerStreamOperandStage(c *Context, e *expr.Expr, flat *expr.Expr) graphPorts {
	index := len(flat.Items)
	node := expr.Clone(c.Run, e)
	flat.Items = slices.Append(c.Run, flat.Items, node)
	ports := graphPorts{}
	ports.Inputs = slices.Append(c.Run, ports.Inputs, index)
	ports.Outputs = slices.Append(c.Run, ports.Outputs, index)
	for i := range e.Items {
		if flat.Text != "" { break }
		operand := e.Items[i]
		if operand.Kind != expr.CommandOperand { continue }
		slot := i+1
		if len(operand.Body) == 0 {
			if operand.Text == "" {
				if node.Int != 0 { flat.Text, flat.Span = "KASH_DOUBLE_CONSUME", operand.Span } else { node.Int = int64(slot) }
			} else {
				reader := mem.Alloc[expr.Expr](c.Run)
				reader.Kind, reader.Text, reader.Span = expr.CommandReader, operand.Text, operand.Span
				from := len(flat.Items)
				flat.Items = slices.Append(c.Run, flat.Items, reader)
				flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: from, To: index, Argument: slot})
			}
			continue
		}
		child := operand.Body[0]
		if child.Async || child.AcceptExit || len(child.Body) != 0 || child.Exit != "" { flat.Text, flat.Span = "KASH_REJOIN_OUT_OF_SCOPE", child.Span; continue }
		start := len(flat.Items)
		production := lowerProcessGraph(c, child, flat)
		// Exactly one unredirected terminal supplies this operand. Explicit
		// side sinks remain owned, but never leak into presentation or capture.
		terminal, count := -1, 0
		for j := start; j < len(flat.Items); j++ {
			connected, redirected := false, false
			for k := range flat.Edges { if flat.Edges[k].From == j { connected = true } }
			for k := range flat.Items[j].Items { word := flat.Items[j].Items[k]; if word.Kind == expr.CommandRedirection && word.Text != "<" { redirected = true } }
			if !connected && !redirected && flat.Items[j].Kind != expr.CommandWriter { terminal, count = j, count+1 }
		}
		if flat.Text == "" {
			if count != 1 { flat.Text, flat.Span = "KASH_AMBIGUOUS_CAPTURE", operand.Span } else { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: terminal, To: index, Argument: slot}) }
		}
		slices.Free(c.Run, production.Inputs); slices.Free(c.Run, production.Outputs)
	}
	return ports
}
