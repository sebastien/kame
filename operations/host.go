package operations

import (
	"littlemake/core"
	"littlemake/host"
	"littlemake/lang/eval"
)

func request(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	completion := c.TakeCompletion()
	if completion.RequestID != 0 {
		// Resume does not submit, so the freshly built payload is still owned here.
		payload.Free(c.Run)
		if completion.Diagnostic.Code != "" {
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			return invalid()
		}
		result := eval.Result{Value: completion.Value.Clone(c.Run)}
		completion.Value.Free(c.Run)
		return result
	}
	id := c.Submit(kind, payload)
	payload.Free(c.Run)
	if id == 0 {
		return failure("HOST_FAIL", "host request was not accepted")
	}
	return eval.Result{Waiting: true}
}
func dependency(c *eval.Context, kind core.ResourceKind, name string) bool {
	key := core.NewResourceKey(c.Run, kind, name)
	current := c.Dependency(key)
	key.Free(c.Run)
	return current
}
func fileRequest(c *eval.Context, op string, value core.Value) eval.Result {
	if value.Kind != core.String {
		return invalid()
	}
	if !c.Allows(eval.Read, value.Text) {
		return failure("CAP_DENIED", "read access denied")
	}
	kind := core.ResourceFile
	if op == host.OpWildcard {
		kind = core.ResourceGlob
	}
	if !dependency(c, kind, value.Text) {
		return eval.Result{Waiting: true}
	}
	return request(c, host.RequestReadFile, host.FilePayload(c.Run, op, value.Text))
}
func opRead(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpRead, v[0])
}
func opExists(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpExists, v[0])
}
func opStat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpStat, v[0])
}
func opWildcard(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpWildcard, v[0])
}
func opWrite(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String || (v[1].Kind != core.String && v[1].Kind != core.Bytes) {
		return invalid()
	}
	if !c.Allows(eval.Write, v[0].Text) {
		return failure("CAP_DENIED", "write access denied")
	}
	data := v[1].Bytes
	if v[1].Kind == core.String {
		data = []byte(v[1].Text)
	}
	if c.Phase == eval.RenderingPhase {
		c.EmitWrite(v[0].Text, data)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if c.Phase == eval.PlanningPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "write is invalid while planning")
	}
	return request(c, host.RequestWriteFile, host.WritePayload(c.Run, v[0].Text, data))
}
func opEnv(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String {
		return invalid()
	}
	if !c.Allows(eval.Env, v[0].Text) {
		return failure("CAP_DENIED", "environment access denied")
	}
	if !dependency(c, core.ResourceEnvironment, v[0].Text) {
		return eval.Result{Waiting: true}
	}
	return request(c, host.RequestEnvironment, core.NewString(c.Run, v[0].Text))
}
func opShell(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if c.Phase != eval.EvaluatePhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "shell is invalid outside evaluation")
	}
	if v[0].Kind != core.String || (len(v) == 2 && v[1].Kind != core.Record) {
		return invalid()
	}
	return request(c, host.RequestProcess, host.ProcessPayload(c.Run, v[0].Text))
}
