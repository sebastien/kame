package host

import (
	"kame/core"
	"kame/internal/mem"
)

// Fixed-width internal completion rows. Public results name fields and use nil
// for unavailable measurements; hosts never infer traffic on direct OS pipes.
func ProcessMetricsPayload(a mem.Allocator, metrics []ProcessEdgeMetrics) core.Value {
	var rows []core.Value
	for i := range metrics {
		m := metrics[i]
		saturated := int64(0)
		if m.Saturated {
			saturated = 1
		}
		values := []int64{int64(m.Measurement), m.Bytes, m.Items, m.DroppedItems, m.QueueHighWaterBytes, m.QueueHighWaterItems, saturated}
		var fields []core.Value
		for j := range values {
			fields = append(fields, core.Value{Kind: core.Int, Int: values[j]})
		}
		rows = append(rows, core.NewList(a, fields))
		mem.FreeSlice(nil, fields)
	}
	value := core.NewList(a, rows)
	for i := range rows {
		rows[i].Free(a)
	}
	mem.FreeSlice(nil, rows)
	return value
}

func ProcessSnapshotPayload(a mem.Allocator, states []int, metrics []ProcessEdgeMetrics) core.Value {
	var values []core.Value
	for i := range states {
		values = append(values, core.Value{Kind: core.Int, Int: int64(states[i])})
	}
	fields := []core.RecordField{{Key: "states", Value: core.NewList(a, values)}, {Key: "edgeMetrics", Value: ProcessMetricsPayload(a, metrics)}}
	value := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	mem.FreeSlice(nil, values)
	return value
}
