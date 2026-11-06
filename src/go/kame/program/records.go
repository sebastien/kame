package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/slices"
)

type recordReaderOwner struct {
	ID         int64
	Node       int64
	Generation int64
	Teardown   bool
}

func (p *Program) noteRecordReader(request host.Request) {
	id := host.PayloadInt(request.Payload, "reader")
	for i := range p.RecordReaders {
		if p.RecordReaders[i].ID == id {
			return
		}
	}
	node := p.Engine.LookupID(request.NodeID)
	p.RecordReaders = slices.Append(p.Alloc, p.RecordReaders, recordReaderOwner{ID: id, Node: request.NodeID, Generation: request.Generation, Teardown: node != nil && node.IsTearingDown()})
}

func (p *Program) closeAbandonedRecordReaders() {
	for i := len(p.RecordReaders) - 1; i >= 0; i-- {
		reader := p.RecordReaders[i]
		node := p.Engine.LookupID(reader.Node)
		if node == nil || node.Generation != reader.Generation || (node.IsTearingDown() && !reader.Teardown) || node.State == core.NodeComplete || node.State == core.NodeFailed || node.State == core.NodeCancelled {
			p.CloseRecordReader(reader.ID)
		}
	}
}

func (p *Program) tryRecordRequest(request host.Request) bool {
	name := host.PayloadPath(request.Payload)
	stdin := host.PayloadBool(request.Payload, "stdin")
	code := ""
	inputFD := -1
	if stdin {
		if !p.Options.HasInput || name != "" {
			code = "FEATURE_UNSUP"
		} else {
			inputFD = p.Options.InputFD
		}
	} else if name == "" {
		code = "EXPR_INVALID"
	}
	if p.Host == nil {
		code = "FEATURE_UNSUP"
	}
	result := host.RecordResult{Code: code}
	if code == "" {
		result = p.Host.ReadRecord(p.Alloc, host.PayloadInt(request.Payload, "reader"), name, int(host.PayloadInt(request.Payload, "delimiter")), inputFD)
	}
	if result.Waiting {
		return false
	}
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	if result.Code != "" {
		result.Value.Free(p.Alloc)
		completion.Diagnostic = failure(p.Alloc, result.Code, "cannot read framed record")
	} else {
		completion.Value, completion.HasValue = result.Value, true
	}
	p.Engine.Complete(completion)
	return true
}

func (p *Program) serviceRecordRequests() {
	for i := len(p.RecordRequests) - 1; i >= 0; i-- {
		request := p.RecordRequests[i]
		node := p.Engine.LookupID(request.NodeID)
		live := node != nil && node.Submitted && node.Generation == request.Generation && node.Attempt == request.Attempt && node.HostRequestID == request.ID
		if live && !p.tryRecordRequest(request) {
			continue
		}
		request.Free(p.Alloc)
		copy(p.RecordRequests[i:], p.RecordRequests[i+1:])
		p.RecordRequests = p.RecordRequests[:len(p.RecordRequests)-1]
	}
}

func (p *Program) CloseRecordReader(id int64) {
	// Zero is evaluator shutdown, not a wire reader ID. Cleanup may open new
	// independent readers after this one-time release of body-owned resources.
	if id == 0 {
		for i := len(p.RecordReaders) - 1; i >= 0; i-- {
			p.CloseRecordReader(p.RecordReaders[i].ID)
		}
		return
	}
	for i := len(p.RecordRequests) - 1; i >= 0; i-- {
		request := p.RecordRequests[i]
		if host.PayloadInt(request.Payload, "reader") != id {
			continue
		}
		d := failure(p.Alloc, "EXEC_CANCELLED", "record reader closed during cancellation")
		d.Protected, d.Cancelled = true, true
		p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: d})
		request.Free(p.Alloc)
		copy(p.RecordRequests[i:], p.RecordRequests[i+1:])
		p.RecordRequests = p.RecordRequests[:len(p.RecordRequests)-1]
	}
	for i := range p.RecordReaders {
		if p.RecordReaders[i].ID == id {
			copy(p.RecordReaders[i:], p.RecordReaders[i+1:])
			p.RecordReaders = p.RecordReaders[:len(p.RecordReaders)-1]
			break
		}
	}
	if p.Forwarding {
		p.nextRequest++
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestRecordClose, Payload: host.RecordPayload(p.Alloc, id, "", 0, false)})
	} else if p.Host != nil {
		p.Host.CloseRecord(id)
	}
}
