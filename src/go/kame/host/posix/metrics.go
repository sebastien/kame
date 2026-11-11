package posix

import (
	"io"
	"kame/host"
	"os"
	"sync/atomic"
	"time"
)

type nodeMeter struct{ finished, reading, writing atomic.Int32 }

func (n *nodeMeter) state() int {
	if s := n.finished.Load(); s >= 4 {
		return int(s)
	}
	if n.writing.Load() > 0 {
		return 3
	}
	if n.reading.Load() > 0 {
		return 2
	}
	return 1
}
func (n *nodeMeter) complete(result host.ProcessStageResult) {
	state := int32(4)
	if result.Status != 0 || result.Outcome == host.ProcessFailed {
		state = 5
	}
	if result.Outcome == host.ProcessCancelled {
		state = 6
	}
	if result.Outcome == host.ProcessTimedOut {
		state = 7
	}
	n.finished.Store(state)
}

type edgeMeter struct {
	measurement                                 int
	framed                                      bool
	queued                                      bool
	bytes, items, dropped, highBytes, highItems atomic.Int64
	saturated                                   atomic.Bool
}

func (m *edgeMeter) add(counter *atomic.Int64, n int64) {
	for {
		old := counter.Load()
		next := old + n
		if next < old {
			next = int64(^uint64(0) >> 1)
			m.saturated.Store(true)
		}
		if counter.CompareAndSwap(old, next) {
			return
		}
	}
}
func atomicMax(counter *atomic.Int64, n int64) {
	for {
		old := counter.Load()
		if old >= n || counter.CompareAndSwap(old, n) {
			return
		}
	}
}
func (m *edgeMeter) record(bytes int) { m.add(&m.bytes, int64(bytes)); m.add(&m.items, 1) }
func (m *edgeMeter) queue(bytes, items int) {
	atomicMax(&m.highBytes, int64(bytes))
	atomicMax(&m.highItems, int64(items))
}
func (m *edgeMeter) snapshot() host.ProcessEdgeMetrics {
	items, dropped := int64(-1), int64(-1)
	if m.framed {
		items, dropped = m.items.Load(), m.dropped.Load()
	}
	if m.measurement == 0 {
		return host.ProcessEdgeMetrics{}
	}
	highBytes, highItems := int64(0), int64(0)
	if m.queued {
		highBytes, highItems = m.highBytes.Load(), m.highItems.Load()
	}
	mode := m.measurement
	if m.queued {
		mode = 3
	}
	return host.ProcessEdgeMetrics{Measurement: mode, Bytes: m.bytes.Load(), Items: items, DroppedItems: dropped, QueueHighWaterBytes: highBytes, QueueHighWaterItems: highItems, Saturated: m.saturated.Load()}
}
func edgeSnapshots(meters []edgeMeter) []host.ProcessEdgeMetrics {
	out := make([]host.ProcessEdgeMetrics, len(meters))
	for i := range meters {
		out[i] = meters[i].snapshot()
	}
	return out
}

type meteredReader struct {
	file  *os.File
	meter *edgeMeter
	node  *nodeMeter
}

func (r *meteredReader) Read(b []byte) (int, error) {
	r.node.reading.Add(1)
	defer r.node.reading.Add(-1)
	n, e := r.file.Read(b)
	if r.meter.measurement == 2 && !r.meter.framed {
		r.meter.add(&r.meter.bytes, int64(n))
	}
	return n, e
}
func (r *meteredReader) frameRead(size int) {
	if r.meter.measurement == 2 && r.meter.framed {
		r.meter.record(size)
	}
}

type meteredWriter struct {
	file  *os.File
	meter *edgeMeter
	node  *nodeMeter
}

func (w *meteredWriter) Write(b []byte) (int, error) {
	w.node.writing.Add(1)
	defer w.node.writing.Add(-1)
	n, e := w.file.Write(b)
	if w.meter.measurement == 1 {
		if w.meter.framed {
			if n == len(b) {
				w.meter.record(max(0, n-4))
			} else {
				w.meter.add(&w.meter.bytes, int64(max(0, n-4)))
			}
		} else {
			w.meter.add(&w.meter.bytes, int64(n))
		}
	}
	return n, e
}
func (w *meteredWriter) Close() error            { return w.file.Close() }
func (w *meteredWriter) queued(bytes, items int) { w.meter.queue(bytes, items) }
func (w *meteredWriter) dropped()                { w.meter.add(&w.meter.dropped, 1) }
func (h *Host) refreshSnapshots() {
	now := time.Now()
	h.native.mu.Lock()
	defer h.native.mu.Unlock()
	for _, run := range h.native.processes {
		if run.snapshot == nil || now.Before(run.nextSnapshot) {
			continue
		}
		run.nextSnapshot = now.Add(100 * time.Millisecond)
		event := run.snapshot()
		replaced := false
		for i := range h.pending {
			if h.pending[i].ID == event.ID && h.pending[i].Kind == host.ProcessSnapshot {
				h.pending[i] = event
				replaced = true
				break
			}
		}
		if !replaced {
			h.pending = append(h.pending, event)
		}
	}
}

var _ io.Writer = (*meteredWriter)(nil)
