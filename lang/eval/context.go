// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host"
	"littlemake/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

type Context struct {
	Program            *Program
	Engine             *core.EngineContext
	Scope              *Scope
	Run                mem.Allocator
	Requests           *host.Queue
	Cwd                string
	Frames             []diagnostic.Frame
	Source             string
	Grants             []Grant
	Inputs             []core.Value
	Outputs            []core.Value
	Args               []core.Value
	HasArgs            bool
	RuleFrames         []RuleFrame
	Effects            []Effect
	WritePaths         []string
	Span               source.Span
	Phase              Phase
	ResolveDefinition  DefinitionResolver
	ResolverState      any
	DependencyObserver func(any, core.ResourceKey)
	// OperationObserver records the stable operation identity used by a render.
	// It is observational only and must not mutate evaluation state.
	OperationObserver  func(any, string, string)
	denied             bool
	phaseInvalid       bool
	activeCapabilities []Capability
	operationStart     int
	operationEnd       int
	completionConsumed bool
}

type operationState struct {
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
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd {
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
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd {
			state.Value, state.Free, state.Generation = value, free, c.Engine.Generation()
			return
		}
	}
	c.Program.OperationStates = slices.Append(c.Program.Alloc, c.Program.OperationStates, operationState{NodeID: c.Engine.NodeID(), Generation: c.Engine.Generation(), Start: c.operationStart, End: c.operationEnd, Value: value, Free: free})
}
func (c *Context) ClearOperationState() {
	if c == nil || c.Program == nil || c.Engine == nil {
		return
	}
	for i := range c.Program.OperationStates {
		state := &c.Program.OperationStates[i]
		if state.NodeID == c.Engine.NodeID() && state.Start == c.operationStart && state.End == c.operationEnd {
			if state.Free != nil {
				state.Free(c.Program.Alloc, state.Value)
			}
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
	RenderingPhase
)

type DefinitionResolver func(any, core.ResourceKey, *Context) Result

type EffectKind int

const (
	EffectOut EffectKind = iota
	EffectErr
	EffectYield
	EffectWrite
)

type Effect struct {
	Kind EffectKind
	Data []byte
	Span source.Span
}

func (e *Effect) Free(a mem.Allocator) {
	if len(e.Data) != 0 {
		slices.Free(a, e.Data)
	}
	*e = Effect{}
}
func FreeEffects(a mem.Allocator, effects []Effect) {
	for i := range effects {
		effects[i].Free(a)
	}
	if len(effects) != 0 {
		slices.Free(a, effects)
	}
}

func (c *Context) Emit(kind EffectKind, data []byte) {
	if c.Phase == PlanningPhase {
		c.phaseInvalid = true
		return
	}
	c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: kind, Data: slices.Clone(c.Run, data), Span: c.Span})
}
func (c *Context) EmitWrite(name string, data []byte) {
	if c.Phase == PlanningPhase {
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
	Inputs  []core.Value
	Outputs []core.Value
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
	canonicalName := canonicalPath(a, c.Cwd, name)
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
			root := canonicalPath(a, c.Cwd, grant.Names[j])
			allowed := canonicalName == root || (len(canonicalName) > len(root) && len(root) != 0 && canonicalName[:len(root)] == root && canonicalName[len(root)] == '/')
			mem.FreeString(a, root)
			if allowed {
				return true
			}
		}
	}
	return false
}

func canonicalPath(a mem.Allocator, cwd string, name string) string {
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
	if c.DependencyObserver != nil {
		c.DependencyObserver(c.ResolverState, key)
	}
	return c.Engine.Dependency(key)
}

// Value returns a borrowed current dependency value after Dependency accepted it.
func (c *Context) Value(key core.ResourceKey) core.CurrentValue {
	if c.Engine == nil {
		return core.CurrentValue{}
	}
	return c.Engine.Value(key)
}

// Submit queues an owned request and records its generated ID on the active
// engine node. The host completes it using the request correlation data.
func (c *Context) Submit(kind host.RequestKind, payload core.Value) int64 {
	if c.Phase == PlanningPhase {
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
	return c.Program.callValues(callable.Callable.(*Function), values, c, source.Span{})
}

// FreeCallable releases a temporary function after an operation has finished
// invoking it. Named functions remain owned by their lexical scope.
func (c *Context) FreeCallable(callable *core.Value) { freeCallables(c.Run, callable) }
