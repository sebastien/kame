package program

import "kame/diagnostic"

// Cancel releases the runtime's request interest for a target and forwards any
// resulting process cancellation to the host.
func (p *Program) Cancel(target string) diagnostic.Diagnostic {
	resolved := p.instanceFor(target)
	if resolved.Diagnostic.Code != "" {
		return resolved.Diagnostic
	}
	if resolved.Node == nil {
		resolved.Plan.Free(p.Alloc)
		return failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+target)
	}
	p.Engine.Cancel(resolved.Node)
	resolved.Plan.Free(p.Alloc)
	p.drainCancellations()
	return diagnostic.Diagnostic{}
}
