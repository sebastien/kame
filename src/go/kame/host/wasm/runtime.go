package wasm

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
)

// Runtime is the portable, single-owner state machine used by the wasm ABI.
// It deliberately neither performs host work nor creates a thread.
type Runtime struct {
	Alloc      mem.Allocator
	Arena      *mem.Arena
	Engine     *core.Engine
	Eval       *eval.Program
	Parsed     *script.Script
	Registry   *eval.Registry
	Node       *core.Node
	Expr       *expr.Expr
	ExprParsed *script.Script
}

type runtimeState struct {
	Runtime *Runtime
}

type RuntimeResult struct {
	Value      core.Value
	Diagnostic core.Diagnostic
	Done       bool
}

type RuntimeStart struct {
	Runtime *Runtime
	Result  PureResult
}

func (r *RuntimeResult) Free(a mem.Allocator) {
	r.Value.Free(a)
	r.Diagnostic.Free(a)
	*r = RuntimeResult{}
}

// NewRuntime compiles source without performing filesystem or process work.
func NewRuntime(a mem.Allocator, source string) RuntimeStart {
	return newRuntime(a, nil, source)
}

// NewRuntimeIn creates an instance whose logical lifetime is bounded by buf.
// ABI callers supply one fixed arena per instance and Runtime.Free resets it.
func NewRuntimeIn(buf []byte, source string) RuntimeStart {
	arena := mem.Alloc[mem.Arena](mem.System)
	*arena = mem.NewArena(buf)
	result := newRuntime(arena, arena, source)
	if result.Runtime == nil {
		arena.Reset()
	}
	return result
}

func newRuntime(a mem.Allocator, arena *mem.Arena, source string) RuntimeStart {
	parsed := script.Parse(a, "<wasm-source>", source)
	if len(parsed.Diagnostics) != 0 {
		parsed.Free()
		return RuntimeStart{Result: PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "source contains invalid syntax")}}
	}
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	engine := core.NewEngine(a)
	compiled := eval.CompileChecked(a, engine, parsed, registry)
	if compiled.Program == nil {
		compiled.Free(a)
		engine.Free()
		registry.Free()
		parsed.Free()
		return RuntimeStart{Result: PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "source cannot be compiled")}}
	}
	program := compiled.Program
	compiled.Free(a)
	program.SetGrants([]eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}})
	program.DirectHostRequests = true
	runtime := mem.Alloc[Runtime](a)
	runtime.Alloc, runtime.Arena, runtime.Engine, runtime.Eval, runtime.Parsed, runtime.Registry = a, arena, engine, program, parsed, registry
	return RuntimeStart{Runtime: runtime}
}

// RequestExpression schedules one expression. Only one root is active in this
// initial runner; callers must observe its terminal result before replacing it.
func (r *Runtime) RequestExpression(text string) PureResult {
	if r == nil || r.Node != nil {
		return PureResult{Code: pureText(mem.System, "PHASE_INVALID"), Message: pureText(mem.System, "runtime already has an active expression")}
	}
	parsed := script.Parse(r.Alloc, "<wasm-expr>", text)
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 || parsed.Items[0].Expression == nil {
		parsed.Free()
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "expected one valid expression")}
	}
	r.Expr, r.ExprParsed = parsed.Items[0].Expression, parsed
	state := mem.Alloc[runtimeState](r.Alloc)
	state.Runtime = r
	key := core.NewResourceKey(r.Alloc, core.ResourceTarget, "<wasm-expr>")
	r.Node = r.Engine.AddOwned(key, runExpression, state, freeRuntimeState)
	key.Free(r.Alloc)
	if r.Node == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "cannot schedule expression")}
	}
	r.Engine.Request(r.Node)
	return PureResult{}
}

func freeRuntimeState(a mem.Allocator, value any) { mem.Free(a, value.(*runtimeState)) }

func runExpression(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*runtimeState)
	r := state.Runtime
	grants := []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}}
	context := &eval.Context{Program: r.Eval, Engine: c, Scope: r.Eval.Scope, Run: c.Allocator(), Grants: grants, DirectHostRequests: true}
	result := r.Eval.EvaluateWith(r.Expr, context)
	if result.Waiting {
		result.Free(c.Allocator())
		if c.Submitted() {
			return core.ProducerSubmitted
		}
		return core.ProducerWaiting
	}
	if result.Diagnostic.Code != "" {
		c.Fail(result.Diagnostic)
		result.Diagnostic = diagnostic.Diagnostic{}
		result.Free(c.Allocator())
		return core.ProducerFailed
	}
	c.Publish(result.Value)
	result.Value = core.Value{}
	result.Free(c.Allocator())
	return core.ProducerCompleted
}

// Step advances at most one engine action and transfers one host request to
// its caller. A nil request means that no host work is currently required.
func (r *Runtime) Step() host.NextResult {
	if r == nil || r.Engine == nil {
		return host.NextResult{}
	}
	r.Engine.Step()
	return r.Eval.Requests.Next()
}

// Complete copies host completion data into the engine. Stale generations and
// attempts are accepted and discarded by Engine.Step according to core rules.
func (r *Runtime) Complete(request host.Request, value core.Value, diagnostic diagnostic.Diagnostic) {
	if r == nil || r.Engine == nil {
		value.Free(mem.System)
		diagnostic.Free(mem.System)
		return
	}
	r.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Value: value, HasValue: diagnostic.Code == "", Diagnostic: diagnostic})
}

// Cancel releases the expression root's request interest. The engine records
// any outstanding host request as cancelled and ignores a later completion.
func (r *Runtime) Cancel() bool {
	if r == nil || r.Engine == nil || r.Node == nil {
		return false
	}
	r.Engine.Cancel(r.Node)
	return true
}

func (r *Runtime) Result() RuntimeResult {
	if r == nil || r.Node == nil {
		return RuntimeResult{}
	}
	if r.Node.State == core.NodeComplete {
		return RuntimeResult{Value: r.Node.Latest.Clone(r.Alloc), Done: true}
	}
	if r.Node.State == core.NodeFailed || r.Node.State == core.NodeCancelled {
		return RuntimeResult{Diagnostic: r.Node.Diagnostic.Clone(r.Alloc), Done: true}
	}
	return RuntimeResult{}
}

func (r *Runtime) Free() {
	if r == nil {
		return
	}
	if r.ExprParsed != nil {
		r.ExprParsed.Free()
	}
	r.Eval.Free()
	r.Engine.Free()
	r.Registry.Free()
	r.Parsed.Free()
	if r.Arena != nil {
		r.Arena.Reset()
		return
	}
	a := r.Alloc
	*r = Runtime{}
	mem.Free(a, r)
}
