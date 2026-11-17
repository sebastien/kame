package eval

import (
	"kame/core"
	"kame/internal/mem"
	"slices"
)

// This is not a valid environment variable name. Only its digest is persisted.
const ProcessEnvironmentName = "@process-environment"

// ProcessEnvironmentSignature ignores assignment order, not child environment values.
func ProcessEnvironmentSignature(a mem.Allocator, environment []string) core.Signature {
	sorted := slices.Clone(environment)
	slices.Sort(sorted)
	values := make([]core.Value, len(sorted))
	for i := range sorted {
		values[i] = core.Value{Kind: core.String, Text: sorted[i]}
	}
	mem.FreeSlice(nil, sorted)
	signature := core.ValueSignature(core.Value{Kind: core.List, List: values})
	mem.FreeSlice(nil, values)
	return signature
}

// processEnvironmentSignature memoizes ProcessEnvironmentSignature for the
// environment slice most recently presented: contexts share that slice and
// never mutate it in place, and every process request observes it.
func (p *Program) processEnvironmentSignature(a mem.Allocator, environment []string) core.Signature {
	if p == nil {
		return ProcessEnvironmentSignature(a, environment)
	}
	if len(environment) != 0 && p.processEnvironmentLen == len(environment) && p.processEnvironmentBase == &environment[0] {
		return p.processEnvironmentValue
	}
	signature := ProcessEnvironmentSignature(a, environment)
	if len(environment) != 0 {
		p.processEnvironmentBase, p.processEnvironmentLen, p.processEnvironmentValue = &environment[0], len(environment), signature
	}
	return signature
}
