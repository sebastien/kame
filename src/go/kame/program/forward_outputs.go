package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Output verification belongs to the file rule, including recipes containing no
// shell commands. The host supplies existence; the engine decides completion.
func (p *Program) verifyForwardOutputs(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	if entry.VerifyPending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		entry.VerifyPending = false
		if completion.Diagnostic.Code != "" {
			completion.Value.Free(c.Allocator())
			p.failRule(c, index, completion.Diagnostic)
			return core.ProducerFailed
		}
		for i := range entry.Plan.Outputs {
			var timestamp int64
			if completion.Value.Kind != core.List || i >= len(completion.Value.List) || !contextTime(completion.Value.List[i], &timestamp) {
				completion.Value.Free(c.Allocator())
				p.failRule(c, index, failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i]))
				return core.ProducerFailed
			}
		}
		if entry.FileContextWanted {
			var digest [32]byte
			if contextRecord(entry.FileContextDigest[:], completion.Value.List, len(entry.Plan.Outputs), digest[:]) {
				payload := host.CachePutPayload(p.Alloc, entry.FileContextKey[:], digest[:])
				p.nextRequest++
				p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestCachePut, Payload: payload})
			}
		}
		completion.Value.Free(c.Allocator())
		entry.VerifyIndex = len(entry.Plan.Outputs)
	}
	if entry.VerifyIndex < len(entry.Plan.Outputs) {
		var names []string
		for i := range entry.Plan.Outputs {
			names = slices.Append(p.Alloc, names, p.canonicalTarget(entry.Plan.Outputs[i], true))
		}
		p.submitFileTimes(c, names)
		for i := range names {
			mem.FreeString(p.Alloc, names[i])
		}
		slices.Free(p.Alloc, names)
		entry.VerifyPending = true
		return core.ProducerSubmitted
	}
	entry.VerifyOutputs = false
	c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
	return core.ProducerCompleted
}
