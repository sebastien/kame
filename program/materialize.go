package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/time"
)

type Result struct {
	Path       string
	Value      core.Value
	Fresh      bool
	Diagnostic diagnostic.Diagnostic
}

func (r *Result) Free(a mem.Allocator) {
	if r.Path != "" {
		mem.FreeString(a, r.Path)
	}
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
	ResourceInputs []PlanInput
	Owned          bool
	Waiting        bool
	Diagnostic     diagnostic.Diagnostic
}
type renderDependencyState struct {
	Program *Program
	Index   int
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
	if state.Name != "" {
		mem.FreeString(a, state.Name)
	}
	mem.Free(a, state)
}
func freeExternalValueState(a mem.Allocator, value any) {
	state := value.(*externalValueState)
	if state.Name != "" {
		mem.FreeString(a, state.Name)
	}
	mem.Free(a, state)
}

func produceExternalFile(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalFileState)
	name := state.Program.canonicalTarget(state.Name, true)
	_, err := os.Stat(name)
	mem.FreeString(state.Program.Alloc, name)
	// A missing path is an observed dependency, not a failed producer. Declared
	// inputs still fail before execution. The cache records an explicit missing
	// marker and invalidates when the path later appears.
	if err == os.ErrNotExist {
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	if err != nil {
		c.Fail(failure(state.Program.Alloc, "TGT_NO_RULE", "required input does not exist: "+state.Name))
		return core.ProducerFailed
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
			_, err := os.Stat(name)
			mem.FreeString(p.Alloc, name)
			if err == nil {
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
		if p.Instances[i].Plan.Target == name {
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
	if len(ready) != 0 {
		slices.Free(p.Alloc, ready)
	}
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

func (h *Handle) Poll() HandleResult {
	if h == nil || h.Program == nil || h.Node == nil {
		return HandleResult{Done: true, Result: Result{Diagnostic: failure(mem.System, "TGT_NO_RULE", "invalid handle")}}
	}
	if h.Node.State != core.NodeComplete && h.Node.State != core.NodeFailed && h.Node.State != core.NodeCancelled {
		return HandleResult{}
	}
	if h.Root != nil {
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
		if entry.Rule == plan.Rule && sameCaptures(entry.Captures, plan.Captures) {
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

func produce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*instanceState)
	p := state.Program
	entry := &p.Instances[state.Index]
	if !entry.started || entry.startedGeneration != c.Generation() {
		if len(entry.CacheStdout) != 0 {
			slices.Free(p.Alloc, entry.CacheStdout)
		}
		if len(entry.CacheStderr) != 0 {
			slices.Free(p.Alloc, entry.CacheStderr)
		}
		entry.CacheStdout, entry.CacheStderr, entry.CacheStdoutTruncated, entry.CacheStderrTruncated, entry.CacheReady = nil, nil, false, false, false
		if entry.Script != "" {
			mem.FreeString(p.Alloc, entry.Script)
			entry.Script = ""
		}
		freeStrings(p.Alloc, entry.Operations)
		entry.Operations = nil
		if entry.runEpoch != 0 && entry.satisfiedEpoch < entry.runEpoch {
			entry.satisfiedEpoch = entry.runEpoch
		}
		p.emitNode(entry.Node, entry.Plan.Target, TargetStarted, diagnostic.Span{}, nil)
		entry.started, entry.startedGeneration, entry.terminalEmitted = true, c.Generation(), false
	}
	if entry.Rule.Kind == rule.ServiceRule {
		c.Fail(failure(p.Alloc, "FEATURE_UNSUP", "service execution is not supported"))
		return core.ProducerFailed
	}
	if c.Completion().RequestID != 0 && entry.Script != "" {
		if entry.Script != "" {
			mem.FreeString(p.Alloc, entry.Script)
		}
		entry.Script = ""
		if c.Completion().Diagnostic.Code != "" {
			c.Fail(c.Completion().Diagnostic)
			return core.ProducerFailed
		}
		if entry.Rule.Kind == rule.FileRule {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil {
					c.Fail(failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i]))
					return core.ProducerFailed
				}
			}
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
	}
	resolvedInputs := p.resolveInputs(c, entry)
	inputs, resourceInputs := resolvedInputs.Inputs, resolvedInputs.ResourceInputs
	defer freeOwnedStrings(p.Alloc, inputs, resolvedInputs.Owned)
	defer freePlanInputs(p.Alloc, resourceInputs, resolvedInputs.Owned)
	if resolvedInputs.Waiting {
		return core.ProducerWaiting
	}
	if resolvedInputs.Diagnostic.Code != "" {
		c.Fail(resolvedInputs.Diagnostic)
		return core.ProducerFailed
	}
	for i := range inputs {
		input := inputs[i]
		kind := core.ResourceTarget
		if i < len(resourceInputs) {
			kind = resourceInputs[i].Key.Kind
		}
		if kind == core.ResourceFile || (kind == core.ResourceTarget && isFileName(input)) {
			resolved := p.instanceFor(input)
			entry = &p.Instances[state.Index]
			resolved.Diagnostic.Free(p.Alloc)
			if resolved.Node != nil {
				resolved.Plan.Free(p.Alloc)
				if !p.prepareDependency(c, state.Index, resolved.Node) {
					return core.ProducerWaiting
				}
				continue
			}
			resolved.Plan.Free(p.Alloc)
			name := p.canonicalTarget(input, true)
			_, err := os.Stat(name)
			mem.FreeString(p.Alloc, name)
			if err != nil {
				c.Fail(failure(p.Alloc, "TGT_NO_RULE", "required input does not exist: "+input))
				return core.ProducerFailed
			}
			continue
		}
		resolved := p.instanceFor(input)
		entry = &p.Instances[state.Index]
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code == "" && dep != nil {
			if !p.prepareDependency(c, state.Index, dep) {
				return core.ProducerWaiting
			}
			continue
		}
		d.Free(p.Alloc)
		definition := p.Eval.Definition(input)
		if definition == nil {
			c.Fail(failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+input))
			return core.ProducerFailed
		}
		definitionNode := p.definitionNode(definition.Key.Name)
		if definitionNode == nil || !p.addDependency(c, entry, definitionNode) {
			return core.ProducerWaiting
		}
	}
	entry = &p.Instances[state.Index]
	rendered := p.render(c, entry, inputs)
	commands, effects, writePaths, d := rendered.Commands, rendered.Effects, rendered.WritePaths, rendered.Diagnostic
	if len(entry.LineSpans) != 0 {
		slices.Free(p.Alloc, entry.LineSpans)
	}
	entry.LineSpans = rendered.LineSpans
	defer eval.FreeEffects(p.Alloc, effects)
	defer freeStrings(p.Alloc, writePaths)
	if rendered.Waiting {
		return core.ProducerWaiting
	}
	if d.Code != "" {
		c.Fail(d)
		return core.ProducerFailed
	}
	if entry.Rule.Kind == rule.FileRule {
		entry.Plan.Freshness = p.freshness(&entry.Plan, entry.Node)
	} else {
		entry.Plan.Freshness = Stale
	}
	if entry.Rule.Kind == rule.CachedTaskRule && !p.Options.CacheDisabled && !p.Options.Force && !p.Options.DryRun {
		entry.CacheReady = p.cacheFingerprint(entry, commands)
		if !p.cacheBlockedByBareTask(entry) {
			record := p.cacheLoad(entry, entry.CacheFingerprint[:])
			if record.Identity != "" {
				entry.Plan.Freshness = Fresh
				p.emitCachedLog(entry, Stdout, record.Stdout, record.StdoutTruncated)
				p.emitCachedLog(entry, Stderr, record.Stderr, record.StderrTruncated)
				record.Free(p.Alloc)
				if commands != "" {
					mem.FreeString(p.Alloc, commands)
				}
				c.Publish(core.Value{Kind: core.Nil})
				return core.ProducerCompleted
			}
		}
	}
	if entry.Plan.Freshness == Fresh && !p.Options.Force && !p.Options.DryRun {
		if commands != "" {
			mem.FreeString(p.Alloc, commands)
		}
		if entry.Rule.Kind == rule.FileRule {
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
	}
	if hasYield(effects) && commands != "" {
		if commands != "" {
			mem.FreeString(p.Alloc, commands)
		}
		c.Fail(failureAt(p.Alloc, "OUTPUT_CONFLICT", yieldSpan(effects), "yield cannot be combined with shell commands"))
		return core.ProducerFailed
	}
	if effectDiagnostic := validateEffects(p.Alloc, entry, effects); effectDiagnostic.Code != "" {
		if commands != "" {
			mem.FreeString(p.Alloc, commands)
		}
		c.Fail(effectDiagnostic)
		return core.ProducerFailed
	}
	if p.Options.DryRun {
		p.commitEffects(entry, effects, writePaths, true)
		if commands != "" {
			mem.FreeString(p.Alloc, commands)
		}
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	if effectDiagnostic := p.commitEffects(entry, effects, writePaths, false); effectDiagnostic.Code != "" {
		if commands != "" {
			mem.FreeString(p.Alloc, commands)
		}
		c.Fail(effectDiagnostic)
		return core.ProducerFailed
	}
	if commands == "" {
		if entry.Rule.Kind == rule.FileRule && !hasYield(effects) {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil {
					c.Fail(failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i]))
					return core.ProducerFailed
				}
			}
		}
		if entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && !p.Options.CacheDisabled {
			p.cacheCommit(entry, nil, nil, false, false)
		}
		if entry.Rule.Kind == rule.FileRule && hasYield(effects) {
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
	}
	if entry.Rule.Kind == rule.FileRule {
		for i := range entry.Plan.Outputs {
			name := p.canonicalTarget(entry.Plan.Outputs[i], true)
			ok := mkdirParent(name)
			mem.FreeString(p.Alloc, name)
			if !ok {
				c.Fail(failure(p.Alloc, "FS_ERR", "cannot create output directory"))
				return core.ProducerFailed
			}
		}
	}
	p.nextRequest++
	entry.Script = commands
	retain := p.Options.RetainBytes
	if entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain {
		retain = p.Options.CacheRetainBytes
	}
	entry.cacheStartedAt = time.Now().UnixNano()
	entry.retryCount = 0
	request := posix.Request{ID: p.nextRequest, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
	if !p.Host.Start(request) {
		c.Fail(failure(p.Alloc, "HOST_FAIL", "cannot start recipe"))
		return core.ProducerFailed
	}
	c.Submit(request.ID)
	return core.ProducerSubmitted
}

func shellValue(a mem.Allocator, event posix.Event) core.Value {
	fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: int64(event.Status)}}, {Key: "stdout", Value: core.NewBytes(a, event.Stdout)}, {Key: "stderr", Value: core.NewBytes(a, event.Stderr)}}
	value := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return value
}

func (p *Program) instanceForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].Node.HostRequestID == id {
			return &p.Instances[i]
		}
	}
	return nil
}
