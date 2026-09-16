package host_test

import (
	"littlemake/core"
	"littlemake/host"
	"solod.dev/so/testing"
)

func TestQueueOwnsPayloadAndCorrelation(t *testing.T) {
	a := t.Allocator()
	queue := host.NewQueue(a)
	payload := core.NewString(a, "request")
	id := queue.Submit(7, 3, 2, host.RequestReadFile, payload)
	payload.Free(a)
	next := queue.Next()
	if !next.OK || next.Request.ID != id || next.Request.NodeID != 7 || next.Request.Generation != 3 || next.Request.Attempt != 2 || next.Request.Payload.Text != "request" {
		t.Error("queue did not retain correlated request payload")
	}
	next.Request.Free(a)
	queue.Free()
}
