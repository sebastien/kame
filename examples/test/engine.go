package examples_test

import (
	"littlemake/core"
	"littlemake/examples"
	"solod.dev/so/io"
	"solod.dev/so/testing"
)

// TestBuildExample exercises backward demand propagation, forward value
// propagation, subscription, and a rebuild end to end. t.Allocator() fails the
// test on any leaked value, node, edge, or event.
func TestBuildExample(t *testing.T) {
	b := examples.NewBuild(t.Allocator())
	report := b.Run(io.Discard)
	if !report.OK() {
		t.Error("build example invariants failed")
	}
	if report.Steps != 10 || report.SourceRuns != 2 {
		t.Errorf("expected 10 steps and 2 source runs, got %d and %d", int(report.Steps), int(report.SourceRuns))
	}
	if b.Source.Generation != 1 || b.Default.Generation != 1 {
		t.Error("input edit did not invalidate the graph once")
	}
	if b.Default.Revision != 2 || b.Default.Latest.Kind != core.Record {
		t.Error("target did not publish a replacement result")
	}
	b.Free()
}

// TestStreamExample exercises asynchronous batches, a host completion that
// resumes a waiting source, and subscriber queue coalescing end to end.
func TestStreamExample(t *testing.T) {
	s := examples.NewStream(t.Allocator())
	report := s.Run(io.Discard)
	if !report.OK() {
		t.Error("stream example invariants failed")
	}
	if s.Logs.Revision != 2 || s.Logs.State != core.NodeComplete {
		t.Error("log source did not publish two batches and complete")
	}
	if s.Burst.Revision != 12 {
		t.Error("burst source did not publish every value")
	}
	s.Free()
}
