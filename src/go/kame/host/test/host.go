package hostcheck

import (
	"kame/core"
	"kame/host"
	"kame/internal/testing"
	"strings"
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
	if host.PayloadPath(file) != "src/**/*.km" || host.PayloadText(file, host.FieldOp) != host.OpWildcard {
		t.Error("file payload fields were not readable")
	}
	file.Free(a)
	process := host.ProcessPayload(a, "printf ok")
	if host.PayloadText(process, host.FieldScript) != "printf ok" {
		t.Error("process payload script was not readable")
	}
	process.Free(a)
	key := host.CacheGetPayload(a, []byte{1, 2})
	wantKey := []byte{1, 2}
	if string(host.CacheKey(key)) != string(wantKey) {
		t.Error("cache get payload key was not readable")
	}
	key.Free(a)
	put := host.CachePutPayload(a, []byte{3}, []byte{4, 5})
	wantPutKey, wantRecord := []byte{3}, []byte{4, 5}
	if string(host.CacheKey(put)) != string(wantPutKey) || string(host.CacheRecord(put)) != string(wantRecord) {
		t.Error("cache put payload was not readable")
	}
	put.Free(a)
}

func TestStderrRoutingSurvivesTheConfiguredGraph(t *testing.T) {
	a := t.Allocator()
	argv := []core.Value{core.NewString(a, "sh")}
	command := core.NewList(a, argv)
	payload := host.PipelinePayload(a, []core.Value{command})
	command.Free(a)
	for i := range argv {
		argv[i].Free(a)
	}
	setup := host.StageSetupPayload(a, "", 0, nil)
	host.ConfigureStderr(a, &setup, "./errors", true, host.StderrFile)
	host.ConfigureStages(a, &payload, []core.Value{setup})
	setups := host.PayloadSetups(payload)
	if len(setups) != 1 {
		t.Error("configured stage setup was lost")
	} else {
		if host.PayloadText(setups[0], host.FieldError) != "./errors" || !host.PayloadBool(setups[0], host.FieldErrorAppend) || host.PayloadInt(setups[0], host.FieldErrorMode) != host.StderrFile {
			t.Error("stderr routing fields were not readable")
		}
		if host.PayloadBool(setups[0], host.FieldAppend) {
			t.Error("stderr append leaked into the stdout append flag")
		}
	}
	// The JavaScript host reads the same wire text, so the routing must be
	// visible there even when no stdout redirection exists.
	graph := host.PayloadText(payload, host.FieldData)
	if !strings.Contains(graph, `"errorMode":1`) || !strings.Contains(graph, `"errorAppend":true`) || !strings.Contains(graph, `"error":"./errors"`) {
		t.Error("configured graph text omitted stderr routing")
	}
	if strings.Contains(graph, `"acceptExit"`) {
		t.Error("configured graph published an empty acceptExit list")
	}
	payload.Free(a)
	setup.Free(a)
}

func TestServiceCancellationPayloadPreservesProcessIDAndGrace(t *testing.T) {
	a := t.Allocator()
	payload := host.ServiceCancelPayload(a, 9223372036854775000, 250)
	if got := host.PayloadText(payload, host.FieldData); got != `{"id":"9223372036854775000","graceMS":250}` {
		t.Error("service cancellation payload lost its exact process ID or grace period")
	}
	payload.Free(a)
}

func TestQueueReleasesRecordPayload(t *testing.T) {
	a := t.Allocator()
	queue := host.NewQueue(a)
	payload := host.FilePayload(a, host.OpRead, "input")
	queue.Submit(1, 1, 1, host.RequestReadFile, payload)
	payload.Free(a)
	next := queue.Next()
	if !next.OK {
		t.Fatal("request missing")
		return
	}
	next.Request.Free(a)
	queue.Free()
}
