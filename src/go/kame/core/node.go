package core

type NodeState int

const (
	NodeIdle NodeState = iota
	NodeWaiting
	NodeReady
	NodeComplete
	NodeFailed
	NodeCancelled
)

type Node struct {
	ID              int64
	Key             ResourceKey
	Producer        Producer
	Context         any
	ContextFree     ContextFree
	Latest          Value
	Current         bool
	Requested       bool
	State           NodeState
	Revision        int64
	Generation      int64
	Attempt         int64
	Diagnostic      Diagnostic
	Static          []*Node
	Dynamic         []*Node
	// Retain old dependency interest while a reactive invocation rebinds edges.
	previousDynamic []*Node
	// OrderOnly edges schedule work without consuming prerequisite contents.
	OrderOnly []*Node
	InvalidatedForOrderOnly bool
	// Observed failures wake the producer instead of failing it transitively.
	Observed        []*Node
	Dependents      []*Node
	Subs            []*Subscription
	Interest        int64
	RequestInterest int64
	Completion      Completion
	HasCompletion   bool
	Submitted       bool
	HostRequestID   int64
	materializer    *Materializer
	offered         bool
	invalidating    bool
	// Restartable nodes can be evaluated again after their last consumer releases them.
	Restartable bool
}

func (n *Node) HasActiveSource() bool { return n != nil && n.materializer != nil }

func (n *Node) complete(e *Engine, diagnostic Diagnostic) {
	if n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled { return }
	n.Diagnostic = diagnostic
	if diagnostic.Code == "" { n.State = NodeComplete } else { n.State = NodeFailed; if n.Current { n.Latest.Free(e.Alloc); n.Current = false } }
	e.releasePreviousDependencies(n)
	e.emit(n, terminal(n))
	for i := range n.Dependents {
		if n.Dependents[i].State == NodeWaiting { n.Dependents[i].State = NodeReady }
	}
}
