package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Accepted records are bounded, versioned and self-checking. Malformed,
// duplicate or unavailable observations cannot be persisted as reusable facts.
const SignatureRecordMax = 16 * 1024 * 1024

type signatureRecordCodec struct {
	Data   []byte
	At     int
	Failed bool
}

func (c *signatureRecordCodec) put(n uint32) {
	for i := 0; i < 4; i++ {
		c.Data[c.At] = byte(n >> uint(i*8))
		c.At++
	}
}

func (c *signatureRecordCodec) get() uint32 {
	if len(c.Data)-c.At < 4 {
		c.Failed = true
		return 0
	}
	var n uint32
	for i := 0; i < 4; i++ {
		n |= uint32(c.Data[c.At]) << uint(i*8)
		c.At++
	}
	return n
}

func (c *signatureRecordCodec) writeSignature(s Signature) {
	c.Data[c.At] = byte(s.Mode)
	c.At++
	copy(c.Data[c.At:c.At+32], s.Digest[:])
	c.At += 32
}

func (c *signatureRecordCodec) readSignature() Signature {
	if len(c.Data)-c.At < 33 {
		c.Failed = true
		return Signature{}
	}
	s := Signature{Mode: SignatureMode(c.Data[c.At])}
	c.At++
	copy(s.Digest[:], c.Data[c.At:c.At+32])
	c.At += 32
	if s.Mode < SignatureContent || s.Mode > SignatureMissing {
		c.Failed = true
	}
	return s
}

func (c *signatureRecordCodec) writeObservations(values []Observation) {
	c.put(uint32(len(values)))
	for i := range values {
		item := values[i]
		c.Data[c.At], c.Data[c.At+1] = byte(item.Key.Kind), byte(item.Aspect)
		c.At += 2
		c.writeSignature(item.Signature)
		c.put(uint32(len(item.Key.Name)))
		copy(c.Data[c.At:c.At+len(item.Key.Name)], item.Key.Name)
		c.At += len(item.Key.Name)
	}
}

func (c *signatureRecordCodec) readObservations(a mem.Allocator) []Observation {
	n := c.get()
	if c.Failed || uint64(n) > uint64((len(c.Data)-c.At)/39) {
		c.Failed = true
		return nil
	}
	var out []Observation
	for i := uint32(0); i < n; i++ {
		if len(c.Data)-c.At < 39 {
			c.Failed = true
			break
		}
		kind, aspect := ResourceKind(c.Data[c.At]), ObservationAspect(c.Data[c.At+1])
		c.At += 2
		s := c.readSignature()
		length := c.get()
		if c.Failed || uint64(length) > uint64(len(c.Data)-c.At) || kind < ResourceDefinition || kind > ResourceOperation || aspect < ObservationValue || aspect > ObservationMetadata {
			c.Failed = true
			break
		}
		key := NewResourceKey(a, kind, string(c.Data[c.At:c.At+int(length)]))
		c.At += int(length)
		out = slices.Append(a, out, Observation{Key: key, Aspect: aspect, Signature: s})
	}
	return out
}

func EncodeSignatureRecord(a mem.Allocator, record *SignatureRecord) []byte {
	if !record.Matches(record) {
		return nil
	}
	n := 4 + 33 + 8 + 32
	for i := range record.Inputs {
		length := len(record.Inputs[i].Key.Name)
		if length > SignatureRecordMax-n-39 {
			return nil
		}
		n += 39 + length
	}
	for i := range record.Outputs {
		length := len(record.Outputs[i].Key.Name)
		if length > SignatureRecordMax-n-39 {
			return nil
		}
		n += 39 + length
	}
	c := signatureRecordCodec{Data: slices.Make[byte](a, n), At: 4}
	copy(c.Data[:4], "KSR1")
	c.writeSignature(record.Implementation)
	c.writeObservations(record.Inputs)
	c.writeObservations(record.Outputs)
	digest := ContentSignature(c.Data[:c.At])
	copy(c.Data[c.At:], digest.Digest[:])
	return c.Data
}

// DecodeSignatureRecord leaves out untouched on failure. Success transfers
// allocator-owned keys and slices to the caller.
func DecodeSignatureRecord(a mem.Allocator, data []byte, out *SignatureRecord) bool {
	if len(data) < 77 || len(data) > SignatureRecordMax || string(data[:4]) != "KSR1" {
		return false
	}
	end := len(data) - 32
	digest := ContentSignature(data[:end])
	for i := range digest.Digest {
		if digest.Digest[i] != data[end+i] {
			return false
		}
	}
	c := signatureRecordCodec{Data: data[:end], At: 4}
	r := SignatureRecord{Implementation: c.readSignature()}
	r.Inputs = c.readObservations(a)
	if !c.Failed {
		r.Outputs = c.readObservations(a)
	}
	if c.Failed || c.At != end || !r.Matches(&r) {
		r.Free(a)
		return false
	}
	*out = r
	return true
}
