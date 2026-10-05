// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

type Context struct {
	Program     *Program
	Engine      *core.EngineContext
	Scope       *Scope
	Run         mem.Allocator
	Requests    *host.Queue
	Cwd         string
	Environment []string
	// HasEnvironment binds a complete snapshot, including an empty environment.
	HasEnvironment bool
	// DefinitionNamespace identifies an immutable runtime environment and phase.
	DefinitionNamespace    [32]byte
	HasDefinitionNamespace bool
	ScriptGroup            int64
	RecipeNode             int64
	TimeoutMS              int64
	Frames                 []diagnostic.Frame
	Source                 string
	Grants                 []Grant
	Inputs                 []core.Value
	Outputs                []core.Value
	Args                   []core.Value
	HasArgs                bool
	RuleFrames             []RuleFrame
	Effects                []Effect
	WritePaths             []string
	Span                   source.Span
	OperationName          string
	// CallPath distinguishes repeated calls of the same authored AST on a node.
	CallPath                  string
	operationArguments        []*expr.Expr
	Phase                     Phase
	ResolveDefinition         DefinitionResolver
	ResolverState             any
	DependencyObserver        func(any, core.ResourceKey)
	DependencyContextObserver func(any, core.ResourceKey, *Context)
	// DirectHostRequests bypasses build-graph dependency discovery for host
	// operations. Embedders with an explicit request/completion loop use it to
	// obtain each host value directly from their capability provider.
	DirectHostRequests bool
	ToolResolver       func(any, string) (string, bool)
	// OperationObserver records the stable operation identity used by a render.
	// It is observational only and must not mutate evaluation state.
	OperationObserver func(any, string, string)
	// RenderStack tracks file templates currently being rendered for
	// TPL_CYCLE detection. Entries are Run-owned canonical paths.
	RenderStack        []string
	denied             bool
	phaseInvalid       bool
	activeCapabilities []Capability
	operationStart     int
	operationEnd       int
	completionConsumed bool
	RecoverFailures    bool
	ReserveEnv         bool
}

type operationState struct {
	CallPath   string
	NodeID     int64
	Generation int64
	Start      int
	End        int
	Value      any
	Free       ContextFree
}

// OperationState survives a suspended application on one engine generation.
// Operations use it only for progress that must not be replayed after a wait.
func (c *Context) OperationState() any {
	if c == nil || c.Program == nil || c.Engine == nil {
		return nil
	}
	for i := range c.Program.OperationStates {
		state := &c.Program.OperationStates[i]
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd && state.CallPath == c.CallPath {
			if state.Generation == c.Engine.Generation() {
				return state.Value
			}
			if state.Free != nil {
				state.Free(c.Program.Alloc, state.Value)
			}
			state.Generation, state.Value, state.Free = c.Engine.Generation(), nil, nil
			return nil
		}
	}
	return nil
}
func (c *Context) SetOperationState(value any, free ContextFree) {
	if c == nil || c.Program == nil || c.Engine == nil {
		return
	}
	for i := range c.Program.OperationStates {
		state := &c.Program.OperationStates[i]
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd && state.CallPath == c.CallPath {
			state.Value, state.Free, state.Generation = value, free, c.Engine.Generation()
			return
		}
	}
	c.Program.OperationStates = slices.Append(c.Program.Alloc, c.Program.OperationStates, operationState{CallPath: owned(c.Program.Alloc, c.CallPath), NodeID: c.Engine.NodeID(), Generation: c.Engine.Generation(), Start: c.operationStart, End: c.operationEnd, Value: value, Free: free})
}
func (c *Context) ClearOperationState() {
	if c == nil || c.Program == nil || c.Engine == nil {
		return
	}
	for i := range c.Program.OperationStates {
		state := &c.Program.OperationStates[i]
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd && state.CallPath == c.CallPath {
			if state.Free != nil {
				state.Free(c.Program.Alloc, state.Value)
			}
			mem.FreeString(c.Program.Alloc, state.CallPath)
			copy(c.Program.OperationStates[i:], c.Program.OperationStates[i+1:])
			c.Program.OperationStates = c.Program.OperationStates[:len(c.Program.OperationStates)-1]
			return
		}
	}
}

type Phase int

const (
	EvaluatePhase Phase = iota
	PlanningPhase
	// ResolvingPhase permits read-only host requests while resolving rule inputs.
	// It rejects effects, writes, and process execution.
	ResolvingPhase
	RenderingPhase
)

type DefinitionResolver func(any, core.ResourceKey, *Context) Result

type EffectKind int

const (
	EffectOut EffectKind = iota
	EffectErr
	EffectYield
	EffectWrite
	// ProcessWrite records a process-owned path, not bytes to write on commit.
	EffectProcessWrite
)

type Effect struct {
	Kind EffectKind
	Data []byte
	Span source.Span
}

func (e *Effect) Free(a mem.Allocator) {
	slices.Free(a, e.Data)
	*e = Effect{}
}
func FreeEffects(a mem.Allocator, effects []Effect) {
	for i := range effects {
		effects[i].Free(a)
	}
	slices.Free(a, effects)
}

func (c *Context) Emit(kind EffectKind, data []byte) {
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		c.phaseInvalid = true
		return
	}
	if c.Program != nil && c.Program.DryRun && (kind == EffectOut || kind == EffectErr) {
		return
	}
	c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: kind, Data: slices.Clone(c.Run, data), Span: c.Span})
}
func (c *Context) EmitWrite(name string, data []byte) {
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		c.phaseInvalid = true
		return
	}
	c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: EffectWrite, Data: slices.Clone(c.Run, data), Span: c.Span})
	c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, name))
}

func (c *Context) PhaseInvalid() bool { return c.phaseInvalid }
func (c *Context) MarkPhaseInvalid()  { c.phaseInvalid = true }

// RuleFrame supplies the inputs and outputs for one enclosing rule evaluation.
// Selectors use the most recently pushed frame.
type RuleFrame struct {
	FileRule    bool
	NewerInputs []core.Value
	Inputs      []core.Value
	Outputs     []core.Value
}

func (c *Context) allowed(capability Capability) bool {
	for i := range c.Grants {
		if c.Grants[i].Capability == capability {
			return true
		}
	}
	return false
}

// Allows checks an unrestricted grant or a lexical root/name grant. Filesystem
// operations pass their canonical path; environment operations pass the name.
func (c *Context) Allows(capability Capability, name string) bool {
	a := c.Run
	if a == nil {
		a = mem.System
	}
	canonicalName := canonicalGrantName(a, c.Cwd, name)
	defer mem.FreeString(a, canonicalName)
	for i := range c.Grants {
		grant := c.Grants[i]
		if grant.Capability != capability {
			continue
		}
		if len(grant.Names) == 0 {
			return true
		}
		for j := range grant.Names {
			root := canonicalGrantName(a, c.Cwd, grant.Names[j])
			allowed := canonicalName == root || (len(canonicalName) > len(root) && len(root) != 0 && canonicalName[:len(root)] == root && (root[len(root)-1] == '/' || canonicalName[len(root)] == '/'))
			mem.FreeString(a, root)
			if allowed {
				return true
			}
		}
	}
	return false
}

// File URI grants and native path grants name the same filesystem objects.
// Resource identity remains a URI in dependency keys; this conversion is only
// for access-policy comparisons.
func canonicalGrantName(a mem.Allocator, cwd string, name string) string {
	if core.IsResourceURIName(name) {
		parsed := core.ParseResourceURI(a, name)
		if parsed.Error == "" {
			if parsed.URI.Scheme == "file" {
				canonical := path.Clean(a, parsed.URI.Path)
				parsed.URI.Free()
				return canonical
			}
			canonical := parsed.URI.Canonical(a)
			parsed.URI.Free()
			return canonical
		}
		parsed.URI.Free()
	}
	return canonicalPath(a, cwd, name)
}

func canonicalPath(a mem.Allocator, cwd string, name string) string {
	if core.IsResourceURIName(name) {
		parsed := core.ParseResourceURI(a, name)
		if parsed.Error == "" {
			canonical := parsed.URI.Canonical(a)
			parsed.URI.Free()
			return canonical
		}
		return owned(a, name)
	}
	if path.IsAbs(name) {
		return path.Clean(a, name)
	}
	if cwd == "" {
		return path.Clean(a, name)
	}
	return path.Join(a, cwd, name)
}

func (c *Context) Dependency(key core.ResourceKey) bool {
	if c.Engine == nil {
		return false
	}
	resourcePath := ""
	if key.Kind == core.ResourceFile {
		resourcePath = canonicalPath(c.Run, c.Cwd, key.Name)
		key.Name = resourcePath
	}
	if c.DependencyContextObserver != nil {
		c.DependencyContextObserver(c.ResolverState, key, c)
	} else if c.DependencyObserver != nil {
		c.DependencyObserver(c.ResolverState, key)
	}
	current := c.Engine.Dependency(key)
	mem.FreeString(c.Run, resourcePath)
	return current
}

// Value returns a borrowed current dependency value after Dependency accepted it.
func (c *Context) Value(key core.ResourceKey) core.CurrentValue {
	if c.Engine == nil {
		return core.CurrentValue{}
	}
	resourcePath := ""
	if key.Kind == core.ResourceFile {
		resourcePath = canonicalPath(c.Run, c.Cwd, key.Name)
		key.Name = resourcePath
	}
	current := c.Engine.Value(key)
	mem.FreeString(c.Run, resourcePath)
	return current
}

// Submit queues an owned request and records its generated ID on the active
// engine node. The host completes it using the request correlation data.
func (c *Context) Submit(kind host.RequestKind, payload core.Value) int64 {
	if c.Phase == PlanningPhase {
		c.phaseInvalid = true
		return 0
	}
	if c.Phase == ResolvingPhase && kind != host.RequestReadFile && kind != host.RequestEnvironment {
		c.phaseInvalid = true
		return 0
	}
	if !c.requestAllowed(kind, payload) {
		c.denied = true
		return 0
	}
	if c.Engine == nil || c.Requests == nil {
		return 0
	}
	id := c.Requests.Submit(c.Engine.NodeID(), c.Engine.Generation(), c.Engine.Attempt(), kind, payload)
	if id != 0 {
		c.Engine.Submit(id)
	}
	return id
}

func (c *Context) requestAllowed(kind host.RequestKind, payload core.Value) bool {
	var capability Capability
	switch kind {
	case host.RequestReadFile:
		capability = Read
	case host.RequestWriteFile:
		capability = Write
	case host.RequestProcess:
		capability = Run
		input, output := host.PayloadText(payload, host.FieldInput), host.PayloadText(payload, host.FieldOutput)
		if (input != "" && !c.Allows(Read, input)) || (output != "" && !c.Allows(Write, output)) {
			return false
		}
		stages := host.PayloadStages(payload)
		for i := range stages {
			if stages[i].Kind != core.List || len(stages[i].List) == 0 || stages[i].List[0].Kind != core.String || !authorizeExecutable(c, stages[i].List[0].Text) {
				return false
			}
		}
	case host.RequestEnvironment:
		capability = Env
	default:
		for i := range c.activeCapabilities {
			unrestricted := false
			for j := range c.Grants {
				if c.Grants[j].Capability == c.activeCapabilities[i] && len(c.Grants[j].Names) == 0 {
					unrestricted = true
					break
				}
			}
			if !unrestricted {
				return false
			}
		}
		return true
	}
	name := host.PayloadPath(payload)
	if name == "" {
		for i := range c.Grants {
			if c.Grants[i].Capability == capability && len(c.Grants[i].Names) == 0 {
				return true
			}
		}
		return false
	}
	return c.Allows(capability, name)
}

// Resume binds a retained context to a new engine call and its completion.
func (c *Context) Resume(engine *core.EngineContext) {
	c.Engine, c.Requests = engine, c.Program.Requests
	c.completionConsumed, c.denied, c.phaseInvalid = false, false, false
}

// Completion returns the host completion that resumed this evaluation, if any.
func (c *Context) Completion() core.Completion {
	if c.Engine == nil || c.completionConsumed {
		return core.Completion{}
	}
	return c.Engine.Completion()
}
func (c *Context) TakeCompletion() core.Completion {
	completion := c.Completion()
	if completion.RequestID != 0 {
		c.completionConsumed = true
	}
	return completion
}

// Call invokes a lexical function with already evaluated values.
func (c *Context) Call(callable core.Value, values []core.Value) Result {
	if c == nil {
		return failure(mem.System, "EXPR_INVALID", source.Span{}, "expected a function")
	}
	if c.Program == nil || callable.Kind != core.Callable {
		return failure(c.Run, "EXPR_INVALID", source.Span{}, "expected a function")
	}
	return c.Program.callValues(callable.Callable.(*Function), values, c, c.Span)
}

// CallAt supplies a stable ordinal for callbacks resumed within a collection
// operation. Identical arguments at different indexes are still distinct calls.
func (c *Context) CallAt(callable core.Value, values []core.Value, index int) Result {
	previous := c.CallPath
	c.CallPath = invocationPath(c.Run, previous, c.Span.Start, index)
	r := c.Call(callable, values)
	mem.FreeString(c.Run, c.CallPath)
	c.CallPath = previous
	return r
}

func invocationPath(a mem.Allocator, parent string, start int, end int) string {
	var first, second [strconv.MaxIntBase10Len]byte
	return owned(a, parent+"/"+strconv.FormatInt(first[:], int64(start), 10)+":"+strconv.FormatInt(second[:], int64(end), 10))
}

// FreeCallable releases a temporary function after an operation has finished
// invoking it. Named functions remain owned by their lexical scope.
func (c *Context) FreeCallable(callable *core.Value) { freeCallables(c.Run, callable) }
