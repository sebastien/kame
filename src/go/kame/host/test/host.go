package host_test

import (
	"kame/core"
	"kame/host"
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

func TestPayloadRecordsExposePathsAndScripts(t *testing.T) {
	a := t.Allocator()
	file := host.FilePayload(a, host.OpWildcard, "src/**/*.km")
	if host.PayloadPath(file) != "src/**/*.km" || host.PayloadText(file, host.FieldOp) != host.OpWildcard { t.Error("file payload fields were not readable") }
	file.Free(a)
	process := host.ProcessPayload(a, "printf ok")
	if host.PayloadText(process, host.FieldScript) != "printf ok" { t.Error("process payload script was not readable") }
	process.Free(a)
}

func TestQueueReleasesRecordPayload(t *testing.T) {
	a := t.Allocator()
	queue := host.NewQueue(a)
	payload := host.FilePayload(a, host.OpRead, "input")
	queue.Submit(1, 1, 1, host.RequestReadFile, payload)
	payload.Free(a)
	next := queue.Next()
	if !next.OK { t.Fatal("request missing"); return }
	next.Request.Free(a)
	queue.Free()
}
