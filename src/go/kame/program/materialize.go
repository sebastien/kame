package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Result struct {
	Path       string
	Value      core.Value
	Fresh      bool
	Diagnostic diagnostic.Diagnostic
}

func (r *Result) Free(a mem.Allocator) {
	mem.FreeString(a, r.Path)
	r.Value.Free(a)
	r.Diagnostic.Free(a)
	*r = Result{}
}

type instanceState struct {
	Program *Program
	Index   int
}
type instanceResult struct {
	Node       *core.Node
	Plan       Plan
	Diagnostic diagnostic.Diagnostic
}
type renderResult struct {
	Commands   string
	Effects    []eval.Effect
	WritePaths []string
	LineSpans  []diagnostic.Span
	Waiting    bool
	Diagnostic diagnostic.Diagnostic
}
type inputsResult struct {
	Inputs         []string
	DynamicInputs  []string
	ResourceInputs []PlanInput
	Owned          bool
	Waiting        bool
	Diagnostic     diagnostic.Diagnostic
}
type renderDependencyState struct {
	Program    *Program
	Index      int
	Inspection bool
}
type externalFileState struct {
	Program *Program
	Name    string
}
type externalValueState struct {
	Program *Program
	Name    string
	Kind    core.ResourceKind
}

func freeInstanceState(a mem.Allocator, value any) { mem.Free(a, value.(*instanceState)) }
func freeExternalFileState(a mem.Allocator, value any) {
	state := value.(*externalFileState)
	mem.FreeString(a, state.Name)
	mem.Free(a, state)
}
func freeExternalValueState(a mem.Allocator, value any) {
	state := value.(*externalValueState)
	mem.FreeString(a, state.Name)
	mem.Free(a, state)
}

func produceExternalFile(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalFileState)
	if state.Program.Forwarding {
		completion := c.Completion()
		if completion.RequestID == 0 {
			payload := host.FilePayload(c.Allocator(), host.OpExists, state.Name)
			id := state.Program.Eval.Requests.Submit(c.NodeID(), c.Generation(), c.Attempt(), host.RequestReadFile, payload)
			payload.Free(c.Allocator())
			c.Submit(id)
			return core.ProducerSubmitted
		}
		if completion.Diagnostic.Code != "" {
			completion.Value.Free(c.Allocator())
			c.Fail(completion.Diagnostic)
			return core.ProducerFailed
		}
		exists := completion.HasValue && completion.Value.Kind == core.Bool && completion.Value.Bool
		completion.Value.Free(c.Allocator())
		if exists {
			c.Publish(core.NewString(c.Allocator(), state.Name))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
	}
	name := state.Program.canonicalTarget(state.Name, true)
	result := state.Program.Host.Stat(name)
	mem.FreeString(state.Program.Alloc, name)
	// A missing path is an observed dependency, not a failed producer. Declared
	// inputs still fail before execution. The cache records an explicit missing
	// marker and invalidates when the path later appears.
	if result.Failed {
		c.Fail(failure(state.Program.Alloc, "TGT_NO_RULE", "required input does not exist: "+state.Name))
		return core.ProducerFailed
	}
	if !result.Exists {
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	c.Publish(core.NewString(c.Allocator(), state.Name))
	return core.ProducerCompleted
}
func produceExternalValue(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalValueState)
	if state.Kind == core.ResourceEnvironment {
		value, ok := state.Program.configuredEnvironment(state.Name)
		if ok {
			c.Publish(core.NewString(c.Allocator(), value))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
	} else if state.Kind == core.ResourceGlob {
		c.Publish(state.Program.wildcard(state.Name))
	} else {
		c.Fail(failure(state.Program.Alloc, "HOST_FAIL", "invalid external resource"))
		return core.ProducerFailed
	}
	return core.ProducerCompleted
}

func (p *Program) Materialize(target string) Result {
	started := p.Start(target)
	handle, d := started.Handle, started.Diagnostic
	if d.Code != "" {
		if d.Code == "TGT_NO_RULE" && isPathTarget(target) {
			name := p.canonicalTarget(target, true)
			result := p.Host.Stat(name)
			mem.FreeString(p.Alloc, name)
			if result.Exists {
				d.Free(p.Alloc)
				return Result{Path: cloneText(p.Alloc, target), Fresh: true}
			}
		}
		return Result{Diagnostic: d}
	}
	defer handle.Free()
	for {
		p.Tick(10)
		result := handle.Poll()
		if result.Done {
			return result.Result
		}
	}
}

func (p *Program) Start(target string) HandleStart {
	resolved := p.instanceFor(target)
	node, plan, d := resolved.Node, resolved.Plan, resolved.Diagnostic
	if d.Code != "" {
		return HandleStart{Diagnostic: d}
	}
	definition := node == nil
	if definition {
		node = p.Eval.Definition(target)
		if node == nil {
			plan.Free(p.Alloc)
			return HandleStart{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+target)}
		}
	}
	plan.Free(p.Alloc)
	index := p.instanceIndex(node)
	if index >= 0 {
		p.epoch++
		p.Instances[index].runEpoch = p.epoch
		p.invalidateStaleBareTasks(index, p.epoch)
	}
	if index >= 0 && taskTerminal(node.State) {
		entry := &p.Instances[index]
		if entry.Rule.Kind == rule.FileRule && node.State == core.NodeComplete {
			entry.Plan.Freshness = p.freshness(&entry.Plan, node)
			if p.Options.Force || entry.Plan.Freshness == Stale {
				p.Engine.Invalidate(node)
			}
		} else if entry.Rule.Kind == rule.TaskRule || entry.Rule.Kind == rule.CachedTaskRule {
			p.Engine.Invalidate(node)
		}
	}
	handle := mem.Alloc[Handle](p.Alloc)
	handle.Program, handle.Root, handle.Node, handle.Target, handle.Definition = p, p.Engine.RequestRoot(node), node, cloneText(p.Alloc, target), definition
	return HandleStart{Handle: handle}
}

// invalidateStaleBareTasks reruns stale terminal bare tasks once for this root epoch.
// It walks instance indexes and stored plan inputs only. Calling instanceFor here
// would allocate and transfer Plans during the walk.
func (p *Program) invalidateStaleBareTasks(root int, epoch int64) {
	closure := p.taskClosure(root)
	for i := len(closure) - 1; i >= 0; i-- {
		p.claimStaleTask(p.Instances[closure[i]].Node, epoch)
	}
	slices.Free(p.Alloc, closure)
}

func taskTerminal(state core.NodeState) bool {
	return state == core.NodeComplete || state == core.NodeFailed || state == core.NodeCancelled
}

// claimStaleTask invalidates one terminal task whose last claim is older than epoch.
// The stamp is set before Invalidate so a later parent in this epoch, or a resumed
// producer, does not invalidate the same generation again.
func (p *Program) claimStaleTask(node *core.Node, epoch int64) {
	if node == nil || epoch == 0 {
		return
	}
	index := p.instanceIndex(node)
	if index < 0 || p.Instances[index].Rule == nil {
		return
	}
	kind := p.Instances[index].Rule.Kind
	state := p.Instances[index].Node.State
	if !taskTerminal(state) || p.Instances[index].Node.Interest > 0 || p.Instances[index].satisfiedEpoch >= epoch {
		return
	}
	if kind == rule.CachedTaskRule {
		if state == core.NodeComplete && !p.cacheBlockedByBareTask(&p.Instances[index]) {
			return
		}
	} else if kind != rule.TaskRule {
		return
	}
	p.Instances[index].satisfiedEpoch = epoch
	p.Instances[index].runEpoch = epoch
	p.Engine.Invalidate(p.Instances[index].Node)
}

func (p *Program) taskClosure(root int) []int {
	var seen []int
	seen = slices.Append(p.Alloc, seen, root)
	i := 0
	for i < len(seen) {
		p.appendTaskClosure(seen[i], &seen)
		i++
	}
	return seen
}

func (p *Program) appendTaskClosure(index int, seen *[]int) {
	if index < 0 || index >= len(p.Instances) {
		return
	}
	inputs := p.Instances[index].Plan.Inputs
	if p.Instances[index].Plan.Resolved {
		inputs = p.Instances[index].Plan.ResolvedInputs
	}
	for n := range inputs {
		dep := p.instanceByTarget(inputs[n])
		if dep >= 0 && !slices.Contains(*seen, dep) {
			*seen = slices.Append(p.Alloc, *seen, dep)
		}
	}
	node := p.Instances[index].Node
	if node == nil {
		return
	}
	for n := range node.Dynamic {
		dep := p.instanceIndex(node.Dynamic[n])
		if dep >= 0 && !slices.Contains(*seen, dep) {
			*seen = slices.Append(p.Alloc, *seen, dep)
		}
	}
	for n := range node.Static {
		dep := p.instanceIndex(node.Static[n])
		if dep >= 0 && !slices.Contains(*seen, dep) {
			*seen = slices.Append(p.Alloc, *seen, dep)
		}
	}
}

func (p *Program) instanceByTarget(name string) int {
	for i := range p.Instances {
		if !p.Instances[i].Inspection && p.Instances[i].Plan.Target == name {
			return i
		}
	}
	return -1
}

func (p *Program) Tick(wait int) {
	p.pump(wait)
	p.drainRequests()
	p.Engine.DrainCompletions()
	jobs := p.Options.Jobs
	if jobs <= 0 {
		jobs = 1
	}
	ready := p.Engine.Ready(jobs)
	for i := range ready {
		p.Engine.Dispatch(ready[i])
	}
	slices.Free(p.Alloc, ready)
	p.observeInstances()
}

func (h *Handle) Cancel() {
	if h == nil || h.Program == nil || h.Root == nil {
		return
	}
	h.Program.Engine.Release(h.Root)
	h.Root = nil
	h.Program.drainCancellations()
}

func (h *Handle) Poll() HandleResult { return h.poll(true) }

// PollRetained observes completion without dropping root interest. Watchers and
// reactive hosts retain the root across subsequent invalidations.
func (h *Handle) PollRetained() HandleResult { return h.poll(false) }

func (h *Handle) poll(release bool) HandleResult {
	if h == nil || h.Program == nil || h.Node == nil {
		return HandleResult{Done: true, Result: Result{Diagnostic: failure(mem.System, "TGT_NO_RULE", "invalid handle")}}
	}
	if h.Definition && h.Node.Current && h.Node.State != core.NodeFailed && h.Node.State != core.NodeCancelled {
		value := h.Node.Latest.Clone(h.Program.Alloc)
		if release && h.Root != nil {
			h.Program.Engine.Release(h.Root)
			h.Root = nil
		}
		return HandleResult{Done: true, Result: Result{Value: value}}
	}
	if h.Node.State != core.NodeComplete && h.Node.State != core.NodeFailed && h.Node.State != core.NodeCancelled {
		return HandleResult{}
	}
	if release && h.Root != nil {
		h.Program.Engine.Release(h.Root)
		h.Root = nil
	}
	if h.Node.State != core.NodeComplete {
		return HandleResult{Done: true, Result: Result{Diagnostic: h.Node.Diagnostic.Clone(h.Program.Alloc)}}
	}
	if h.Definition {
		return HandleResult{Done: true, Result: Result{Value: h.Node.Latest.Clone(h.Program.Alloc)}}
	}
	result := Result{Fresh: h.Program.Instances[h.Program.instanceIndex(h.Node)].Plan.Freshness == Fresh}
	if h.Program.Instances[h.Program.instanceIndex(h.Node)].Rule.Kind == rule.FileRule {
		result.Path = cloneText(h.Program.Alloc, h.Target)
	}
	return HandleResult{Done: true, Result: result}
}

func (p *Program) instanceIndex(node *core.Node) int {
	for i := range p.Instances {
		if p.Instances[i].Node == node {
			return i
		}
	}
	return -1
}
func isPathTarget(target string) bool { return isPath(target) || hasSlash(target) }

func (p *Program) instanceFor(target string) instanceResult {
	planned := p.Plan(target)
	plan, d := planned.Plan, planned.Diagnostic
	if d.Code != "" {
		return instanceResult{Diagnostic: d}
	}
	if plan.Rule == nil {
		return instanceResult{Plan: plan}
	}
	for i := range p.Instances {
		entry := &p.Instances[i]
		if !entry.Inspection && entry.Rule == plan.Rule && sameCaptures(entry.Captures, plan.Captures) {
			return instanceResult{Node: entry.Node, Plan: plan}
		}
	}
	state := mem.Alloc[instanceState](p.Alloc)
	state.Program, state.Index = p, len(p.Instances)
	node := p.Engine.AddOwned(plan.Key, produce, state, freeInstanceState)
	if node == nil {
		plan.Free(p.Alloc)
		return instanceResult{Diagnostic: failure(p.Alloc, "TGT_AMBIG", "duplicate rule instance")}
	}
	p.Instances = slices.Append(p.Alloc, p.Instances, instance{Rule: plan.Rule, Captures: cloneCaptures(p.Alloc, plan.Captures), Node: node, Plan: plan})
	return instanceResult{Node: node}
}

func sameCaptures(left, right []template.CaptureValue) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || left[i].Text != right[i].Text {
			return false
		}
	}
	return true
}
