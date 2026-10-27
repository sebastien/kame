package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
)

func (p *Program) nativeKash(e *expr.Expr, scope *Scope, c *Context) Result {
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	r := p.invokeKashBody(nil, e, scope, c.Args, c)
	c.operationStart, c.operationEnd = start, end
	return r
}

func (p *Program) kashReturn(scope *Scope, e *expr.Expr, c *Context) Result {
	r := p.evaluate(c.Engine, scope, e.Items[0], c)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.Int || r.Value.Int < 0 || r.Value.Int > 255 {
		r.Free(c.Run)
		return failure(c.Run, "EXPR_INVALID", e.Items[0].Span, "return status must be an integer from 0 through 255")
	}
	status := r.Value.Int
	r.Free(c.Run)
	fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: status}}}
	r = processResult(c.Run, core.Value{Kind: core.Record, Record: fields}, e.Span)
	r.Returned = true
	if status != 0 && !e.AcceptExit {
		failed := failure(c.Run, "RECIPE_FAIL", e.Span, "function returned an unsuccessful status")
		failed.Diagnostic.Cause = diagnostic.Cause{Kind: owned(c.Run, "process"), Message: owned(c.Run, "function returned"), Status: int(status), HasStatus: true}
		r.Diagnostic = failed.Diagnostic
	}
	return r
}
