package eval

import (
	"kame/core"
	"kame/host"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func processGraphResult(a mem.Allocator, result Result, graph *expr.Expr, completion core.Value, argv []core.Value) Result {
	metrics := host.PayloadList(completion, "edgeMetrics")
	valid := len(metrics) == 0 || len(metrics) == len(graph.Edges)
	for i := range metrics {
		row := metrics[i]
		if row.Kind != core.List || len(row.List) != 7 {
			valid = false
			continue
		}
		for j := range row.List {
			if row.List[j].Kind != core.Int || row.List[j].Int < -1 || row.List[j].Int > 9007199254740991 {
				valid = false
			}
		}
		if row.List[0].Int < 0 || row.List[0].Int > 3 || row.List[1].Int < 0 || row.List[4].Int < 0 || row.List[5].Int < 0 || row.List[6].Int < 0 || row.List[6].Int > 1 {
			valid = false
		}
		if row.List[0].Int != 0 && i < len(graph.Edges) {
			from := graph.Edges[i].From
			if graph.StreamKinds[from] == 0 && (row.List[2].Int != -1 || row.List[3].Int != -1) {
				valid = false
			}
			if graph.StreamKinds[from] == 1 && (row.List[2].Int < 0 || row.List[3].Int < 0) {
				valid = false
			}
			if row.List[0].Int == 3 {
				node := graph.Items[from]
				budget, count := node.Int, node.BufferItems
				if budget == 0 {
					budget = 65536
				}
				if count == 0 {
					count = 64
					if node.StreamOverflow == 3 {
						count = 1
					}
				}
				if node.StreamOverflow == 0 || row.List[4].Int > budget || row.List[5].Int > int64(count) {
					valid = false
				}
			}
		}
	}
	if !valid {
		result.Free(a)
		return failure(a, "HOST_FAIL", graph.Span, "host returned invalid process edge telemetry")
	}
	var nodes, edges []core.Value
	stages := host.PayloadStages(completion)
	for i := range graph.Items {
		node := graph.Items[i]
		kind, program, state := node.Text, "", "unknown"
		status, signal, outcome := int64(0), int64(0), int64(0)
		if node.Kind == expr.CommandStage {
			kind = "process"
			if i < len(argv) && len(argv[i].List) != 0 {
				program = argv[i].List[0].Text
			}
		}
		if node.Each {
			kind = "each"
		}
		known := 0
		if i < len(stages) {
			for j := range stages[i].Record {
				field := stages[i].Record[j]
				if (field.Key == "status" || field.Key == "signal" || field.Key == "outcome") && field.Value.Kind == core.Int {
					known++
				}
			}
		}
		if known == 3 {
			status, signal, outcome = host.PayloadInt(stages[i], "status"), host.PayloadInt(stages[i], "signal"), host.PayloadInt(stages[i], "outcome")
			state = "completed"
			if status != 0 || signal != 0 || outcome != 0 {
				state = "failed"
			}
			if outcome == 1 {
				state = "timed-out"
			}
			if outcome == 2 {
				state = "cancelled"
			}
		}
		fields := []core.RecordField{
			{Key: "id", Value: core.Value{Kind: core.Int, Int: int64(i)}},
			{Key: "kind", Value: core.NewString(a, kind)},
			{Key: "program", Value: core.NewString(a, program)},
			{Key: "state", Value: core.NewString(a, state)},
			{Key: "status", Value: core.Value{Kind: core.Int, Int: status}},
			{Key: "signal", Value: core.Value{Kind: core.Int, Int: signal}},
			{Key: "outcome", Value: core.Value{Kind: core.Int, Int: outcome}},
		}
		nodes = slices.Append(a, nodes, core.NewRecord(a, fields))
		for j := range fields {
			fields[j].Value.Free(a)
		}
	}
	for i := range graph.Edges {
		edge := graph.Edges[i]
		measurement := "unavailable"
		bytes, items, dropped, highBytes, highItems := core.Value{Kind: core.Nil}, core.Value{Kind: core.Nil}, core.Value{Kind: core.Nil}, core.Value{Kind: core.Nil}, core.Value{Kind: core.Nil}
		saturated := false
		if i < len(metrics) && metrics[i].List[0].Int != 0 {
			row := metrics[i].List
			measurement = "completed-write"
			if row[0].Int == 2 {
				measurement = "read"
			}
			bytes, saturated = row[1], row[6].Int != 0
			if row[2].Int >= 0 {
				items = row[2]
			}
			if row[3].Int >= 0 {
				dropped = row[3]
			}
			if row[0].Int == 3 {
				highBytes, highItems = row[4], row[5]
			}
		}
		kind := "bytes"
		if graph.StreamKinds[edge.From] == 1 {
			kind = "lines"
		}
		fields := []core.RecordField{
			{Key: "from", Value: core.Value{Kind: core.Int, Int: int64(edge.From)}},
			{Key: "to", Value: core.Value{Kind: core.Int, Int: int64(edge.To)}},
			{Key: "argumentItem", Value: core.Value{Kind: core.Int, Int: int64(edge.Argument)}},
			{Key: "kind", Value: core.NewString(a, kind)},
			{Key: "measurement", Value: core.NewString(a, measurement)},
			{Key: "bytes", Value: bytes}, {Key: "items", Value: items}, {Key: "droppedItems", Value: dropped},
			{Key: "queueHighWaterBytes", Value: highBytes}, {Key: "queueHighWaterItems", Value: highItems},
			{Key: "saturated", Value: core.Value{Kind: core.Bool, Bool: saturated}},
		}
		edges = slices.Append(a, edges, core.NewRecord(a, fields))
		for j := range fields {
			fields[j].Value.Free(a)
		}
	}
	fields := []core.RecordField{{Key: "nodes", Value: core.NewList(a, nodes)}, {Key: "edges", Value: core.NewList(a, edges)}, {Key: "terminal", Value: core.Value{Kind: core.Bool, Bool: true}}, {Key: "failure", Value: core.NewString(a, expr.StreamFailureName(graph.StreamFailure))}}
	value := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	for i := range nodes {
		nodes[i].Free(a)
	}
	slices.Free(a, nodes)
	for i := range edges {
		edges[i].Free(a)
	}
	slices.Free(a, edges)
	result.Value.Record = slices.Append(a, result.Value.Record, core.RecordField{Key: owned(a, "graph"), Value: value})
	return result
}
