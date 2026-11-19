package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
)

type shortCircuitState struct {
	UseRight   bool
	Attempt    int64
	Effects    []Effect
	WritePaths []string
}

func freeShortCircuitState(a mem.Allocator, value any) {
	state := value.(*shortCircuitState)
	FreeEffects(a, state.Effects)
	for _, path := range state.WritePaths {
		mem.FreeString(a, path)
	}
	mem.FreeSlice(nil, state.WritePaths)
	mem.Free(a, state)
}

func recoverableValue(code string) bool {
	return code == "REF_MISSING" || code == "FS_ERR" || code == "HOST_FAIL" || code == "TOOL_MISSING" || code == "RECIPE_FAIL" || code == "RECIPE_TIMEOUT" || code == "CAPTURE_LIMIT" || code == "CAPTURE_ENCODING" || code == "KASH_RECORD_LIMIT" || code == "KASH_FRAME_ENCODING"
}

func (p *Program) shortCircuitValue(scope *Scope, e *expr.Expr, c *Context) Result {
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *shortCircuitState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*shortCircuitState)
	}
	if state == nil && c.Engine != nil {
		state = mem.Alloc[shortCircuitState](p.Alloc)
		c.SetOperationState(state, freeShortCircuitState)
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	operands := e.Items
	lookupOnly := e.Infix == "??"
	logical := e.Infix == "and" || e.Infix == "or"
	if lookupOnly || logical {
		operands = operands[1:]
	}
	if state == nil || !state.UseRight {
		effects, paths := len(c.Effects), len(c.WritePaths)
		previousRecovery := c.RecoverFailures
		c.RecoverFailures = previousRecovery || !logical
		r := p.evaluate(c.Engine, scope, operands[0], c)
		c.RecoverFailures = previousRecovery
		code := r.Diagnostic.Code
		if r.Waiting || r.Completed || r.Diagnostic.Protected {
			return r
		}
		if logical {
			if code != "" || truthValue(r.Value) == (e.Infix == "or") {
				return r
			}
		} else {
			recoverable := recoverableValue(code)
			if lookupOnly {
				recoverable = code == "REF_MISSING" || code == "TOOL_MISSING" || code == "ENV_VAR_MISSING"
			}
			if (code != "" && !recoverable) || (code == "" && r.Value.Kind != core.Nil) {
				return r
			}
			c.handledFinalizerFailure()
		}
		r.Free(c.Run)
		if state != nil {
			state.UseRight = true
			state.Attempt = c.Engine.Attempt()
			for _, effect := range c.Effects[effects:] {
				state.Effects = append(state.Effects, effect.clone())
			}
			for _, path := range c.WritePaths[paths:] {
				state.WritePaths = append(state.WritePaths, owned(p.Alloc, path))
			}
		}
	} else if state.Attempt != c.Engine.Attempt() {
		state.Attempt = c.Engine.Attempt()
		for _, effect := range state.Effects {
			c.Effects = append(c.Effects, effect.clone())
		}
		for _, path := range state.WritePaths {
			c.WritePaths = append(c.WritePaths, owned(c.Run, path))
		}
	}
	return p.evaluate(c.Engine, scope, operands[1], c)
}
