package wasm

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Runtime is the portable, single-owner state machine used by the wasm ABI.
// It deliberately neither performs host work nor creates a thread.
type Runtime struct {
	Alloc      mem.Allocator
	Arena      *Heap
	Engine     *core.Engine
	Eval       *eval.Program
	Parsed     *script.Script
	Registry   *eval.Registry
	Node       *core.Node
	Expr       *expr.Expr
	ExprParsed *script.Script
	// Target mode uses the portable build runtime with an in-memory host. It is
	// mutually exclusive with the single-expression mode above.
	Host               *MemoryHost
	Program            *program.Program
	Session            *program.Session
	BuildSources       []program.CompileSource
	BuildDefines       []string
	BuildParameters    []string
	BuildToolOverrides []string
	BuildEnvironment   []string
	BuildForce         bool
	BuildTimeoutMS     int64
	BuildRetryCount    int
	BuildRetainBytes   int
	Handle             *program.Handle
	Watching           bool
	WatchHandles       []*program.Handle
	Environment        []string
	ToolPaths          []program.Tool
	InspectionGrants   []eval.Grant
	InspectionPolicy   bool
	Forwarding         bool
	EventJSON          []byte
	Effects            []eval.Effect
	EffectIndex        int
	// Directory is the working directory the build runtime canonicalizes
	// against. It defaults to "." and the host may set the absolute cwd.
	Directory string
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
	return newRuntime(a, nil, "<wasm-source>", source)
}

// NewRuntimeIn creates an instance whose logical lifetime is bounded by buf.
// ABI callers supply one fixed arena per instance and Runtime.Free resets it.
// name labels the source in diagnostics; an empty name keeps the default.
func NewRuntimeIn(buf []byte, name string, source string) RuntimeStart {
	arena := mem.Alloc[Heap](mem.System)
	*arena = NewHeap(buf)
	result := newRuntime(arena, arena, name, source)
	if result.Runtime == nil {
		arena.Reset()
	}
	return result
}

// NewRuntimeWithHeap borrows a host-owned allocator descriptor. The descriptor
// stays outside the logical heap so an ABI allocation-failure boundary can
// discard a partially constructed runtime without traversing its objects.
func NewRuntimeWithHeap(arena *Heap, name string, source string) RuntimeStart {
	arena.Reset()
	return newRuntime(arena, arena, name, source)
}

func newRuntime(a mem.Allocator, arena *Heap, name string, source string) RuntimeStart {
	if name == "" {
		name = "<wasm-source>"
	}
	parsed := script.Parse(a, name, source)
	if len(parsed.Diagnostics) != 0 {
		return RuntimeStart{Result: parseFailure(a, parsed)}
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
	evalProgram := compiled.Program
	compiled.Free(a)
	evalProgram.SetGrants([]eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}})
	evalProgram.DirectHostRequests = true
	runtime := mem.Alloc[Runtime](a)
	runtime.Alloc, runtime.Arena, runtime.Engine, runtime.Eval, runtime.Parsed, runtime.Registry = a, arena, engine, evalProgram, parsed, registry
	runtime.Host = NewMemoryHost(a)
	runtime.Directory = "."
	runtime.Environment = nil
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

// Prepare compiles the instance source into a build runtime without starting a
// root, for planning and inspection. It is mutually exclusive with the
// expression and target roots.
func (r *Runtime) Prepare() PureResult {
	if r == nil || r.Program != nil || r.Node != nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "runtime already has an active root")}
	}
	if r.Host == nil {
		r.Host = NewMemoryHost(r.Alloc)
	}
	grants := []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}}
	options := program.Options{Host: r.Host, Directory: r.Directory, Jobs: 1, Environment: r.Environment, Grants: grants, ForwardRequests: r.Forwarding}
	if r.InspectionPolicy {
		options.Grants = r.InspectionGrants
	}
	compiled := r.compileBuild(options)
	if compiled.Program == nil {
		r.Host = nil
		out := r.buildCompileFailure(&compiled)
		compiled.Free(r.Alloc)
		return out
	}
	r.Host = nil
	r.Program = compiled.Program
	r.Program.Eval.SetDefinitionEffectSink(program.DefinitionEventEffect, r.Program)
	compiled.Free(r.Alloc)
	r.applyToolPaths()
	return PureResult{}
}

// InspectionGrant configures the same capability policy used by the native
// driver. An empty capability clears defaults; names are optional scope roots.
func (r *Runtime) InspectionGrant(capability string, name string) bool {
	if r == nil || r.Program != nil {
		return false
	}
	if capability == "" {
		for i := range r.InspectionGrants {
			for j := range r.InspectionGrants[i].Names {
				mem.FreeString(r.Alloc, r.InspectionGrants[i].Names[j])
			}
			slices.Free(r.Alloc, r.InspectionGrants[i].Names)
		}
		slices.Free(r.Alloc, r.InspectionGrants)
		r.InspectionGrants = nil
		r.InspectionPolicy = true
		if r.Eval != nil { r.Eval.SetGrants(nil) }
		return true
	}
	grant := eval.Grant{}
	switch capability {
	case "read":
		grant.Capability = eval.Read
	case "write":
		grant.Capability = eval.Write
	case "run":
		grant.Capability = eval.Run
	case "env":
		grant.Capability = eval.Env
	default:
		return false
	}
	if name != "" {
		grant.Names = slices.Append(r.Alloc, grant.Names, pureText(r.Alloc, name))
	}
	r.InspectionGrants = slices.Append(r.Alloc, r.InspectionGrants, grant)
	r.InspectionPolicy = true
	if r.Eval != nil { r.Eval.SetGrants(r.InspectionGrants) }
	return true
}

// SetToolPath supplies host-resolved paths without treating unused missing tools
// as compilation failures. Paths may be supplied before target compilation.
func (r *Runtime) SetToolPath(name string, path string) bool {
	if r == nil || name == "" {
		return false
	}
	if r.Program != nil {
		return r.Program.SetToolPath(name, path)
	}
	for i := range r.ToolPaths {
		if r.ToolPaths[i].Name == name {
			mem.FreeString(r.Alloc, r.ToolPaths[i].Path)
			r.ToolPaths[i].Path = pureText(r.Alloc, path)
			return true
		}
	}
	r.ToolPaths = slices.Append(r.Alloc, r.ToolPaths, program.Tool{Name: pureText(r.Alloc, name), Path: pureText(r.Alloc, path)})
	return true
}

func (r *Runtime) applyToolPaths() {
	for i := range r.ToolPaths {
		r.Program.SetToolPath(r.ToolPaths[i].Name, r.ToolPaths[i].Path)
	}
}

// ToolsCheckJSON returns schema-1 diagnostic events, or empty text on success.
// The dependency traversal is shared with the native CLI and never runs recipes.
func (r *Runtime) ToolsCheckJSON(target string) PureResult {
	return r.toolsCheckJSON(target, false)
}

// ToolsCheckReportJSON adds the CLI result without changing the legacy query.
func (r *Runtime) ToolsCheckReportJSON(target string) PureResult {
	return r.toolsCheckJSON(target, true)
}

func (r *Runtime) toolsCheckJSON(target string, report bool) PureResult {
	if r == nil || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no compiled build source")}
	}
	result := r.Program.RequiredTools(target)
	if result.Waiting {
		result.Free(r.Alloc)
		return PureResult{HostNeeded: true}
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	if result.Diagnostic.Code != "" {
		program.WriteJSONDiagnostic(&buffer, result.Diagnostic)
	} else {
		for i := range result.Uses {
			use := result.Uses[i]
			if r.Program.ResolveTool(use.Name) != "" {
				continue
			}
			d := diagnostic.Diagnostic{Code: "TOOL_MISSING", Severity: diagnostic.Error, Message: "required tool not found or not executable: " + use.Name, Source: use.Source, Span: use.Span, Target: use.Target, TargetStack: use.TargetStack, Tips: []string{"install the tool or add its executable directory to PATH"}}
			program.WriteJSONDiagnostic(&buffer, d)
		}
	}
	if report { cli.WriteToolsCheckResult(&buffer, r.Program, target, result.Uses) }
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	result.Free(r.Alloc)
	return PureResult{Text: text}
}

// PlanJSON resolves one target plan (optionally expanding expression inputs)
// and returns its schema-1 JSON document.
func (r *Runtime) PlanJSON(target string, expand bool) PureResult {
	if r == nil || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no compiled build source")}
	}
	var result program.PlanResult
	if expand {
		result = r.Program.ExpandPlan(target)
	} else {
		result = r.Program.Plan(target)
	}
	if result.Diagnostic.Code != "" {
		out := PureResult{Code: pureText(r.Alloc, result.Diagnostic.Code), Message: pureText(r.Alloc, result.Diagnostic.Message)}
		result.Diagnostic.Free(r.Alloc)
		return out
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	program.WritePlan(&buffer, &result.Plan)
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	result.Plan.Free(r.Alloc)
	return PureResult{Text: text}
}

// GraphJSON walks one target's declared inputs or outputs and returns the JSON
// array. kind is "inputs" or "outputs".
func (r *Runtime) GraphJSON(target string, depth int, kind string) PureResult {
	if r == nil || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no compiled build source")}
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	d := r.Program.WriteGraph(&buffer, target, depth, kind)
	if d.Code != "" {
		out := PureResult{Code: pureText(r.Alloc, d.Code), Message: pureText(r.Alloc, d.Message)}
		d.Free(r.Alloc)
		buffer.Free()
		return out
	}
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	return PureResult{Text: text}
}

// SpanJSON walks one target's static (and optionally expanded) edges and
// returns the span JSON document.
func (r *Runtime) SpanJSON(target string, depth int, expand bool) PureResult {
	if r == nil || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no compiled build source")}
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	d := r.Program.WriteSpan(&buffer, target, depth, expand)
	if r.Program.InspectionWaiting {
		buffer.Free()
		return PureResult{HostNeeded: true}
	}
	if d.Code != "" {
		out := PureResult{Code: pureText(r.Alloc, d.Code), Message: pureText(r.Alloc, d.Message)}
		d.Free(r.Alloc)
		buffer.Free()
		return out
	}
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	return PureResult{Text: text}
}

// ToolNames returns the declared build tool names as a JSON array. Path
// resolution belongs to the embedding host, which knows its own PATH.
func (r *Runtime) ToolNames() PureResult {
	if r == nil || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no compiled build source")}
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	e := json.NewEncoder(&buffer)
	e.BeginArray()
	for i := range r.Program.Tools {
		e.Str(r.Program.Tools[i].Name)
	}
	e.EndArray()
	e.Flush()
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	return PureResult{Text: text}
}

// SetFile supplies one in-memory file for target materialization. It must be
// called before RequestTarget.
func (r *Runtime) SetFile(path string, data []byte) bool {
	if r == nil || r.Program != nil {
		return false
	}
	if r.Host == nil {
		r.Host = NewMemoryHost(r.Alloc)
	}
	r.Host.SetFile(path, data)
	return true
}

// SetDirectory sets the working directory the build runtime canonicalizes
// against. It must be called before Prepare, RequestTarget, or a plan request.
func (r *Runtime) SetDirectory(directory string) bool {
	if r == nil || r.Program != nil {
		return false
	}
	r.Directory = pureText(r.Alloc, directory)
	return true
}

// SetForwarding routes target host requests to the embedding host instead of
// the in-memory filesystem. It must be set before RequestTarget.
func (r *Runtime) SetForwarding(enabled bool) bool {
	if r == nil || r.Program != nil {
		return false
	}
	r.Forwarding = enabled
	return true
}

// SetEnvironment adds one NAME=value pair for target evaluation and fingerprinting.
func (r *Runtime) SetEnvironment(name string, value string) bool {
	if r == nil || r.Program != nil {
		return false
	}
	r.Environment = slices.Append(r.Alloc, r.Environment, pureText(r.Alloc, name+"="+value))
	return true
}

// RequestTarget schedules one target through the portable build runtime and its
// in-memory host. Only one root, expression or target, is active at a time.
func (r *Runtime) RequestTarget(target string) PureResult {
	if r == nil || r.Node != nil || r.Program != nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "runtime already has an active root")}
	}
	if r.Host == nil {
		r.Host = NewMemoryHost(r.Alloc)
	}
	grants := []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}}
	options := program.Options{Host: r.Host, Directory: r.Directory, Jobs: 1, Environment: r.Environment, Grants: grants, ForwardRequests: r.Forwarding}
	if r.InspectionPolicy {
		options.Grants = r.InspectionGrants
	}
	compiled := r.compileBuild(options)
	if compiled.Program == nil {
		// Compile released the host on failure, so drop the borrowed pointer too.
		r.Host = nil
		out := r.buildCompileFailure(&compiled)
		compiled.Free(r.Alloc)
		return out
	}
	// The Program now owns the host and releases it during Program.Free.
	r.Host = nil
	r.Program = compiled.Program
	compiled.Free(r.Alloc)
	r.applyToolPaths()
	started := r.Program.Start(target)
	if started.Diagnostic.Code != "" {
		out := PureResult{Code: pureText(r.Alloc, started.Diagnostic.Code), Message: pureText(r.Alloc, started.Diagnostic.Message)}
		started.Diagnostic.Free(r.Alloc)
		return out
	}
	r.Handle = started.Handle
	return PureResult{}
}

// TargetResultKind reports the completed target's shape: 0 for none, 1 for a
// definition value, and 2 for a file artifact path. Callers use it to decide
// whether to print the value or read the artifact.
func (r *Runtime) TargetResultKind() uint32 {
	if r == nil || r.Program == nil || r.Handle == nil {
		return 0
	}
	if r.Handle.Definition && r.Handle.Node != nil && r.Handle.Node.Current {
		return 1
	}
	poll := r.Handle.Poll()
	if !poll.Done {
		return 0
	}
	result := poll.Result
	kind := uint32(0)
	if result.Diagnostic.Code == "" {
		if result.Path != "" {
			kind = 2
		} else if result.Value.Kind != core.Nil {
			kind = 1
		}
	}
	result.Free(r.Alloc)
	return kind
}

// NextEventJSON pops one queued target event and pins its schema-1 JSON line
// until the host copies or clears it, so a length query and the copy observe
// the same event. It reports false when no event is queued.
func (r *Runtime) NextEventJSON() bool {
	if r == nil || r.Program == nil {
		return false
	}
	r.EventJSONClear()
	next := r.Program.NextEvent()
	if !next.OK {
		return false
	}
	var buffer bytes.Buffer = bytes.NewBuffer(r.Alloc, nil)
	program.WriteJSONEventWithAllocator(r.Alloc, &buffer, next.Event)
	next.Event.Free(r.Alloc)
	r.EventJSON = slices.Clone(r.Alloc, []byte(buffer.String()))
	buffer.Free()
	return true
}

func (r *Runtime) EventJSONLength() int {
	if r == nil {
		return 0
	}
	return len(r.EventJSON)
}

func (r *Runtime) EventJSONCopy(dst []byte) bool {
	if r == nil || len(dst) < len(r.EventJSON) {
		return false
	}
	for i := range r.EventJSON {
		dst[i] = r.EventJSON[i]
	}
	r.EventJSONClear()
	return true
}

func (r *Runtime) EventJSONClear() {
	if r == nil || len(r.EventJSON) == 0 {
		return
	}
	slices.Free(r.Alloc, r.EventJSON)
	r.EventJSON = nil
}

// ProcessStarted, ProcessStream, and ProcessTerminal let the embedding host
// report a forwarded process's progress and completion.
func (r *Runtime) ProcessStarted(request host.Request) {
	if r != nil && r.Program != nil {
		r.Program.ProcessStarted(request)
	}
}

func (r *Runtime) ProcessExited(request host.Request) {
	if r != nil && r.Program != nil {
		r.Program.ProcessExited(request)
	}
}

func (r *Runtime) ProcessStream(request host.Request, stderr bool, data []byte) {
	if r != nil && r.Program != nil {
		r.Program.ProcessStream(request, stderr, data)
	}
}

func (r *Runtime) ProcessRetainLimit(request host.Request) int {
	if r.Program != nil {
		return r.Program.ProcessRetainLimit(request)
	}
	return 64 * 1024
}

func (r *Runtime) ProcessTerminal(request host.Request, stdout []byte, stderr []byte, status int, signal int, outcome int, code string, message string) {
	if r != nil && r.Program != nil {
		r.Program.ProcessTerminal(request, stdout, stderr, status, signal, outcome, code, message)
	}
}

func freeRuntimeState(a mem.Allocator, value any) { mem.Free(a, value.(*runtimeState)) }

func runExpression(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*runtimeState)
	r := state.Runtime
	grants := []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}}
	if r.InspectionPolicy { grants = r.InspectionGrants }
	context := &eval.Context{Program: r.Eval, Engine: c, Scope: r.Eval.Scope, Run: c.Allocator(), Cwd: r.Directory, Grants: grants, DirectHostRequests: true}
	result := r.Eval.EvaluateWith(r.Expr, context)
	if result.Waiting {
		eval.FreeEffects(c.Allocator(), context.Effects)
		result.Free(c.Allocator())
		if c.Submitted() {
			return core.ProducerSubmitted
		}
		return core.ProducerWaiting
	}
	if result.Diagnostic.Code != "" {
		eval.FreeEffects(c.Allocator(), context.Effects)
		c.Fail(result.Diagnostic)
		result.Diagnostic = diagnostic.Diagnostic{}
		result.Free(c.Allocator())
		return core.ProducerFailed
	}
	r.Effects = context.Effects
	context.Effects = nil
	c.Publish(result.Value)
	result.Value = core.Value{}
	result.Free(c.Allocator())
	return core.ProducerCompleted
}

// ExpressionEffectKind returns the next standalone expression effect: 1 out,
// 2 err, 3 yield, or 0 when the completed expression has none left.
func (r *Runtime) ExpressionEffectKind() uint32 {
	if r != nil {
		for r.EffectIndex < len(r.Effects) && r.Effects[r.EffectIndex].Kind == eval.EffectProcessWrite {
			r.Effects[r.EffectIndex].Free(r.Alloc)
			r.EffectIndex++
		}
	}
	if r == nil || r.EffectIndex >= len(r.Effects) {
		return 0
	}
	kind := r.Effects[r.EffectIndex].Kind
	if kind == eval.EffectOut {
		return 1
	}
	if kind == eval.EffectErr {
		return 2
	}
	if kind == eval.EffectYield {
		return 3
	}
	return 0
}

func (r *Runtime) ExpressionEffectLength() int {
	if r == nil || r.EffectIndex >= len(r.Effects) {
		return 0
	}
	return len(r.Effects[r.EffectIndex].Data)
}

// CopyExpressionEffect copies and releases the next expression effect.
func (r *Runtime) CopyExpressionEffect(dst []byte) bool {
	if r == nil || r.EffectIndex >= len(r.Effects) || len(dst) < len(r.Effects[r.EffectIndex].Data) {
		return false
	}
	effect := &r.Effects[r.EffectIndex]
	copy(dst, effect.Data)
	effect.Free(r.Alloc)
	r.EffectIndex++
	return true
}

// Step advances at most one engine action and transfers one host request to
// its caller. A nil request means that no host work is currently required.
func (r *Runtime) Step() host.NextResult {
	if r == nil {
		return host.NextResult{}
	}
	if r.Program != nil {
		if r.Handle != nil {
			r.Program.Tick(0)
		}
		if outbound := r.Program.NextOutbound(); outbound.OK {
			return outbound
		}
		return host.NextResult{}
	}
	if r.Engine == nil {
		return host.NextResult{}
	}
	r.Engine.Step()
	return r.Eval.Requests.Next()
}

// Complete copies host completion data into the engine. Stale generations and
// attempts are accepted and discarded by Engine.Step according to core rules.
func (r *Runtime) Complete(request host.Request, value core.Value, diagnostic diagnostic.Diagnostic) {
	if r == nil {
		value.Free(mem.System)
		diagnostic.Free(mem.System)
		return
	}
	if r.Program != nil {
		r.Program.Complete(request, value, diagnostic)
		return
	}
	if r.Engine == nil {
		value.Free(mem.System)
		diagnostic.Free(mem.System)
		return
	}
	r.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Value: value, HasValue: diagnostic.Code == "", Diagnostic: diagnostic})
}

// Cancel releases the expression root's request interest. The engine records
// any outstanding host request as cancelled and ignores a later completion.
func (r *Runtime) Cancel() bool {
	if r != nil && r.Program != nil && r.Handle != nil {
		for i := range r.WatchHandles {
			r.Program.Engine.Cancel(r.WatchHandles[i].Node)
			r.WatchHandles[i].Cancel()
		}
		r.Program.Engine.Cancel(r.Handle.Node)
		r.Handle.Cancel()
		r.Program.Eval.CancelProcesses()
		return true
	}
	if r == nil || r.Engine == nil || r.Node == nil {
		return false
	}
	r.Engine.Cancel(r.Node)
	if r.Eval != nil {
		r.Eval.CancelProcesses()
	}
	return true
}

func (r *Runtime) Result() RuntimeResult {
	if r == nil {
		return RuntimeResult{}
	}
	if r.Watching {
		return RuntimeResult{}
	}
	if r.Program != nil {
		if r.Handle == nil {
			return RuntimeResult{}
		}
		// Definition targets publish a current value without reaching a terminal
		// node state; this mirrors the native CLI's expression loop.
		if r.Session != nil {
			r.Session.Observe(r.Handle)
		}
		if r.Handle.Definition && r.Handle.Node != nil && r.Handle.Node.Current {
			if r.Session == nil { r.Program.ObserveDefinition(r.Handle) }
			return RuntimeResult{Value: r.Handle.Node.Latest.Clone(r.Alloc), Done: true}
		}
		poll := r.Handle.Poll()
		if !poll.Done {
			return RuntimeResult{}
		}
		result := poll.Result
		if result.Diagnostic.Code != "" {
			d := result.Diagnostic.Clone(r.Alloc)
			result.Free(r.Alloc)
			return RuntimeResult{Diagnostic: d, Done: true}
		}
		if result.Path != "" {
			value := core.NewString(r.Alloc, result.Path)
			result.Free(r.Alloc)
			return RuntimeResult{Value: value, Done: true}
		}
		value := result.Value.Clone(r.Alloc)
		result.Free(r.Alloc)
		return RuntimeResult{Value: value, Done: true}
	}
	if r.Node == nil {
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
	r.freeWatchHandles()
	if r.Handle != nil {
		r.Handle.Free()
		r.Handle = nil
	}
	if r.Session != nil {
		r.Session.Free()
		r.Session, r.Program = nil, nil
	} else if r.Program != nil {
		r.Program.Free()
		r.Program = nil
	}
	if r.Host != nil {
		r.Host.Free()
		r.Host = nil
	}
	if len(r.EventJSON) != 0 {
		slices.Free(r.Alloc, r.EventJSON)
		r.EventJSON = nil
	}
	eval.FreeEffects(r.Alloc, r.Effects)
	for i := range r.Environment {
		mem.FreeString(r.Alloc, r.Environment[i])
	}
	slices.Free(r.Alloc, r.Environment)
	r.InspectionGrant("", "")
	for i := range r.ToolPaths {
		mem.FreeString(r.Alloc, r.ToolPaths[i].Name)
		mem.FreeString(r.Alloc, r.ToolPaths[i].Path)
	}
	slices.Free(r.Alloc, r.ToolPaths)
	if r.ExprParsed != nil {
		r.ExprParsed.Free()
	}
	r.Eval.Free()
	r.Engine.Free()
	r.Registry.Free()
	r.Parsed.Free()
	r.freeBuildSources()
	if r.Arena != nil {
		r.Arena.Reset()
		return
	}
	a := r.Alloc
	*r = Runtime{}
	mem.Free(a, r)
}
