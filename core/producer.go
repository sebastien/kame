package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type ProducerResult int

const (
	ProducerCompleted ProducerResult = iota
	ProducerWaiting
	ProducerSubmitted
	ProducerActive
	ProducerFailed
)

// Producer never mutates a node directly. Its context is valid only for its call.
type Producer func(*EngineContext, int64) ProducerResult

type ContextFree func(mem.Allocator, any)

type Completion struct {
	NodeID     int64
	Generation int64
	Attempt    int64
	RequestID  int64
	Value      Value
	HasValue   bool
	Diagnostic Diagnostic
}

type Cancellation struct {
	NodeID     int64
	Generation int64
	Attempt    int64
	RequestID  int64
}

type EngineContext struct {
	engine     *Engine
	node       *Node
	completion Completion
}

func (c *EngineContext) Publish(value Value) {
	if c.node.State == NodeComplete || c.node.State == NodeFailed || c.node.State == NodeCancelled {
		value.Free(c.engine.Alloc)
		return
	}
	c.engine.publish(c.node, value)
}

func (c *EngineContext) Fail(d Diagnostic) { c.node.complete(c.engine, d) }

// Dependency adds one generation-owned edge and returns whether it is current.
func (c *EngineContext) Dependency(key ResourceKey) bool {
	dep := c.engine.node(key)
	if dep == nil || dep == c.node || reaches(dep, c.node) {
		c.node.complete(c.engine, Diagnostic{Code: DiagnosticDependencyCycle})
		return false
	}
	if !slices.Contains(c.node.Dynamic, dep) {
		c.node.Dynamic = slices.Append(c.engine.Alloc, c.node.Dynamic, dep)
		dep.Dependents = slices.Append(c.engine.Alloc, dep.Dependents, c.node)
		c.engine.emit(c.node, Event{Kind: UpdateDependency, DependencyID: dep.ID})
		if c.node.Interest != 0 {
			c.engine.interest(dep, c.node.Interest)
		}
	}
	if !dep.Current || dep.State == NodeFailed || dep.State == NodeCancelled {
		request(dep)
		return false
	}
	return true
}

// CurrentValue is the borrowed current value of a dependency, when present.
// Clone Value before retaining it beyond the active producer call.
type CurrentValue struct {
	Value Value
	OK    bool
}

// Value returns the current value of a dependency without adding an edge. It
// reports OK false when the key is unknown, names this node, or has no current
// value.
func (c *EngineContext) Value(key ResourceKey) CurrentValue {
	dep := c.engine.find(key)
	if dep == nil || dep == c.node || (!slices.Contains(c.node.Static, dep) && !slices.Contains(c.node.Dynamic, dep)) || !dep.Current {
		return CurrentValue{}
	}
	return CurrentValue{Value: dep.Latest, OK: true}
}

func (c *EngineContext) Completion() Completion { return c.completion }

func (c *EngineContext) Context() any { return c.node.Context }

func (c *EngineContext) Allocator() mem.Allocator { return c.engine.Alloc }

// Submit records the host request that will later resume this invocation.
func (c *EngineContext) Submit(requestID int64) {
	if requestID == 0 {
		c.Fail(Diagnostic{Code: DiagnosticHostFailure})
		return
	}
	c.node.Submitted = true
	c.node.HostRequestID = requestID
}

// AttachSource transfers source ownership to the active producer generation.
func (c *EngineContext) AttachSource(source *Source) bool {
	if source == nil || c.node.materializer != nil { return false }
	c.node.materializer = NewMaterializer(c.engine.Alloc, source)
	return true
}
