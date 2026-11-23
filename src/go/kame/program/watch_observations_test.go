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
	a := p.Engine.Add(core.ResourceKey{Name: "a"}, nil, nil)
	b := p.Engine.Add(core.ResourceKey{Name: "b"}, nil, nil)
	a.Observations = []core.Observation{content}
	b.Observations = []core.Observation{{Key: file, Aspect: core.ObservationLinkMetadata}}
	p.Engine.Request(a)
	first := p.WatchObservations()
	if first.MetadataObserved(file) || first.AcceptedFingerprint(file) != hex.EncodeToString(content.Signature.Digest[:]) {
		t.Fatal("unused consumer changed the accepted baseline")
	}
	p.Engine.Request(b)
	next := p.WatchObservations()
	if !next.MetadataObserved(file) || next.AcceptedFingerprint(file) != "unobserved" || first.MetadataObserved(file) {
		t.Fatal("link metadata promotion did not request a fresh scan")
	}
	content.Signature = core.ContentSignature([]byte("different"))
	b.Observations = []core.Observation{content}
	next = p.WatchObservations()
	if next.MetadataObserved(file) || next.AcceptedFingerprint(file) != "unobserved" {
		t.Fatal("conflicting accepted reads must not establish a baseline")
	}
}
