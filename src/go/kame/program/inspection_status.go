package program

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type inspectionStatusState struct {
	Stored core.SignatureRecord
	Outputs []core.Signature
	Statuses []string
	Phase int
	Index int
	ToolIndex int
	Pending bool
	Unknown bool
	Changed bool
}

type inspectionStatusResult struct {
	State *inspectionStatusState
	Waiting bool
}

type inspectionStatusReply struct {
	Completion core.Completion
	Waiting bool
}

func (s *inspectionStatusState) free(a mem.Allocator) {
	s.Stored.Free(a)
	slices.Free(a, s.Outputs)
	slices.Free(a, s.Statuses)
	mem.Free(a, s)
}

func (p *Program) inspectArtifactStatus(plan Plan) inspectionStatusResult {
	index := -1
	for i := range p.Instances {
		if p.Instances[i].InspectionStatus != nil && sameInspectionPlan(p.Instances[i].Plan, plan) { index = i; break }
	}
	if index == -1 {
		index = len(p.Instances)
		state := mem.Alloc[instanceState](p.Alloc)
		state.Program, state.Index = p, index
		key := core.NewResourceKey(p.Alloc, core.ResourceTarget, "\x00inspection-status:"+plan.Key.Name)
		node := p.Engine.AddOwned(key, produceInspectionStatus, state, freeInstanceState)
		key.Free(p.Alloc)
		p.Instances = slices.Append(p.Alloc, p.Instances, instance{Rule: plan.Rule, Node: node, Plan: clonePlan(p.Alloc, plan), Captures: cloneCaptures(p.Alloc, plan.Captures), Inspection: true, InspectionStatus: mem.Alloc[inspectionStatusState](p.Alloc)})
		p.Instances[index].inspectionRoot = p.Engine.RequestRoot(node)
	}
	node := p.Instances[index].Node
	for node.State != core.NodeComplete && node.State != core.NodeFailed && node.State != core.NodeCancelled {
		p.Tick(0)
		if p.Forwarding && len(p.Outbound) != 0 { return inspectionStatusResult{Waiting: true} }
	}
	if p.Instances[index].inspectionRoot != nil {
		p.Engine.Release(p.Instances[index].inspectionRoot)
		p.Instances[index].inspectionRoot = nil
	}
	s := p.Instances[index].InspectionStatus
	for len(s.Statuses) < len(plan.Outputs) { s.Statuses = slices.Append(p.Alloc, s.Statuses, "unknown") }
	return inspectionStatusResult{State: p.Instances[index].InspectionStatus}
}

func (p *Program) inspectionStatusRead(c *core.EngineContext, index int, kind host.RequestKind, op, name string) inspectionStatusReply {
	entry := &p.Instances[index]
	s := entry.InspectionStatus
	if p.Forwarding {
		if !s.Pending {
			payload := host.FilePayload(p.Alloc, op, name)
			if kind == host.RequestCacheGet { payload.Free(p.Alloc); payload = host.CacheGetPayload(p.Alloc, entry.FileContextKey[:]) }
			p.submitFileContextRequest(c, kind, payload)
			s.Pending = true
			return inspectionStatusReply{Waiting: true}
		}
		completion := c.Completion()
		if completion.RequestID == 0 { return inspectionStatusReply{Waiting: true} }
		s.Pending = false
		return inspectionStatusReply{Completion: completion}
	}
	if kind == host.RequestCacheGet {
		path := p.fileContextPath(entry)
		data, err := p.Host.ReadFile(p.Alloc, path)
		mem.FreeString(p.Alloc, path)
		value := core.Value{}
		if err == nil { value = core.NewBytes(p.Alloc, data) }
		mem.FreeSlice(p.Alloc, data)
		return inspectionStatusReply{Completion: core.Completion{Value: value, HasValue: err == nil}}
	}
	return inspectionStatusReply{Completion: p.fileCompletion(host.Request{}, op, name)}
}

func inspectionContent(completion core.Completion) core.Signature {
	if completion.Diagnostic.Code != "" || !completion.HasValue { return core.Signature{} }
	if completion.Value.Kind == core.Nil { return core.Signature{Mode: core.SignatureMissing} }
	return contentObservation(completion.Value)
}

// Verify the accepted preflight proof, never fall back to rendering or building.
func produceInspectionStatus(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	owner := c.Context().(*instanceState)
	p, index := owner.Program, owner.Index
	entry := &p.Instances[index]
	s := entry.InspectionStatus
	context := eval.Context{Run: p.Alloc, Cwd: p.Options.Directory, Grants: p.Options.Grants}
	for s.Phase == 0 && s.Index < len(entry.Plan.Outputs) {
		name := p.canonicalTarget(entry.Plan.Outputs[s.Index], true)
		if (!p.Forwarding && p.Host == nil) || !context.Allows(eval.Read, name) {
			s.Outputs = slices.Append(p.Alloc, s.Outputs, core.Signature{})
			s.Unknown = true
			mem.FreeString(p.Alloc, name)
			s.Index++
			continue
		}
		reply := p.inspectionStatusRead(c, index, host.RequestReadFile, host.OpFileContent, name)
		mem.FreeString(p.Alloc, name)
		if reply.Waiting { return core.ProducerSubmitted }
		signature := inspectionContent(reply.Completion)
		s.Outputs = slices.Append(p.Alloc, s.Outputs, signature)
		if signature.Mode == core.SignatureMissing { s.Changed = true }
		if signature.Mode == core.SignatureUnavailable { s.Unknown = true }
		reply.Completion.Value.Free(p.Alloc); reply.Completion.Diagnostic.Free(p.Alloc)
		s.Index++
	}
	if s.Phase == 0 {
		s.Phase, s.Index = 1, 0
		if s.Unknown { s.Phase = 3 } else if !p.claimEnvironment(index, p.Options.Environment) { s.Unknown = true; s.Phase = 3 }
		entry = &p.Instances[index]
		var identity hashSink
		identity.state = newSHA256()
		identity.appendText("kame-file-context-v1")
		name := p.canonicalTarget(entry.Plan.Outputs[0], true)
		identity.appendText(name); mem.FreeString(p.Alloc, name)
		identity.state.Sum(entry.FileContextKey[:])
	}
	if s.Phase == 1 {
		reply := p.inspectionStatusRead(c, index, host.RequestCacheGet, "", "")
		if reply.Waiting { return core.ProducerSubmitted }
		valid := reply.Completion.Diagnostic.Code == "" && reply.Completion.Value.Kind == core.Bytes && core.DecodeSignatureRecord(p.Alloc, reply.Completion.Value.Bytes, &s.Stored)
		reply.Completion.Value.Free(p.Alloc); reply.Completion.Diagnostic.Free(p.Alloc)
		if !valid || !s.Stored.Guard.Equal(p.fileGuard(entry)) { s.Unknown = true; s.Phase = 3 } else { s.Phase = 2 }
		for i := range s.Stored.Inputs { kind := s.Stored.Inputs[i].Key.Kind; if kind == core.ResourceTask || kind == core.ResourceTarget || kind == core.ResourceService { s.Unknown = true; s.Phase = 3 } }
		if valid && len(s.Stored.Outputs) == len(s.Outputs) {
			for i := range s.Outputs { if s.Outputs[i].Mode != core.SignatureUnavailable && !s.Outputs[i].Equal(s.Stored.Outputs[i].Signature) { s.Changed = true } }
		} else if valid { s.Changed = true }
	}
	// Resolve tools through the existing metadata-only observer before checking
	// file facts: a tool's file-value signature is not a hash of its binary bytes.
	for s.Phase == 2 && s.ToolIndex < len(s.Stored.Inputs) {
		item := s.Stored.Inputs[s.ToolIndex]
		if item.Key.Kind == core.ResourceTool {
			p.observeToolDependency(item.Key)
			if !c.TryDependency(item.Key) { return core.ProducerWaiting }
			if c.DependencyDiagnostic(item.Key).Code != "" { s.Unknown = true }
		}
		s.ToolIndex++
	}
	for s.Phase == 2 && s.Index < len(s.Stored.Inputs) {
		item := s.Stored.Inputs[s.Index]
		signature := core.Signature{}
		if item.Key.Kind == core.ResourceDefinition {
			// The identical guard covers definition code/overrides; validate all
			// recorded transitive leaves rather than evaluating recipe definitions.
			signature = item.Signature
		} else if item.Key.Kind == core.ResourceOperation {
			for i := range p.Eval.Registry.Items { operation := p.Eval.Registry.Items[i]; if operation.Name == item.Key.Name { signature = core.ValueSignature(core.Value{Kind: core.String, Text: operation.Version}); break } }
		} else if item.Key.Kind == core.ResourceTool {
			node := p.Engine.Lookup(item.Key)
			if node != nil && node.Current { signature = node.Signature }
		} else if item.Key.Kind == core.ResourceEnvironment {
			if item.Key.Name == eval.ProcessEnvironmentName { signature = eval.ProcessEnvironmentSignature(p.Alloc, entry.Environment) } else {
				value := core.Value{Kind: core.Nil}
				for i := range entry.Environment { assignment := entry.Environment[i]; equal := strings.IndexByte(assignment, '='); if equal >= 0 && assignment[:equal] == item.Key.Name { value = core.Value{Kind: core.String, Text: assignment[equal+1:]}; break } }
				signature = core.ValueSignature(value)
			}
		} else if item.Key.Kind == core.ResourceFile || item.Key.Kind == core.ResourceGlob {
			toolMetadata := false
			if item.Aspect == core.ObservationValue { for i := range p.Tools { if p.Tools[i].Resolved && p.Tools[i].Path == item.Key.Name { toolMetadata = true; break } } }
			if !toolMetadata && !context.Allows(eval.Read, item.Key.Name) { s.Unknown = true; s.Index++; continue }
			op := host.OpFileContent
			if toolMetadata { op = host.OpToolContent }
			if item.Aspect == core.ObservationExistence { op = host.OpExists }
			if item.Aspect == core.ObservationMetadata { op = host.OpStat }
			if item.Key.Kind == core.ResourceGlob { op = host.OpWildcard }
			reply := p.inspectionStatusRead(c, index, host.RequestReadFile, op, item.Key.Name)
			if reply.Waiting { return core.ProducerSubmitted }
			if op == host.OpFileContent || op == host.OpToolContent {
				signature = inspectionContent(reply.Completion)
			} else if reply.Completion.Diagnostic.Code == "" && reply.Completion.HasValue { signature = core.ValueSignature(reply.Completion.Value) }
			reply.Completion.Value.Free(p.Alloc); reply.Completion.Diagnostic.Free(p.Alloc)
		}
		if signature.Mode == core.SignatureUnavailable { s.Unknown = true } else if !signature.Equal(item.Signature) { s.Changed = true }
		s.Index++
	}
	entry.AcceptedRecord.Inputs = s.Stored.Inputs
	material := fileHasMaterialInputs(entry)
	entry.AcceptedRecord.Inputs = nil
	status := "built"
	if s.Unknown { status = "unknown" }
	if s.Changed || entry.Rule.Always || p.Options.Force || !material || p.cacheBlockedByBareTask(entry) { status = "outdated" }
	for i := range s.Outputs {
		current := status
		if s.Outputs[i].Mode == core.SignatureMissing { current = "missing" }
		if s.Outputs[i].Mode == core.SignatureUnavailable { current = "unknown" }
		s.Statuses = slices.Append(p.Alloc, s.Statuses, current)
	}
	return core.ProducerCompleted
}
