package program

import (
	"encoding/hex"
	"kame/core"
	"kame/internal/mem"
	"testing"
)

func TestWatchObservationsUseInterestedConsumersAndCurrentViews(t *testing.T) {
	p := Program{Alloc: mem.System, Engine: core.NewEngine(mem.System)}
	defer p.Engine.Free()
	file := core.ResourceKey{Kind: core.ResourceFile, Name: "input"}
	content := core.Observation{Key: file, Aspect: core.ObservationContent, Signature: core.ContentSignature([]byte("accepted"))}
	link := core.Observation{Key: file, Aspect: core.ObservationLinkMetadata}
	observe := func(name string, item *core.Observation) *core.Node {
		return p.Engine.Add(core.ResourceKey{Name: name}, func(c *core.EngineContext, _ int64) core.ProducerResult {
			c.ObserveAspect(item.Key, item.Aspect, item.Signature)
			c.Publish(core.Value{Kind: core.Nil})
			return core.ProducerCompleted
		}, nil)
	}
	a, b := observe("a", &content), observe("b", &link)
	p.Engine.Request(a)
	p.Engine.Request(b)
	if p.Engine.Step() != a || p.Engine.Step() != b {
		t.Fatal("fixture did not record both views")
	}
	p.Engine.Cancel(b)
	first := p.WatchObservations()
	if first.MetadataObserved(file) || first.AcceptedFingerprint(file) != hex.EncodeToString(content.Signature.Digest[:]) {
		t.Fatal("unused consumer changed the accepted baseline")
	}
	p.Engine.Request(b)
	next := p.WatchObservations()
	if !next.MetadataObserved(file) || next.AcceptedFingerprint(file) != "unobserved" || first.MetadataObserved(file) {
		t.Fatal("link metadata promotion did not request a fresh scan")
	}
	link.Aspect, link.Signature = core.ObservationContent, core.ContentSignature([]byte("different"))
	p.Engine.Invalidate(b)
	if p.Engine.Step() != b {
		t.Fatal("second consumer did not replace its observation")
	}
	next = p.WatchObservations()
	if next.MetadataObserved(file) || next.AcceptedFingerprint(file) != "unobserved" {
		t.Fatal("conflicting accepted reads must not establish a baseline")
	}
	p.InvalidateResource(file)
	if p.Engine.Step() != a || p.Engine.Step() != b || a.Revision != 2 || b.Revision != 3 {
		t.Fatal("both context-local readers must rerun after a file edit")
	}
}
