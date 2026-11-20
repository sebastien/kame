package program

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
)

// Drive only the requested import graph. Its existing source loader validates
// suffixes, includes, exports and cycles before the constructor can run.
func resolveDeclarationImport(state any, node *core.Node) eval.Result {
	p := state.(*generatedEvaluation).Program
	if p.registrationPending {
		return eval.Result{Waiting: true}
	}
	if p.declarationImportRoots == nil {
		p.declarationImportRoots = make(map[*core.Node]*core.Root)
	}
	if p.declarationImportRoots[node] == nil {
		p.declarationImportRoots[node] = p.Engine.RequestRoot(node)
	}
	for {
		if node.Diagnostic.Code != "" {
			return eval.Result{Diagnostic: node.Diagnostic.Clone(p.Alloc)}
		}
		if node.Current && node.Latest.Kind == core.Module {
			return eval.Result{}
		}
		if next := p.Eval.Requests.Next(); next.OK {
			request := next.Request
			result := p.declarationSource(request)
			result.RequestID, result.NodeID, result.Generation, result.Attempt = request.ID, request.NodeID, request.Generation, request.Attempt
			request.Free(p.Alloc)
			if !result.HasValue && result.Diagnostic.Code == "" {
				p.registrationPending = true
				return eval.Result{Waiting: true}
			}
			p.Engine.Complete(result)
			continue
		}
		if p.Engine.Step() == nil && !p.Eval.Requests.HasRequests() && !p.Engine.HasWork() {
			return eval.Result{Diagnostic: failure(p.Alloc, "HOST_FAIL", "declaration import did not settle")}
		}
	}
}

func (p *Program) declarationSource(request host.Request) core.Completion {
	if request.Kind != host.RequestLoadSource {
		return core.Completion{Diagnostic: failure(p.Alloc, "PHASE_INVALID", "unexpected host request during declaration import")}
	}
	name := request.Payload.Text
	if accepted, ok := p.declarationSourceValues[name]; ok {
		return core.Completion{Value: accepted.Value.Clone(p.Alloc), HasValue: accepted.HasValue, Diagnostic: accepted.Diagnostic.Clone(p.Alloc)}
	}
	var result core.Completion
	if read := p.Options.DeclarationSource; read != nil {
		result = read(p.Options.DeclarationHost, name)
	} else if p.Host != nil && !p.Forwarding {
		result = p.fileCompletion(request, host.OpRead, name)
	} else {
		result.Diagnostic = failure(p.Alloc, "PHASE_INVALID", "declaration import requires a source-loading host")
	}
	if result.HasValue || result.Diagnostic.Code != "" {
		if p.declarationSourceValues == nil {
			p.declarationSourceValues = make(map[string]core.Completion)
		}
		p.declarationSourceValues[name] = core.Completion{Value: result.Value.Clone(p.Alloc), HasValue: result.HasValue, Diagnostic: result.Diagnostic.Clone(p.Alloc)}
		p.declarationSources = append(p.declarationSources, DeclarationSnapshot{
			Key:       core.NewResourceKey(p.Alloc, core.ResourceDeclarationSource, name),
			Directory: cloneText(p.Alloc, p.Options.Directory), Fingerprint: declarationSourceFingerprint(p.Alloc, result),
		})
	}
	return result
}

func declarationSourceFingerprint(a mem.Allocator, result core.Completion) string {
	if result.Diagnostic.Code == "" && result.HasValue {
		switch result.Value.Kind {
		case core.Bytes:
			signature := core.ContentSignature(result.Value.Bytes)
			return cacheHex(a, signature.Digest[:])
		case core.Nil:
			return cloneText(a, "missing")
		}
	}
	return cloneText(a, "unavailable")
}
