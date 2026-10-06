package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
)

func (p *Program) verifyForwardOutputs(c *core.EngineContext, index int) core.ProducerResult {
	return p.observeFileOutputs(c, index, true)
}

// Native and forwarded publication validate the same physical bytes. Before a
// cache decision, unavailable outputs cause a miss; after execution, absent or
// unreadable declared outputs prevent publication.
func (p *Program) observeFileOutputs(c *core.EngineContext, index int, executed bool) core.ProducerResult {
	entry := &p.Instances[index]
	if entry.VerifyPending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		entry.VerifyPending = false
		if !p.acceptOutputObservation(c, index, completion, executed) {
			return core.ProducerFailed
		}
	}
	for entry.VerifyIndex < len(entry.Plan.Outputs) {
		name := p.canonicalTarget(entry.Plan.Outputs[entry.VerifyIndex], true)
		if p.Forwarding {
			payload := host.FilePayload(p.Alloc, host.OpOutputContent, name)
			p.submitFileContextRequest(c, host.RequestReadFile, payload)
			mem.FreeString(p.Alloc, name)
			entry.VerifyPending = true
			return core.ProducerSubmitted
		}
		completion := p.fileCompletion(host.Request{}, host.OpFileContent, name)
		mem.FreeString(p.Alloc, name)
		if !p.acceptOutputObservation(c, index, completion, executed) {
			return core.ProducerFailed
		}
	}
	if !executed {
		return p.finishFileContext(c, index)
	}
	p.saveAcceptedFileRecord(entry)
	entry.VerifyOutputs = false
	c.PublishSigned(core.NewString(c.Allocator(), entry.Plan.Outputs[0]), fileResultSignature(&entry.AcceptedRecord))
	return core.ProducerCompleted
}

func (p *Program) acceptOutputObservation(c *core.EngineContext, index int, completion core.Completion, executed bool) bool {
	entry := &p.Instances[index]
	signature := core.Signature{}
	if completion.Diagnostic.Code == "" && completion.HasValue {
		signature = contentObservation(completion.Value)
	}
	completion.Value.Free(p.Alloc)
	if executed && (completion.Diagnostic.Code != "" || signature.Mode != core.SignatureContent) {
		d := completion.Diagnostic
		if d.Code == "" {
			d = failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted readable declared output: "+entry.Plan.Outputs[entry.VerifyIndex])
		}
		p.failRule(c, index, d)
		return false
	}
	completion.Diagnostic.Free(p.Alloc)
	name := p.canonicalTarget(entry.Plan.Outputs[entry.VerifyIndex], true)
	item := core.Observation{Key: core.ResourceKey{Kind: core.ResourceFile, Name: name}, Aspect: core.ObservationContent, Signature: signature}
	entry.AcceptedRecord.Outputs = appendAcceptedObservation(p.Alloc, entry.AcceptedRecord.Outputs, item)
	item.Key.Free(p.Alloc)
	entry.VerifyIndex++
	return true
}

func (p *Program) beginVerifyFileOutputs(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	core.FreeObservations(p.Alloc, entry.AcceptedRecord.Outputs)
	entry.AcceptedRecord.Outputs = nil
	entry.VerifyOutputs, entry.VerifyPending, entry.VerifyIndex = true, false, 0
	return p.verifyForwardOutputs(c, index)
}
