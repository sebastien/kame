package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
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
		exists := completion.HasValue && completion.Value.Kind == core.Bool && completion.Value.Bool
		completion.Value.Free(c.Allocator())
		if completion.Diagnostic.Code != "" {
			p.failRule(c, index, completion.Diagnostic)
			return core.ProducerFailed
		}
		if !exists {
			p.failRule(c, index, failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[entry.VerifyIndex]))
			return core.ProducerFailed
		}
		entry.VerifyIndex++
	}
	if entry.VerifyIndex < len(entry.Plan.Outputs) {
		name := p.canonicalTarget(entry.Plan.Outputs[entry.VerifyIndex], true)
		payload := host.FilePayload(c.Allocator(), host.OpOutputExists, name)
		mem.FreeString(p.Alloc, name)
		id := p.Eval.Requests.Submit(c.NodeID(), c.Generation(), c.Attempt(), host.RequestReadFile, payload)
		payload.Free(c.Allocator())
		entry.VerifyPending = true
		c.Submit(id)
		return core.ProducerSubmitted
	}
	entry.VerifyOutputs = false
	c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
	return core.ProducerCompleted
}
