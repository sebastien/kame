package core_test

import (
	"kame/core"
	"solod.dev/so/math"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type signatureVector struct {
	Text string
	Hex  string
}

func TestDigestStandardVectorsAndIncrementalSnapshots(t *testing.T) {
	vectors := []signatureVector{
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq", "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"},
	}
	for i := range vectors {
		d := core.NewDigest()
		for j := range vectors[i].Text {
			d.Write([]byte(vectors[i].Text[j : j+1]))
		}
		var first, second [32]byte
		d.Sum(first[:])
		d.Sum(second[:])
		for j := range first {
			if first[j] != second[j] {
				t.Error("digest snapshot mutated the state")
			}
			if "0123456789abcdef"[first[j]>>4] != vectors[i].Hex[j*2] || "0123456789abcdef"[first[j]&15] != vectors[i].Hex[j*2+1] {
				t.Error("digest differs from SHA-256 vector")
			}
		}
	}
	d := core.NewDigest()
	d.Write([]byte("a"))
	var before [32]byte
	d.Sum(before[:])
	d.Write([]byte("bc"))
	var after [32]byte
	d.Sum(after[:])
	actual := core.Signature{Mode: core.SignatureContent}
	actual.Digest = after
	if !actual.Equal(core.ContentSignature([]byte("abc"))) {
		t.Error("write after snapshot changed the digest")
	}
	// Exercise complete blocks and padding at both sides of the 56-byte boundary.
	data := slices.Make[byte](t.Allocator(), 129)
	for i := range data {
		data[i] = byte(i)
	}
	for n := 55; n <= len(data); n++ {
		d = core.NewDigest()
		d.Write(data[:n/2])
		d.Write(data[n/2 : n])
		d.Sum(after[:])
		actual.Digest = after
		if !actual.Equal(core.ContentSignature(data[:n])) {
			t.Error("chunk boundaries changed the digest")
		}
	}
	slices.Free(t.Allocator(), data)
}

func TestValueSignaturesAreCanonicalAndTyped(t *testing.T) {
	a := t.Allocator()
	left := core.NewRecord(a, []core.RecordField{{Key: "z", Value: core.Value{Kind: core.Int, Int: 3}}, {Key: "a", Value: core.Value{Kind: core.Bool, Bool: true}}})
	right := core.NewRecord(a, []core.RecordField{{Key: "a", Value: core.Value{Kind: core.Bool, Bool: true}}, {Key: "z", Value: core.Value{Kind: core.Int, Int: 3}}})
	if !core.ValueSignature(left).Equal(core.ValueSignature(right)) {
		t.Error("record insertion order changed identity")
	}
	left.Free(a)
	right.Free(a)
	if core.ValueSignature(core.Value{Kind: core.String, Text: "abc"}).Equal(core.ValueSignature(core.Value{Kind: core.Bytes, Bytes: []byte("abc")})) {
		t.Error("string and bytes share identity")
	}
	if core.ValueSignature(core.Value{Kind: core.Int}).Equal(core.ValueSignature(core.Value{Kind: core.Float})) {
		t.Error("integer and float share identity")
	}
	if !core.ValueSignature(core.Value{Kind: core.Float}).Equal(core.ValueSignature(core.Value{Kind: core.Float, Float: math.Float64frombits(0x8000000000000000)})) {
		t.Error("signed zeros differ")
	}
	kinds := []core.Kind{core.Callable, core.Process}
	for _, kind := range kinds {
		s := core.ValueSignature(core.Value{Kind: kind})
		if s.Mode != core.SignatureUnavailable || s.Equal(s) {
			t.Error("opaque value proves reusable equality")
		}
	}
	duplicate := core.NewRecord(a, []core.RecordField{{Key: "a"}, {Key: "a"}})
	if core.ValueSignature(duplicate).Mode != core.SignatureUnavailable {
		t.Error("duplicate record keys are reusable")
	}
	duplicate.Free(a)
}

func TestSignatureRecordRejectsMissingErrorsAndDuplicateInputs(t *testing.T) {
	s := core.ContentSignature([]byte("input"))
	implementation := core.ContentSignature([]byte("implementation"))
	inputs := []core.Observation{{Key: core.ResourceKey{Kind: core.ResourceFile, Name: "a"}, Signature: s}, {Key: core.ResourceKey{Kind: core.ResourceEnvironment, Name: "b"}, Signature: s}}
	reversed := []core.Observation{inputs[1], inputs[0]}
	record := core.SignatureRecord{Implementation: implementation, Inputs: inputs}
	current := core.SignatureRecord{Implementation: implementation, Inputs: reversed}
	if !record.Matches(&current) {
		t.Error("input order changed reuse")
	}
	reversed[0] = inputs[0]
	if record.Matches(&current) || current.Matches(&record) {
		t.Error("duplicate inputs accepted")
	}
	reversed[0] = inputs[1]
	reversed[0].Signature = core.Signature{}
	if record.Matches(&current) || current.Matches(&current) {
		t.Error("unavailable input proves equality")
	}
	missing := core.Signature{Mode: core.SignatureMissing}
	if !missing.Equal(missing) || missing.Equal(core.Signature{}) || missing.Equal(core.ContentSignature(nil)) {
		t.Error("missing, unreadable and empty file identities collapsed")
	}
	metadata := s
	metadata.Mode = core.SignatureMetadata
	if metadata.Equal(s) {
		t.Error("metadata observation accepted as content")
	}
	invalid := s
	invalid.Mode = core.SignatureMode(99)
	if invalid.Equal(invalid) {
		t.Error("unknown observation mode accepted")
	}
	current = record
	current.Outputs = []core.Observation{{Key: inputs[0].Key, Signature: s}}
	if record.Matches(&current) {
		t.Error("artifact membership is not checked")
	}
	record.Outputs = []core.Observation{{Key: inputs[0].Key, Signature: s}}
	if !record.Matches(&current) {
		t.Error("equal artifacts rejected")
	}
	current.Outputs[0].Signature = core.ContentSignature([]byte("tampered"))
	if record.Matches(&current) {
		t.Error("artifact content is not checked")
	}
}

func observeThenPublish(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	signature := core.ContentSignature([]byte("bytes"))
	c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: "./input"}, signature)
	c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: "input"}, signature)
	c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: "USED"}, signature)
	c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: "USED"}, core.ContentSignature([]byte("changed")))
	c.PublishSigned(core.NewString(c.Allocator(), "input"), signature)
	return core.ProducerCompleted
}

func TestEngineOwnsConsumedObservationsAndSignedPublication(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	n := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "observe"}, observeThenPublish, nil)
	e.Request(n)
	e.Step()
	if len(n.Observations) != 2 {
		t.Error("file observations were not canonicalized and deduplicated")
	} else if n.Observations[1].Signature.Mode != core.SignatureUnavailable {
		t.Error("conflicting reads accepted")
	}
	if !n.Signature.Equal(core.ContentSignature([]byte("bytes"))) || n.Latest.Text != "input" {
		t.Error("file identity was replaced with path value identity")
	}
	e.Invalidate(n)
	if len(n.Observations) != 0 {
		t.Error("new generation inherited old observations")
	}
	e.Step()
	e.Free()
}

func consumeSignature(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	key := core.ResourceKey{Kind: core.ResourceTask, Name: "observe"}
	if !c.Dependency(key) {
		return core.ProducerWaiting
	}
	value := c.Value(key)
	if !value.OK {
		return core.ProducerWaiting
	}
	c.Publish(value.Value.Clone(c.Allocator()))
	return core.ProducerCompleted
}

func TestValueReadRecordsDependencySignature(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	source := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "observe"}, observeThenPublish, nil)
	consumer := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "consumer"}, consumeSignature, nil)
	e.Request(consumer)
	for i := 0; i < 4; i++ {
		e.Step()
	}
	if len(consumer.Observations) != 1 || !consumer.Observations[0].Signature.Equal(source.Signature) {
		t.Error("value read lost dependency's semantic identity")
	}
	e.Free()
}

func failThenPublishSigned(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	c.Fail(core.Diagnostic{Code: "FAILED"})
	c.PublishSigned(core.NewString(c.Allocator(), "rejected"), core.ContentSignature([]byte("rejected")))
	c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: "rejected"}, core.ContentSignature([]byte("rejected")))
	return core.ProducerFailed
}

func TestFailedPublicationCannotReplaceSignatureOrObservations(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	n := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "failed"}, failThenPublishSigned, nil)
	e.Request(n)
	e.Step()
	if n.Current || n.Signature.Mode != core.SignatureUnavailable || len(n.Observations) != 0 {
		t.Error("failed producer published a reusable identity")
	}
	e.Free()
}

func observeFileViews(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	key := core.ResourceKey{Kind: core.ResourceFile, Name: "./input"}
	c.ObserveAspect(key, core.ObservationContent, core.ContentSignature([]byte("A")))
	c.ObserveAspect(key, core.ObservationExistence, core.ValueSignature(core.Value{Kind: core.Bool, Bool: true}))
	c.ObserveAspect(key, core.ObservationMetadata, core.ContentSignature([]byte("metadata")))
	c.ObserveAspect(key, core.ObservationContent, core.ContentSignature([]byte("B")))
	c.Publish(core.Value{Kind: core.Nil})
	return core.ProducerCompleted
}

func TestObservationViewsRejectConflictsWithoutPoisoningOtherViews(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	n := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "views"}, observeFileViews, nil)
	e.Request(n)
	e.Step()
	if len(n.Observations) != 3 {
		t.Error("resource views collapsed")
	} else {
		if n.Observations[0].Signature.Mode != core.SignatureUnavailable {
			t.Error("conflicting content reads were accepted")
		}
		if n.Observations[1].Signature.Mode != core.SignatureContent || n.Observations[2].Signature.Mode != core.SignatureContent {
			t.Error("content conflict poisoned other views")
		}
	}
	s := core.ContentSignature([]byte("same digest"))
	content := []core.Observation{{Key: core.ResourceKey{Kind: core.ResourceFile, Name: "input"}, Aspect: core.ObservationContent, Signature: s}}
	metadata := []core.Observation{{Key: content[0].Key, Aspect: core.ObservationMetadata, Signature: s}}
	left := core.SignatureRecord{Implementation: s, Inputs: content}
	right := core.SignatureRecord{Implementation: s, Inputs: metadata}
	if left.Matches(&right) {
		t.Error("metadata view substituted for content")
	}
	metadata[0].Aspect = core.ObservationAspect(99)
	if right.Matches(&right) {
		t.Error("unknown resource view was accepted")
	}
	e.Free()
}

func TestAcceptedSignatureRecordRoundTripAndCorruption(t *testing.T) {
	a := t.Allocator()
	s := core.ContentSignature([]byte("implementation"))
	inputs := []core.Observation{{Key: core.ResourceKey{Kind: core.ResourceFile, Name: "input"}, Aspect: core.ObservationContent, Signature: core.ContentSignature([]byte("bytes"))}, {Key: core.ResourceKey{Kind: core.ResourceEnvironment, Name: "MISSING"}, Signature: core.Signature{Mode: core.SignatureMissing}}}
	outputs := []core.Observation{{Key: core.ResourceKey{Kind: core.ResourceFile, Name: "output"}, Aspect: core.ObservationContent, Signature: core.ContentSignature(nil)}}
	record := core.SignatureRecord{Implementation: s, Inputs: inputs, Outputs: outputs}
	data := core.EncodeSignatureRecord(a, &record)
	if len(data) == 0 {
		t.Fatal("valid accepted record was not encoded")
		return
	}
	var decoded core.SignatureRecord
	if !core.DecodeSignatureRecord(a, data, &decoded) || !record.Matches(&decoded) {
		t.Error("accepted record did not round trip")
	}
	decoded.Free(a)
	for n := 0; n < len(data); n++ {
		if core.DecodeSignatureRecord(a, data[:n], &decoded) {
			t.Error("truncated accepted record was decoded")
			decoded.Free(a)
		}
		before := data[n]
		data[n] ^= 1
		if core.DecodeSignatureRecord(a, data, &decoded) {
			t.Error("corrupt accepted record was decoded")
			decoded.Free(a)
		}
		data[n] = before
	}
	slices.Free(a, data)
	inputs[1] = inputs[0]
	if core.EncodeSignatureRecord(a, &record) != nil {
		t.Error("duplicate observations were persisted")
	}
}
