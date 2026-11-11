package posix

import (
	"io"
	"kame/host"
	"sync"
)

// Each destination owns its pending queue and one non-evictable write. Closing
// that destination at EOF must not wait for another destination to drain.
type broadcastQueue struct {
	started chan struct{}
	start   sync.Once
	first   bool
	mu      sync.Mutex
	ready   *sync.Cond
	pending [][]byte
	bytes   int
	closed  bool
	writer  io.Writer
}

func runLossyBroadcast(input io.Reader, writers []io.Writer, stage host.ProcessStage, failed func(error)) error {
	budget, capacity := stage.BufferBytes, stage.BufferItems
	if budget == 0 {
		budget = host.RecordLimit
	}
	if capacity == 0 {
		capacity = 64
	}
	if stage.Overflow == 3 {
		capacity = 1
	}
	queues := make([]*broadcastQueue, len(writers))
	var workers sync.WaitGroup
	var failureMu sync.Mutex
	var failure error
	report := func(e error) {
		failureMu.Lock()
		defer failureMu.Unlock()
		if failure == nil {
			failure = e
			failed(e)
		}
	}
	for i, writer := range writers {
		q := &broadcastQueue{writer: writer, started: make(chan struct{}), first: true}
		q.ready = sync.NewCond(&q.mu)
		queues[i] = q
		workers.Add(1)
		go func() {
			defer workers.Done()
			if closer, ok := q.writer.(io.Closer); ok {
				defer closer.Close()
			}
			for {
				q.mu.Lock()
				for len(q.pending) == 0 && !q.closed {
					q.ready.Wait()
				}
				if len(q.pending) == 0 {
					q.mu.Unlock()
					return
				}
				b := q.pending[0]
				q.pending[0] = nil
				q.pending = q.pending[1:]
				q.bytes -= len(b) - 4
				q.start.Do(func() { close(q.started) })
				q.mu.Unlock()
				if _, e := q.writer.Write(b); e != nil {
					report(e)
					return
				}
			}
		}()
	}
	for {
		b, e := readFrame(input, budget)
		if e != nil {
			if e != io.EOF {
				report(e)
			}
			break
		}
		for _, q := range queues {
			q.mu.Lock()
			drop := func() {
				if meter, ok := q.writer.(interface{ dropped() }); ok {
					meter.dropped()
				}
			}
			full := func() bool { return len(q.pending) >= capacity || q.bytes+len(b)-4 > budget }
			if stage.Overflow == 1 && full() {
				drop()
				q.mu.Unlock()
				continue
			}
			for len(q.pending) > 0 && (full() || stage.Overflow == 3) {
				q.bytes -= len(q.pending[0]) - 4
				q.pending[0] = nil
				q.pending = q.pending[1:]
				drop()
			}
			q.pending = append(q.pending, b)
			q.bytes += len(b) - 4
			if meter, ok := q.writer.(interface{ queued(int, int) }); ok {
				meter.queued(q.bytes, len(q.pending))
			}
			first := q.first
			q.first = false
			q.ready.Signal()
			q.mu.Unlock()
			if first {
				<-q.started
			}
		}
	}
	for _, q := range queues {
		q.mu.Lock()
		q.closed = true
		q.ready.Signal()
		q.mu.Unlock()
	}
	workers.Wait()
	return failure
}
