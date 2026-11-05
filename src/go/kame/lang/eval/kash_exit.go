package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"solod.dev/so/slices"
)

func (p *Program) kashExit(scope *Scope, e *expr.Expr, c *Context) Result {
	if !p.ExitBoundary || c.Phase != EvaluatePhase || c.PurePredicate || c.Finalizing {
		return failure(c.Run, "PHASE_INVALID", e.Span, "exit requires an active execution boundary outside finalization")
	}
	if p.DryRun {
		return Result{}
	}
	previous := c.sequencedOperand
	c.sequencedOperand = true
	r := p.evaluate(c.Engine, scope, e.Items[0], c)
	c.sequencedOperand = previous
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.Int || r.Value.Int < 0 || r.Value.Int > 255 {
		r.Free(c.Run)
		return failure(c.Run, "EXPR_INVALID", e.Items[0].Span, "exit status must be an integer from 0 through 255")
	}
	status := int(r.Value.Int)
	r.Free(c.Run)
	// Use the engine's existing unsuppressible propagation channel. Hosts consume
	// the typed intent, not the carrier code, message, or a process-shaped value.
	return Result{Diagnostic: diagnostic.Diagnostic{Code: "CONTROL_EXIT", Protected: true, Exit: diagnostic.Termination{Requested: true, Status: status}}}
}

// FinishExit unwinds one exited invocation. The typed intent survives failures;
// successful termination joins owned work, unsuccessful termination cancels it.
func (p *Program) FinishExit(primary *diagnostic.Diagnostic, deferred *DeferScope, c *Context) Result {
	if primary.ExitOnly() && primary.Exit.Status == 0 {
		joined := p.joinProcesses(c)
		if joined.Waiting {
			return joined
		}
		if joined.Diagnostic.Code != "" {
			exit := primary.Exit
			exit.Status = 1
			primary.Free(p.Alloc)
			*primary = joined.Diagnostic.Clone(p.Alloc)
			primary.Exit, primary.Protected = exit, true
		}
		joined.Free(c.Run)
	}
	aborting := primary.Exit.Status != 0 || !primary.ExitOnly()
	if aborting {
		p.cancelScriptGroup(c.ScriptGroup)
	}
	r := p.finishDefers(deferred, c, aborting)
	if r.Waiting {
		return r
	}
	if r.Diagnostic.Code != "" && primary.ExitOnly() {
		r.Diagnostic.Exit = primary.Exit
		if r.Diagnostic.Exit.Status == 0 {
			r.Diagnostic.Exit.Status = 1
		}
		r.Diagnostic.Protected = true
		return r
	}
	out := primary.Clone(c.Run)
	if r.Diagnostic.Code != "" {
		out.Notes = slices.Append(c.Run, out.Notes, owned(c.Run, "deferred cleanup failed: "+r.Diagnostic.Code+": "+r.Diagnostic.Message))
	}
	r.Free(c.Run)
	return Result{Diagnostic: out}
}
