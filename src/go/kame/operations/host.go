package operations

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

func request(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	if c.Engine == nil && c.Requests == nil {
		// Hostless synchronous evaluators surface the boundary without queueing work.
		payload.Free(c.Run)
		return eval.Result{Waiting: true}
	}
	if c.Engine != nil && c.Program != nil && (kind == host.RequestReadFile || kind == host.RequestEnvironment) {
		return readRequest(c, kind, payload)
	}
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
			return c.InvalidOperation("host completion omitted its result")
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

// Read-only requests are stable within an engine generation. Retain their
// values so replaying an outer expression neither repeats a completed read nor
// lets an earlier call consume a later call's completion.
type readRequestState struct {
	Alloc mem.Allocator
	Kind  host.RequestKind
	Name  string
	Op    string
	ID    int64
	Value core.Value
	Done  bool
}

func freeReadRequestState(a mem.Allocator, value any) {
	_ = a
	state := value.(*readRequestState)
	mem.FreeString(state.Alloc, state.Name)
	mem.FreeString(state.Alloc, state.Op)
	state.Value.Free(state.Alloc)
	mem.Free(state.Alloc, state)
}

func readRequest(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	name, op := host.PayloadPath(payload), host.PayloadText(payload, host.FieldOp)
	if kind == host.RequestEnvironment {
		name = payload.Text
	}
	var state *readRequestState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*readRequestState)
		if state.Kind != kind || state.Name != name || state.Op != op {
			c.ClearOperationState()
			state = nil
		}
	}
	if state == nil {
		state = mem.Alloc[readRequestState](c.Program.Alloc)
		state.Alloc, state.Kind = c.Program.Alloc, kind
		state.Name, state.Op = core.NewString(state.Alloc, name).Text, core.NewString(state.Alloc, op).Text
		c.SetOperationState(state, freeReadRequestState)
	}
	if state.Done {
		payload.Free(c.Run)
		return eval.Result{Value: state.Value.Clone(c.Run)}
	}
	if state.ID != 0 {
		payload.Free(c.Run)
		if c.Completion().RequestID != state.ID {
			return eval.Result{Waiting: true}
		}
		completion := c.TakeCompletion()
		if completion.Diagnostic.Code != "" {
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			completion.Value.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			completion.Value.Free(c.Run)
			return c.InvalidOperation("host completion omitted its result")
		}
		state.Value, state.Done = completion.Value.Clone(state.Alloc), true
		completion.Value.Free(c.Run)
		return eval.Result{Value: state.Value.Clone(c.Run)}
	}
	state.ID = c.Submit(kind, payload)
	payload.Free(c.Run)
	if state.ID == 0 {
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
	name := ""
	if value.Kind == core.Resource && value.Resource.Kind == core.ResourceFile {
		name = value.Resource.Name
	} else if value.Kind == core.String {
		name = value.Text
	} else {
		// value is a copy of the caller's element: releasing the single
		// retain here is balanced, the caller's shallow free never touches it.
		if value.Kind == core.Callable {
			c.FreeCallable(&value)
		}
		return c.InvalidArgument(0, "string or file resource", value.Kind)
	}
	if !c.Allows(eval.Read, name) {
		return failure("CAP_DENIED", "read access denied; grant the required path with --allow-read=ROOT")
	}
	kind := core.ResourceFile
	if op == host.OpWildcard {
		kind = core.ResourceGlob
	}
	if !c.DirectHostRequests && !dependency(c, kind, name) {
		return eval.Result{Waiting: true}
	}
	return request(c, host.RequestReadFile, host.FilePayload(c.Run, op, name))
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
	if len(v) == 1 {
		return fileRequest(c, host.OpWildcard, v[0])
	}
	for i := range v {
		if v[i].Kind != core.String {
			return invalidArgument(c, v, i, "string")
		}
	}
	// Each union member has its own replay identity, so a later suspension
	// neither repeats earlier host reads nor consumes their completions.
	var values []core.Value
	for i := range v {
		previous := c.CallPath
		var digits [32]byte
		index := strconv.Itoa(digits[:], i)
		member := core.NewString(c.Run, previous+"/wildcard:"+index)
		c.CallPath = member.Text
		result := fileRequest(c, host.OpWildcard, v[i])
		c.CallPath = previous
		member.Free(c.Run)
		if result.Waiting || result.Diagnostic.Code != "" {
			freeValues(c, values)
			return result
		}
		for j := range result.Value.List {
			found := false
			for k := range values {
				if values[k].Text == result.Value.List[j].Text {
					found = true
					break
				}
			}
			if !found {
				values = slices.Append(c.Run, values, result.Value.List[j].Clone(c.Run))
			}
		}
		result.Value.Free(c.Run)
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].Text < values[j-1].Text; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	out := core.NewList(c.Run, values)
	freeValues(c, values)
	return eval.Result{Value: out}
}
func opWrite(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	path := ""
	if v[0].Kind == core.Resource && v[0].Resource.Kind == core.ResourceFile {
		path = v[0].Resource.Name
	} else if v[0].Kind == core.String {
		path = v[0].Text
	} else {
		return invalidArgument(c, v, 0, "string or file resource")
	}
	if !c.Allows(eval.Write, path) {
		return failure("CAP_DENIED", "write access denied")
	}
	// Bytes write raw; every other coercible value renders as with str.
	if v[1].Kind == core.Bytes {
		return writeBytes(c, path, v[1].Bytes)
	}
	text, ok := stringValue(c.Run, v[1], false)
	if !ok {
		return invalidArgument(c, v, 1, "bytes or text-coercible value")
	}
	result := writeBytes(c, path, []byte(text))
	mem.FreeString(c.Run, text)
	return result
}
func writeBytes(c *eval.Context, path string, data []byte) eval.Result {
	if c.Phase == eval.RenderingPhase {
		c.EmitWrite(path, data)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "write is invalid while planning")
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return request(c, host.RequestWriteFile, host.WritePayload(c.Run, path, data))
}
func opEnv(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	if !c.Allows(eval.Env, v[0].Text) {
		return failure("CAP_DENIED", "environment access denied")
	}
	if c.HasEnvironment {
		// Target environments already participate in recipe fingerprints. Do not
		// attach a shared ambient node to a target-local value.
		for i := range c.Environment {
			assignment := c.Environment[i]
			equal := strings.IndexByte(assignment, '=')
			if equal >= 0 && assignment[:equal] == v[0].Text {
				return eval.Result{Value: core.NewString(c.Run, assignment[equal+1:])}
			}
		}
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if !c.DirectHostRequests && !dependency(c, core.ResourceEnvironment, v[0].Text) {
		return eval.Result{Waiting: true}
	}
	return request(c, host.RequestEnvironment, core.NewString(c.Run, v[0].Text))
}
func opShell(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if c.Phase != eval.EvaluatePhase {
		c.MarkPhaseInvalid()
		freeArgCallables(c, v)
		return failure("PHASE_INVALID", "shell is invalid outside evaluation")
	}
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	if len(v) == 2 && v[1].Kind != core.Record {
		return invalidArgument(c, v, 1, "record")
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{Value: core.NewString(c.Run, "")}
	}
	if c.HasEnvironment {
		return request(c, host.RequestProcess, host.ScopedProcessPayload(c.Run, v[0].Text, c.Environment))
	}
	return request(c, host.RequestProcess, host.ProcessPayload(c.Run, v[0].Text))
}

// opNow reads the host wall clock as nanoseconds since the Unix epoch. Clocks
// are impure, so they are invalid while planning or resolving a graph; the
// request stays a host operation rather than a cached value.
func opNow(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	_ = v
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "now is invalid while planning")
	}
	return request(c, host.RequestWallTime, core.Value{Kind: core.Nil})
}

// opMonotonic reads the host monotonic clock as nanoseconds from an arbitrary
// origin, for measuring durations without wall-clock jumps.
func opMonotonic(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	_ = v
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "monotonic is invalid while planning")
	}
	return request(c, host.RequestMonotonicTime, core.Value{Kind: core.Nil})
}
