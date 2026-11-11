package posix

import "kame/host"

// Evaluator callbacks belong to the host's owner goroutine. Stream workers
// queue bounded requests; Pump executes them between engine ticks, avoiding
// concurrent access to scopes, operation state and allocator bookkeeping.
type predicateRequest struct {
	run      *running
	evaluate func() int
	result   chan int
}

func (h *Host) streamItem(r host.ProcessRequest, run *running, node int, data []byte) int {
	work := &predicateRequest{run: run, evaluate: func() int { return r.StreamItem(r.StreamContext, r.ID, node, data) }, result: make(chan int, 1)}
	select {
	case h.native.predicates <- work:
	case <-run.stopped:
		return -124
	case <-h.native.closed:
		return -124
	}
	select {
	case result := <-work.result:
		return result
	case <-run.stopped:
		return -124
	case <-h.native.closed:
		return -124
	}
}
func (h *Host) evaluatePredicate(work *predicateRequest) {
	select {
	case <-work.run.stopped:
		work.result <- -124
	default:
		work.result <- work.evaluate()
	}
}
func (h *Host) drainPredicates() {
	n := len(h.native.predicates)
	for i := 0; i < n; i++ {
		h.evaluatePredicate(<-h.native.predicates)
	}
}
