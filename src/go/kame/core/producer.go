package core

import (
	"kame/diagnostic"
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
	ProducerRestart
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

// PublishSigned separates a resource's public value (for example a file path)
// from its semantic identity (the file's contents).
func (c *EngineContext) PublishSigned(value Value, signature Signature) {
	if c.node.State == NodeComplete || c.node.State == NodeFailed || c.node.State == NodeCancelled {
		value.Free(c.engine.Alloc)
		return
	}
	c.engine.publishSigned(c.node, value, signature)
}

func (c *EngineContext) Fail(d Diagnostic) { c.node.complete(c.engine, d) }

// Dependency adds one generation-owned edge and returns whether it is current.
func (c *EngineContext) Dependency(key ResourceKey) bool {
	return c.dependency(key, false)
}

// OrderDependency schedules a hard prerequisite without consuming its contents.
// A normal edge already present cannot be downgraded to order-only.
func (c *EngineContext) OrderDependency(key ResourceKey) bool {
	dep := c.engine.find(key)
	existed := dep != nil && slices.Contains(c.node.Dynamic, dep)
	ordered := dep != nil && slices.Contains(c.node.OrderOnly, dep)
	ready := c.dependency(key, false)
	dep = c.engine.find(key)
	if dep != nil && (!existed || ordered) && slices.Contains(c.node.Dynamic, dep) {
		c.node.OrderOnly = slices.Append(c.engine.Alloc, c.node.OrderOnly, dep)
	}
	return ready
}

// TryDependency lets a producer inspect a dependency's failure as a value.
// The edge retains normal interest, invalidation, and cycle detection.
func (c *EngineContext) TryDependency(key ResourceKey) bool { return c.dependency(key, true) }

// DependencyDiagnostic returns a borrowed diagnostic for an observed edge.
func (c *EngineContext) DependencyDiagnostic(key ResourceKey) Diagnostic {
	dep := c.engine.find(key)
	if dep != nil && slices.Contains(c.node.Observed, dep) && (dep.State == NodeFailed || dep.State == NodeCancelled) {
		// Recovery consumed an error, not a missing resource or an ordinary value.
		c.Observe(dep.Key, Signature{})
		return dep.Diagnostic
	}
	return Diagnostic{}
}

func (c *EngineContext) dependency(key ResourceKey, observed bool) bool {
	dep := c.engine.node(key)
	if dep == nil || dep == c.node || reaches(dep, c.node) {
		d := Diagnostic{Code: DiagnosticDependencyCycle, Severity: diagnostic.Error, Message: "dependency cycle"}
		if c.node.Key.Kind == ResourceDefinition {
			d.Frames = slices.Append(c.engine.Alloc, d.Frames, diagnostic.Frame{Label: "definition"})
		}
		c.node.complete(c.engine, d)
		return false
	}
	if dep.Restartable && dep.State == NodeCancelled && dep.Interest == 0 {
		c.engine.Invalidate(dep)
	}
	if !slices.Contains(c.node.Dynamic, dep) {
		c.node.Dynamic = slices.Append(c.engine.Alloc, c.node.Dynamic, dep)
		if observed {
			c.node.Observed = slices.Append(c.engine.Alloc, c.node.Observed, dep)
		}
		dep.Dependents = slices.Append(c.engine.Alloc, dep.Dependents, c.node)
		c.engine.emit(c.node, Event{Kind: UpdateDependency, DependencyID: dep.ID})
		held := false
		for i := range c.node.previousDynamic {
			if c.node.previousDynamic[i] == dep {
				copy(c.node.previousDynamic[i:], c.node.previousDynamic[i+1:])
				c.node.previousDynamic = c.node.previousDynamic[:len(c.node.previousDynamic)-1]
				held = true
				break
			}
		}
		if c.node.Interest != 0 && !held {
			c.engine.interest(dep, c.node.Interest)
		}
	}
	for i := range c.node.OrderOnly {
		if c.node.OrderOnly[i] == dep {
			copy(c.node.OrderOnly[i:], c.node.OrderOnly[i+1:])
			c.node.OrderOnly = c.node.OrderOnly[:len(c.node.OrderOnly)-1]
			break
		}
	}
	if !observed {
		for i := range c.node.Observed {
			if c.node.Observed[i] == dep {
				copy(c.node.Observed[i:], c.node.Observed[i+1:])
				c.node.Observed = c.node.Observed[:len(c.node.Observed)-1]
				break
			}
		}
	}
	if observed && (dep.State == NodeFailed || dep.State == NodeCancelled) {
		return true
	}
	if !observed && (dep.State == NodeFailed || dep.State == NodeCancelled) {
		c.node.complete(c.engine, dep.Diagnostic.Clone(c.engine.Alloc))
		return false
	}
	if !dep.Current || dep.ValidationPending || dep.State == NodeFailed || dep.State == NodeCancelled {
		request(dep)
		return false
	}
	return true
}

// CurrentValue is the borrowed current value of a dependency, when present.
// Clone Value before retaining it beyond the active producer call.
type CurrentValue struct {
	Value     Value
	Signature Signature
	Revision  int64
	OK        bool
}

// Value returns the current value of a dependency without adding an edge. It
// reports OK false when the key is unknown, names this node, or has no current
// value.
func (c *EngineContext) Value(key ResourceKey) CurrentValue {
	dep := c.engine.find(key)
	if dep == nil || dep == c.node || (!slices.Contains(c.node.Static, dep) && !slices.Contains(c.node.Dynamic, dep)) || !dep.Current || dep.ValidationPending {
		return CurrentValue{}
	}
	c.Observe(dep.Key, dep.Signature)
	return CurrentValue{Value: dep.Latest, Signature: dep.Signature, Revision: dep.Revision, OK: true}
}

func (c *EngineContext) Completion() Completion { return c.completion }

func (c *EngineContext) Context() any { return c.node.Context }

func (c *EngineContext) Allocator() mem.Allocator { return c.engine.Alloc }
func (c *EngineContext) NodeID() int64            { return c.node.ID }

// RetainRoot pins the demanding generation for invocation-owned handles.
func (c *EngineContext) RetainRoot() *Root { return c.engine.RequestRoot(c.node) }
func (c *EngineContext) Generation() int64 { return c.node.Generation }
func (c *EngineContext) Attempt() int64    { return c.node.Attempt }
func (c *EngineContext) Failed() bool {
	return c.node.State == NodeFailed || c.node.State == NodeCancelled
}
func (c *EngineContext) Diagnostic() Diagnostic { return c.node.Diagnostic }
func (c *EngineContext) Submitted() bool        { return c.node.Submitted }

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
	if source == nil || c.node.materializer != nil {
		return false
	}
	c.node.materializer = NewMaterializer(c.engine.Alloc, source)
	return true
}
