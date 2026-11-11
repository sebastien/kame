package wasm

import (
	"kame/core"
	"kame/host"
	"testing"
)

// These are ABI wire numbers, independent of the engine's internal enum.
func TestABIRequestKindWireNumbers(t *testing.T) {
	cases := []struct {
		kind host.RequestKind
		wire uint32
	}{
		{host.RequestReadFile, 1}, {host.RequestWriteFile, 2}, {host.RequestProcess, 3}, {host.RequestEnvironment, 4},
		{host.RequestStatPath, 5}, {host.RequestExpandGlob, 6}, {host.RequestWallTime, 8}, {host.RequestMonotonicTime, 9},
		{host.RequestCacheGet, 10}, {host.RequestCachePut, 11}, {host.RequestCacheDelete, 12}, {host.RequestPrepareOutputs, 21},
		{host.RequestTimer, 22}, {host.RequestProcessCancel, 23}, {host.RequestCacheLock, 24}, {host.RequestCacheUnlock, 25},
		{host.RequestPlugin, 26}, {host.RequestPublishFile, 39}, {host.RequestRecordRead, 40}, {host.RequestRecordClose, 41}, {host.RequestLoadSource, 42},
	}
	for _, c := range cases {
		if got := ABIRequestKind(host.Request{Kind: c.kind}); got != c.wire {
			t.Errorf("engine kind %d: wire number %d, want %d", c.kind, got, c.wire)
		}
	}
}
func TestABIReadRequestPromotions(t *testing.T) {
	cases := map[string]uint32{"resolve-tool": 18, "tool-exists": 19, "output-exists": 17, "file-times": 20, "file-content": 27, "tool-content": 28, "output-content": 29, "exists": 7, "stat": 5, "lstat": 30, "readlink": 31, "wildcard": 6}
	for op, wire := range cases {
		r := host.Request{Kind: host.RequestReadFile, Payload: core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "op", Value: core.Value{Kind: core.String, Text: op}}}}}
		if got := ABIRequestKind(r); got != wire {
			t.Errorf("%s: wire number %d, want %d", op, got, wire)
		}
	}
}
