package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func snapshotTopology(a mem.Allocator, payload core.Value) core.Value {
	if len(host.PayloadList(payload, "kinds")) == 0 { return core.Value{Kind: core.Nil} }
	var programs []core.Value
	stages := host.PayloadStages(payload)
	for i := range stages {
		name := ""; if len(stages[i].List) != 0 { name = stages[i].List[0].Text }
		programs = slices.Append(a, programs, core.NewString(a, name))
	}
	fields := []core.RecordField{
		{Key: "kinds", Value: core.NewList(a, host.PayloadList(payload, "kinds"))},
		{Key: "edges", Value: core.NewList(a, host.PayloadList(payload, "edges"))},
		{Key: "framed", Value: core.NewList(a, host.PayloadList(payload, "framed"))},
		{Key: "programs", Value: core.NewList(a, programs)},
	}
	value := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	for i := range programs { programs[i].Free(a) }; slices.Free(a, programs)
	return value
}

// ProcessGraphSnapshot is advisory progress, not completion or an engine edge.
// Undrained snapshots coalesce per request. Stale requests cannot publish.
func (p *Program) ProcessGraphSnapshot(request host.Request, value core.Value) bool {
	if len(host.PayloadList(request.Payload, "kinds")) == 0 { return false }
	return p.processGraphSnapshot(request, value)
}

func (p *Program) processGraphSnapshot(request host.Request, value core.Value) bool {
	if value.Kind != core.Record { return false }
	for i := range value.Record { field := value.Record[i]; if (field.Key == "states" || field.Key == "edgeMetrics") && field.Value.Kind != core.List { return false } }
	var pending *pendingRequest
	for i := range p.Pending { if p.Pending[i].ID == request.ID { pending = &p.Pending[i]; break } }
	if pending == nil { return false }
	states, metrics := host.PayloadList(value, "states"), host.PayloadList(value, "edgeMetrics")
	if len(states) == 0 { return false }
	kinds, edges := host.PayloadList(pending.Graph, "kinds"), host.PayloadList(pending.Graph, "edges")
	programs, framed := host.PayloadList(pending.Graph, "programs"), host.PayloadList(pending.Graph, "framed")
	if len(states) != len(kinds) || len(programs) != len(kinds) || len(framed) != len(kinds) || (len(metrics) != 0 && len(metrics) != len(edges)) { return false }
	for i := range states { if states[i].Kind != core.Int || states[i].Int < 0 || states[i].Int > 7 { return false } }
	for i := range metrics {
		row := metrics[i]
		if row.Kind != core.List || len(row.List) != 7 { return false }
		for j := range row.List { if row.List[j].Kind != core.Int || row.List[j].Int < -1 || row.List[j].Int > 9007199254740991 { return false } }
		if row.List[0].Int < 0 || row.List[0].Int > 3 || row.List[1].Int < 0 || row.List[4].Int < 0 || row.List[5].Int < 0 || row.List[6].Int < 0 || row.List[6].Int > 1 { return false }
		if row.List[0].Int != 0 {
			lines := framed[int(edges[i].List[0].Int)].Bool
			if lines && (row.List[2].Int < 0 || row.List[3].Int < 0) { return false }
			if !lines && (row.List[2].Int != -1 || row.List[3].Int != -1) { return false }
			if row.List[4].Int > 65536 || row.List[5].Int > 1024 { return false }
		}
	}
	names := []string{"unknown", "running", "waiting-input", "waiting-output", "completed", "failed", "cancelled", "timed-out"}
	var nodes, measured []core.Value
	for i := range states {
		kind := kinds[i].Text; if kind == "" { kind = "process" }
		fields := []core.RecordField{{Key: "id", Value: core.Value{Kind: core.Int, Int: int64(i)}}, {Key: "state", Value: core.NewString(p.Alloc, names[int(states[i].Int)])}, {Key: "kind", Value: core.NewString(p.Alloc, kind)}, {Key: "program", Value: programs[i]}}
		nodes = slices.Append(p.Alloc, nodes, core.NewRecord(p.Alloc, fields)); fields[1].Value.Free(p.Alloc); fields[2].Value.Free(p.Alloc)
	}
	for i := range edges {
		row := []core.Value{{Kind: core.Int}, {Kind: core.Int}, {Kind: core.Int}, {Kind: core.Int}, {Kind: core.Int}, {Kind: core.Int}, {Kind: core.Int}}
		if i < len(metrics) { row = metrics[i].List }
		mode := "unavailable"; if row[0].Int == 1 || row[0].Int == 3 { mode = "completed-write" }; if row[0].Int == 2 { mode = "read" }
		edge := edges[i].List
		kind := "bytes"; if framed[int(edge[0].Int)].Bool { kind = "lines" }
		argument := int64(0); if len(edge) == 3 { argument = edge[2].Int }
		fields := []core.RecordField{{Key: "id", Value: core.Value{Kind: core.Int, Int: int64(i)}}, {Key: "measurement", Value: core.NewString(p.Alloc, mode)}, {Key: "from", Value: edge[0]}, {Key: "to", Value: edge[1]}, {Key: "argvIndex", Value: core.Value{Kind: core.Int, Int: argument}}, {Key: "kind", Value: core.NewString(p.Alloc, kind)}}
		keys := []string{"bytes", "items", "droppedItems", "queueHighWaterBytes", "queueHighWaterItems"}
		var parts []core.RecordField
		for j := range fields { parts = slices.Append(p.Alloc, parts, fields[j]) }
		for j := range keys {
			counter := core.Value{Kind: core.Nil}
			if row[0].Int != 0 && row[j + 1].Int >= 0 && (j < 3 || row[0].Int == 3) { counter = row[j + 1] }
			parts = slices.Append(p.Alloc, parts, core.RecordField{Key: keys[j], Value: counter})
		}
		parts = slices.Append(p.Alloc, parts, core.RecordField{Key: "saturated", Value: core.Value{Kind: core.Bool, Bool: row[6].Int != 0}})
		measured = slices.Append(p.Alloc, measured, core.NewRecord(p.Alloc, parts))
		fields[1].Value.Free(p.Alloc); fields[5].Value.Free(p.Alloc); slices.Free(p.Alloc, parts)
	}
	fields := []core.RecordField{{Key: "terminal", Value: core.Value{Kind: core.Bool, Bool: false}}, {Key: "nodes", Value: core.NewList(p.Alloc, nodes)}, {Key: "edges", Value: core.NewList(p.Alloc, measured)}}
	graph := core.NewRecord(p.Alloc, fields)
	for i := range fields { fields[i].Value.Free(p.Alloc) }
	for i := range nodes { nodes[i].Free(p.Alloc) }; slices.Free(p.Alloc, nodes)
	for i := range measured { measured[i].Free(p.Alloc) }; slices.Free(p.Alloc, measured)
	for i := range p.Events {
		if p.Events[i].Kind == ProcessSnapshot && p.Events[i].RequestID == request.ID { p.Events[i].Value.Free(p.Alloc); p.Events[i].Value = graph; return true }
	}
	event := Event{Kind: ProcessSnapshot, RequestID: request.ID, NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, Value: graph}
	entry := p.streamInstance(pending.NodeID)
	if entry != nil { event.Target, event.Key = entry.Plan.Target, entry.Node.Key }
	p.emit(event)
	return true
}
