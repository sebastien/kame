package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
)

// AppendKash parses one interactive Kash entry into the live session and
// queues its statements after the existing work. Each entry owns independent
// source storage and spans; appending never copies earlier entries. Provenance
// travels with retained syntax, not a growing source table. On failure the
// session is unchanged, no work is queued, and diagnostics are entry-relative.
// It returns the index of the first new work item; the caller starts items
// from there through len(s.Work)-1.
func (s *Session) AppendKash(name string, text string) (int, []diagnostic.Diagnostic) {
	p := s.Program
	first := len(s.Work)
	part := script.ParseKashEntry(p.Alloc, name, text)
	captured := p.Eval.CheckInteractiveAssignments(part)
	eval.SetInteractiveSource(part)
	var diagnostics []diagnostic.Diagnostic
	located := func(code string, span source.Span, message string) diagnostic.Diagnostic {
		return diagnostic.Diagnostic{Source: name, SourceText: text, Code: code, Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: span.Start, End: span.End}}
	}
	for _, d := range part.Diagnostics {
		if d.Severity == source.Error {
			diagnostics = append(diagnostics, located(d.Code, d.Span, d.Message))
		}
	}
	var definitions []*definition.Definition
	for _, item := range part.Items {
		switch item.Kind {
		case script.Definition:
			definitions = append(definitions, item.Definition)
		case script.Expression, script.Command, script.Comment:
		default:
			diagnostics = append(diagnostics, located("FEATURE_UNSUP", item.Span, "this declaration is not supported interactively"))
		}
	}
	if len(diagnostics) == 0 {
		diagnostics = p.Eval.RegisterInteractive(definitions, captured)
		for i := range diagnostics {
			diagnostics[i].Source = name
			diagnostics[i].SourceText = text
		}
	}
	if len(diagnostics) != 0 {
		part.Free()
		return first, diagnostics
	}
	for i, item := range part.Items {
		if item.Kind == script.Definition && item.Definition.Eager {
			s.Work = append(s.Work, SessionWork{Expression: item.Definition.Expression, transient: true})
			item.Definition.Expression = nil
		}
		if item.Kind == script.Definition && !item.Definition.Function && !item.Definition.Eager {
			p.Parsed.Items = append(p.Parsed.Items, item)
			part.Items[i].Definition = nil
		}
		if item.Kind == script.Expression || item.Kind == script.Command {
			s.Work = append(s.Work, SessionWork{Expression: item.Expression, Display: item.Kind == script.Expression, transient: true})
			part.Items[i].Expression = nil
		}
	}
	part.Free()
	return first, nil
}

// DiscardWork releases a finished/abandoned entry's queue slots. Started syntax
// is owned by its engine node until retirement; unstarted syntax is freed here.
// Other jobs keep their indices: only empty trailing slots are removed.
func (s *Session) DiscardWork(first, end int) {
	for i := first; i < end; i++ {
		if s.Work[i].transient {
			expr.Free(s.Program.Alloc, s.Work[i].Expression)
		}
		mem.FreeString(s.Program.Alloc, s.Work[i].Target)
		s.Work[i] = SessionWork{}
	}
	for len(s.Work) != 0 {
		last := s.Work[len(s.Work)-1]
		if last.transient || last.Expression != nil || last.Target != "" {
			break
		}
		s.Work = s.Work[:len(s.Work)-1]
	}
}

// PendingNodes lists engine nodes with outstanding host requests, so an
// interactive driver can wait for one cancelled entry without joining work
// started by earlier entries.
func (p *Program) PendingNodes() []int64 {
	nodes := make([]int64, 0, len(p.Pending))
	for i := range p.Pending {
		nodes = append(nodes, p.Pending[i].NodeID)
	}
	return nodes
}

// HasForeignShellCall reports a foreground-only exchange on these roots.
func (p *Program) HasForeignShellCall(roots []*core.Root) bool {
	for _, pending := range p.Pending {
		if pending.ForeignShell && RootsReach(roots, p.Engine.LookupID(pending.NodeID)) {
			return true
		}
	}
	return false
}

// SuspendRoots pauses an interactive job's roots and their host work (052).
func (p *Program) SuspendRoots(roots []*core.Root) {
	for _, r := range roots {
		p.Engine.Suspend(r)
	}
	p.drainSuspensions()
}

// ResumeRoots continues an interactive job's roots. In the foreground, every
// host request of the job also gets back a released terminal handoff.
func (p *Program) ResumeRoots(roots []*core.Root, foreground bool) {
	for _, r := range roots {
		p.Engine.Resume(r)
	}
	p.ResumeForeground = foreground
	p.drainSuspensions()
	p.ResumeForeground = false
	if control, ok := p.Host.(host.ProcessControl); ok && foreground {
		for _, id := range p.RootRequests(roots) {
			control.Resume(id, true)
		}
	}
}

// SignalRoots delivers a signal to every running host request of the roots.
func (p *Program) SignalRoots(roots []*core.Root, signal int) {
	if control, ok := p.Host.(host.ProcessControl); ok {
		for _, id := range p.RootRequests(roots) {
			control.Signal(id, signal)
		}
	}
}

// RootRequests lists the host requests submitted by nodes of the roots' graphs.
func (p *Program) RootRequests(roots []*core.Root) []int64 {
	var ids []int64
	for _, n := range p.Engine.TrackedNodes() {
		if n.Submitted && n.HostRequestID != 0 && RootsReach(roots, n) {
			ids = append(ids, n.HostRequestID)
		}
	}
	return ids
}

// RootsReach reports whether n belongs to one of the roots' graphs.
func RootsReach(roots []*core.Root, n *core.Node) bool {
	for _, r := range roots {
		if r.Reaches(n) {
			return true
		}
	}
	return false
}

// DrainCancellations forwards queued engine cancellations to the host at
// once, for owners that may not tick again soon.
func (p *Program) DrainCancellations() { p.drainCancellations() }
