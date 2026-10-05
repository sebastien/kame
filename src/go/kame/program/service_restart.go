package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/slices"
)

func (p *Program) prepareServiceRestart(entry *instance) bool {
	_ = p
	if entry == nil || entry.ServiceRestartCount >= entry.Service.RestartAttempts {
		return false
	}
	entry.ServiceRestartCount++
	entry.ServiceRestartPending = true
	return true
}

func (p *Program) waitServiceRestart(c *core.EngineContext, entry *instance) core.ProducerResult {
	p.nextRequest++
	id := p.nextRequest
	entry.ServiceRestartTimerID = id
	c.Submit(id)
	if p.Forwarding {
		payload := host.ServiceTimerPayload(p.Alloc, entry.Service.RestartBackoff)
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: id, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestTimer, Payload: payload})
	} else if p.Host != nil {
		entry.ServiceRestartDeadline = p.Host.Monotonic() + entry.Service.RestartBackoff*1000000
	} else {
		entry.ServiceRestartTimerID, entry.ServiceRestartPending = 0, false
		c.Fail(failure(p.Alloc, "HOST_FAIL", "service restart timer is unavailable"))
		return core.ProducerFailed
	}
	return core.ProducerSubmitted
}

func (p *Program) serviceRestartTimerForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].ServiceRestartTimerID == id {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) tickServiceRestartTimers() {
	if p.Host == nil || p.Forwarding {
		return
	}
	now := p.Host.Monotonic()
	for i := range p.Instances {
		entry := &p.Instances[i]
		if entry.ServiceRestartTimerID == 0 || entry.ServiceRestartDeadline == 0 || now < entry.ServiceRestartDeadline {
			continue
		}
		id := entry.ServiceRestartTimerID
		entry.ServiceRestartTimerID, entry.ServiceRestartDeadline, entry.ServiceRestartPending = 0, 0, false
		p.Engine.Complete(core.Completion{NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, RequestID: id, HasValue: true, Value: core.Value{Kind: core.Nil}})
	}
}
