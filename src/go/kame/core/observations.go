package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ObservationAspect distinguishes independent views of the same resource.
type ObservationAspect int

const (
	ObservationValue ObservationAspect = iota
	ObservationContent
	ObservationExistence
	ObservationMetadata
)

// Observation is a generation's accepted resource identity. It is independent
// of scheduling edges: context-local reads and operation versions are facts too.
type Observation struct {
	Key       ResourceKey
	Aspect    ObservationAspect
	Signature Signature
}

func FreeObservations(a mem.Allocator, values []Observation) {
	for i := range values {
		values[i].Key.Free(a)
	}
	slices.Free(a, values)
}

// Observe records what the computation actually consumed, not a later live
// read. Conflicting observations make the generation non-reusable.
func (c *EngineContext) Observe(key ResourceKey, signature Signature) {
	c.ObserveAspect(key, ObservationValue, signature)
}

// ObserveAspect keeps intentional metadata and existence reads separate from
// content reads of the same resource. Neither proves the other unchanged.
func (c *EngineContext) ObserveAspect(key ResourceKey, aspect ObservationAspect, signature Signature) {
	if c.Failed() || c.node.State == NodeComplete {
		return
	}
	name, owned := canonicalName(c.engine.Alloc, key)
	key.Name = name
	for i := range c.node.Observations {
		item := &c.node.Observations[i]
		if item.Key.Kind == key.Kind && item.Key.Name == key.Name && item.Aspect == aspect {
			if !item.Signature.Equal(signature) {
				item.Signature = Signature{}
			}
			if owned {
				mem.FreeString(c.engine.Alloc, name)
			}
			return
		}
	}
	c.node.Observations = slices.Append(c.engine.Alloc, c.node.Observations, Observation{Key: key.Clone(c.engine.Alloc), Aspect: aspect, Signature: signature})
	if owned {
		mem.FreeString(c.engine.Alloc, name)
	}
}

// SignatureRecord is the shared equality contract for computation inputs and
// artifacts. Hosts obtain observations; neither host chooses freshness policy.
type SignatureRecord struct {
	Implementation Signature
	// Guard proves that recorded derived values can be reused without evaluation.
	// It is a fast-path condition, not part of semantic result equality.
	Guard          Signature
	Inputs         []Observation
	Outputs        []Observation
}

func sameObservations(left []Observation, right []Observation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Key.Kind < ResourceDefinition || left[i].Key.Kind > ResourceOperation || right[i].Key.Kind < ResourceDefinition || right[i].Key.Kind > ResourceOperation || left[i].Key.Name == "" || right[i].Key.Name == "" {
			return false
		}
		if left[i].Aspect < ObservationValue || left[i].Aspect > ObservationMetadata || right[i].Aspect < ObservationValue || right[i].Aspect > ObservationMetadata {
			return false
		}
		// Persisted records are untrusted sets, not lists with multiplicity.
		for j := 0; j < i; j++ {
			if left[i].Key.Kind == left[j].Key.Kind && left[i].Key.Name == left[j].Key.Name && left[i].Aspect == left[j].Aspect {
				return false
			}
			if right[i].Key.Kind == right[j].Key.Kind && right[i].Key.Name == right[j].Key.Name && right[i].Aspect == right[j].Aspect {
				return false
			}
		}
		found := false
		for j := range right {
			if left[i].Key.Kind == right[j].Key.Kind && left[i].Key.Name == right[j].Key.Name && left[i].Aspect == right[j].Aspect {
				if !left[i].Signature.Equal(right[j].Signature) {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (r *SignatureRecord) Matches(current *SignatureRecord) bool {
	return r.Implementation.Equal(current.Implementation) && sameObservations(r.Inputs, current.Inputs) && sameObservations(r.Outputs, current.Outputs)
}

func (r *SignatureRecord) Free(a mem.Allocator) {
	FreeObservations(a, r.Inputs)
	FreeObservations(a, r.Outputs)
	*r = SignatureRecord{}
}
