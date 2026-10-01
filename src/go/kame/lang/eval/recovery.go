package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/mem"
)

type valueRecoveryState struct { Fallback bool }

func freeValueRecoveryState(a mem.Allocator, value any) { mem.Free(a, value.(*valueRecoveryState)) }

func recoverableValue(code string) bool {
	return code == "REF_MISSING" || code == "FS_ERR" || code == "HOST_FAIL" || code == "RECIPE_FAIL" || code == "RECIPE_TIMEOUT" || code == "CAPTURE_LIMIT" || code == "CAPTURE_ENCODING"
}

func (p *Program) recoverValue(scope *Scope, e *expr.Expr, c *Context) Result {
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *valueRecoveryState
	if stored := c.OperationState(); stored != nil { state = stored.(*valueRecoveryState) }
	if state == nil && c.Engine != nil {
		state = mem.Alloc[valueRecoveryState](p.Alloc)
		c.SetOperationState(state, freeValueRecoveryState)
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	if state == nil || !state.Fallback {
		previousRecovery := c.RecoverFailures
		c.RecoverFailures = true
		r := p.evaluate(c.Engine, scope, e.Items[0], c)
		c.RecoverFailures = previousRecovery
		if r.Waiting || (r.Diagnostic.Code != "" && !recoverableValue(r.Diagnostic.Code)) || (r.Diagnostic.Code == "" && r.Value.Kind != core.Nil) { return r }
		r.Free(c.Run)
		if state != nil { state.Fallback = true }
	}
	return p.evaluate(c.Engine, scope, e.Items[1], c)
}
