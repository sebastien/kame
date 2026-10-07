package program

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// File reuse and publication use one accepted signature record. The host only
// transports resource facts and opaque record bytes, never freshness decisions.
type fileContextState struct {
	Cached        cacheRecord
	Rendered      renderResult
	Stored        core.SignatureRecord
	Phase         int
	Index         int
	Pending       bool
	PendingKey    core.ResourceKey
	PendingAspect core.ObservationAspect
	Checkpoint    core.DependencyCheckpoint
	Preflight     bool
}

func (p *Program) freeFileContext(state *fileContextState) {
	if state == nil {
		return
	}
	mem.FreeString(p.Alloc, state.Rendered.Commands)
	eval.FreeEffects(p.Alloc, state.Rendered.Effects)
	freeStrings(p.Alloc, state.Rendered.WritePaths)
	slices.Free(p.Alloc, state.Rendered.LineSpans)
	state.Rendered.Diagnostic.Free(p.Alloc)
	state.Stored.Free(p.Alloc)
	state.Cached.Free(p.Alloc)
	state.PendingKey.Free(p.Alloc)
	state.Checkpoint.Free(p.Alloc)
	mem.Free(p.Alloc, state)
}

func appendAcceptedObservation(a mem.Allocator, values []core.Observation, item core.Observation) []core.Observation {
	for i := range values {
		if values[i].Key.Kind == item.Key.Kind && values[i].Key.Name == item.Key.Name && values[i].Aspect == item.Aspect {
			if !values[i].Signature.Equal(item.Signature) {
				values[i].Signature = core.Signature{}
			}
			return values
		}
	}
	item.Key = item.Key.Clone(a)
	return slices.Append(a, values, item)
}

func (p *Program) collectAcceptedInputs(node *core.Node, values *[]core.Observation, seen *[]*core.Node) {
	if slices.Contains(*seen, node) {
		return
	}
	*seen = slices.Append(p.Alloc, *seen, node)
	for i := range node.Observations {
		item := node.Observations[i]
		if item.Key.Kind == core.ResourceDefinition {
			item.Key.Name = eval.AuthoredDefinitionName(item.Key.Name)
		}
		*values = appendAcceptedObservation(p.Alloc, *values, item)
	}
	for i := range node.Dynamic {
		// A generated prerequisite is consumed through its published artifact
		// signature, not through the producer's own implementation and inputs.
		if !slices.Contains(node.OrderOnly, node.Dynamic[i]) && p.instanceIndex(node.Dynamic[i]) < 0 {
			p.collectAcceptedInputs(node.Dynamic[i], values, seen)
		}
	}
	for i := range node.Static {
		if p.instanceIndex(node.Static[i]) < 0 {
			p.collectAcceptedInputs(node.Static[i], values, seen)
		}
	}
}

func (p *Program) acceptedInputs(entry *instance) []core.Observation {
	var inputs []core.Observation
	var seen []*core.Node
	p.collectAcceptedInputs(entry.Node, &inputs, &seen)
	slices.Free(p.Alloc, seen)
	return inputs
}

// Process/tool identity proves how a recipe ran, not that an inputless file
// recipe has become declarative. Actual consumed files and globs do qualify.
func fileHasMaterialInputs(entry *instance) bool {
	inputs, resources := entry.Plan.Inputs, entry.Plan.ResourceInputs
	if entry.Plan.Resolved {
		inputs, resources = entry.Plan.ResolvedInputs, entry.Plan.ResolvedResourceInputs
	}
	for i := range inputs {
		if i >= len(resources) || !resources[i].OrderOnly {
			return true
		}
	}
	for i := range entry.AcceptedRecord.Inputs {
		key := entry.AcceptedRecord.Inputs[i].Key
		if key.Kind == core.ResourceFile || key.Kind == core.ResourceGlob || key.Kind == core.ResourceDefinition || (key.Kind == core.ResourceEnvironment && key.Name != eval.ProcessEnvironmentName) {
			return true
		}
	}
	for i := range entry.Node.Dynamic {
		dependency := entry.Node.Dynamic[i]
		if !slices.Contains(entry.Node.OrderOnly, dependency) && (dependency.Key.Kind == core.ResourceFile || dependency.Key.Kind == core.ResourceGlob) {
			return true
		}
	}
	return false
}

func (p *Program) fileImplementation(entry *instance, rendered renderResult) core.Signature {
	var sink hashSink
	sink.state = newSHA256()
	sink.appendText("kame-file-signatures-v2")
	sink.appendText(entry.Executor)
	sink.appendText(entry.ExecutorVersion)
	sink.appendU64(uint64(len(entry.Shell)))
	for i := range entry.Shell {
		sink.appendText(entry.Shell[i])
	}
	// Authored assignments are explicit execution inputs, unlike unread
	// ambient environment entries.
	sink.appendU64(uint64(len(entry.MetadataEnvironment)))
	for i := range entry.MetadataEnvironment {
		sink.appendText(entry.MetadataEnvironment[i])
	}
	sink.appendU64(uint64(len(entry.Rule.Environment)))
	for i := range entry.Rule.Environment {
		sink.appendText(entry.Rule.Environment[i].Value)
	}
	// Inherited rule-authored overrides remain explicit execution inputs.
	var assignments []string
	for i := range entry.Environment {
		if !slices.Contains(p.Options.Environment, entry.Environment[i]) {
			assignments = slices.Append(p.Alloc, assignments, entry.Environment[i])
		}
	}
	sink.appendU64(uint64(len(assignments)))
	for i := range assignments {
		sink.appendText(assignments[i])
	}
	slices.Free(p.Alloc, assignments)
	sink.appendU64(uint64(p.Options.TimeoutMS))
	sink.appendU64(uint64(p.Options.RetryCount))
	if entry.Kash || entry.NewerInputs != nil {
		// Kash evaluates values during execution; newer-input command text is
		// intentionally transient. Their stable implementation is authored source.
		sink.appendU64(uint64(len(entry.Rule.Body)))
		for i := range entry.Rule.Body {
			sink.appendText(entry.Rule.Body[i].Text)
		}
		if entry.Kash && rendered.Commands != "" {
			// Kash evaluates helper calls while executing. Until their code has
			// individual observations, code edits must not reuse opaque commands.
			source := p.sourceSignature()
			sink.state.Write(source.Digest[:])
		}
	} else {
		sink.appendText(rendered.Commands)
	}
	// Template effects are evaluated before either interpreter is handed a recipe.
	sink.appendU64(uint64(len(rendered.Effects)))
	for i := range rendered.Effects {
		sink.appendU64(uint64(rendered.Effects[i].Kind))
		sink.appendU64(uint64(len(rendered.Effects[i].Data)))
		sink.state.Write(rendered.Effects[i].Data)
	}
	sink.appendU64(uint64(len(rendered.WritePaths)))
	for i := range rendered.WritePaths {
		sink.appendText(rendered.WritePaths[i])
	}
	signature := core.Signature{Mode: core.SignatureContent}
	sink.state.Sum(signature.Digest[:])
	return signature
}

func (p *Program) beginFileContext(c *core.EngineContext, index int, rendered renderResult, preflight bool) core.ProducerResult {
	entry := &p.Instances[index]
	state := mem.Alloc[fileContextState](p.Alloc)
	state.Rendered = rendered
	state.Preflight = preflight
	state.Checkpoint = c.CheckpointDependencies()
	entry.AcceptedRecord.Free(p.Alloc)
	entry.AcceptedRecord.Implementation = p.fileImplementation(entry, rendered)
	if entry.Rule.Kind == rule.FileRule {
		entry.AcceptedRecord.Guard = p.fileGuard(entry)
	}
	if entry.Rule.Kind == rule.CachedTaskRule {
		entry.FileContext = state
		state.Phase = -1
		entry.CacheReady = true
		return p.continueFileContext(c, index)
	}
	var identity hashSink
	identity.state = newSHA256()
	identity.appendText("kame-file-context-v1")
	name := p.canonicalTarget(entry.Plan.Outputs[0], true)
	identity.appendText(name)
	mem.FreeString(p.Alloc, name)
	identity.state.Sum(entry.FileContextKey[:])
	entry.FileContext = state
	if p.Forwarding {
		p.submitFileContextRequest(c, host.RequestCacheGet, host.CacheGetPayload(p.Alloc, entry.FileContextKey[:]))
		return core.ProducerSubmitted
	}
	name = p.fileContextPath(entry)
	data, err := p.Host.ReadFile(p.Alloc, name)
	mem.FreeString(p.Alloc, name)
	if err == nil {
		core.DecodeSignatureRecord(p.Alloc, data, &state.Stored)
	}
	mem.FreeSlice(p.Alloc, data)
	state.Phase = 1
	return p.continueFileContext(c, index)
}

func (p *Program) submitFileContextRequest(c *core.EngineContext, kind host.RequestKind, payload core.Value) {
	p.nextRequest++
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: kind, Payload: payload})
	c.Submit(p.nextRequest)
}

func contentObservation(value core.Value) core.Signature {
	if value.Kind == core.Bytes {
		return core.ContentSignature(value.Bytes)
	}
	if value.Kind == core.Nil {
		return core.Signature{Mode: core.SignatureMissing}
	}
	return core.Signature{}
}

func hasAcceptedObservation(values []core.Observation, item core.Observation) bool {
	for i := range values {
		if values[i].Key.Kind == item.Key.Kind && values[i].Key.Name == item.Key.Name && values[i].Aspect == item.Aspect {
			return true
		}
	}
	return false
}

func (p *Program) continueFileContext(c *core.EngineContext, index int) core.ProducerResult {
	state := p.Instances[index].FileContext
	if state.Phase == -1 {
		lookup := p.cacheLookup(c, &p.Instances[index])
		if lookup.Waiting {
			return core.ProducerSubmitted
		}
		state.Cached = lookup.Record
		if lookup.Hit {
			core.DecodeSignatureRecord(p.Alloc, lookup.Record.Manifest, &state.Stored)
		}
		state.Phase = 1
	}
	if state.Phase == 2 {
		return p.observeFileOutputs(c, index, false)
	}
	if state.Phase == 0 {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		if completion.Diagnostic.Code == "" && completion.Value.Kind == core.Bytes {
			core.DecodeSignatureRecord(p.Alloc, completion.Value.Bytes, &state.Stored)
		}
		completion.Value.Free(p.Alloc)
		completion.Diagnostic.Free(p.Alloc)
		state.Phase = 1
	}
	if state.Pending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		signature := core.Signature{}
		if completion.Diagnostic.Code == "" {
			if state.PendingAspect == core.ObservationContent {
				signature = contentObservation(completion.Value)
			} else {
				signature = core.ValueSignature(completion.Value)
			}
		}
		c.ObserveAspect(state.PendingKey, state.PendingAspect, signature)
		completion.Value.Free(p.Alloc)
		completion.Diagnostic.Free(p.Alloc)
		state.PendingKey.Free(p.Alloc)
		state.Pending = false
		state.Index++
	}
	if state.Preflight {
		if !state.Stored.Guard.Equal(p.Instances[index].AcceptedRecord.Guard) {
			return p.preflightMiss(c, index)
		}
		p.Instances[index].AcceptedRecord.Implementation = state.Stored.Implementation
	}
	// Restore previously consumed execution-time resources before deciding reuse.
	// Changed implementations do not restore obsolete branch dependencies.
	if state.Phase == 1 && state.Stored.Implementation.Equal(p.Instances[index].AcceptedRecord.Implementation) {
		for state.Index < len(state.Stored.Inputs) {
			item := state.Stored.Inputs[state.Index]
			inputs := p.acceptedInputs(&p.Instances[index])
			present := hasAcceptedObservation(inputs, item)
			core.FreeObservations(p.Alloc, inputs)
			if present {
				state.Index++
				continue
			}
			if item.Key.Kind == core.ResourceOperation {
				signature := core.Signature{}
				for i := range p.Eval.Registry.Items {
					operation := p.Eval.Registry.Items[i]
					if operation.Name == item.Key.Name {
						signature = core.ValueSignature(core.Value{Kind: core.String, Text: operation.Version})
						break
					}
				}
				c.ObserveAspect(item.Key, item.Aspect, signature)
				state.Index++
				continue
			}
			if item.Key.Kind == core.ResourceEnvironment {
				if item.Key.Name == eval.ProcessEnvironmentName {
					environment := p.Instances[index].Environment
					if p.Instances[index].Executor != "" && p.Instances[index].Executor != "local" { environment = p.Instances[index].MetadataEnvironment }
					c.ObserveAspect(item.Key, item.Aspect, eval.ProcessEnvironmentSignature(p.Alloc, environment))
					state.Index++
					continue
				}
				value := core.Value{Kind: core.Nil}
				for i := range p.Instances[index].Environment {
					assignment := p.Instances[index].Environment[i]
					equal := strings.IndexByte(assignment, '=')
					if equal >= 0 && assignment[:equal] == item.Key.Name {
						value = core.Value{Kind: core.String, Text: assignment[equal+1:]}
						break
					}
				}
				c.ObserveAspect(item.Key, item.Aspect, core.ValueSignature(value))
				state.Index++
				continue
			}
			if item.Key.Kind == core.ResourceDefinition {
				if state.Preflight {
					// The guard covers code/overrides; transitive resource leaves below
					// prove derived values unchanged without parsing or rendering them.
					c.ObserveAspect(item.Key, item.Aspect, item.Signature)
					state.Index++
					continue
				}
				context := p.kashContext(c, index)
				dependency := p.Eval.DefinitionWith(eval.AuthoredDefinitionName(item.Key.Name), context)
				p.freeKashContext(context)
				if dependency == nil {
					c.ObserveAspect(item.Key, item.Aspect, core.Signature{})
				} else {
					if !c.TryDependency(dependency.Key) {
						return core.ProducerWaiting
					}
					if c.DependencyDiagnostic(dependency.Key).Code == "" {
						c.Value(dependency.Key)
					}
				}
				state.Index++
				continue
			}
			if item.Key.Kind != core.ResourceFile && item.Key.Kind != core.ResourceGlob && item.Key.Kind != core.ResourceTool {
				// Opaque or no-longer-resolvable execution reads cannot prove reuse.
				c.ObserveAspect(item.Key, item.Aspect, core.Signature{})
				state.Index++
				continue
			}
			observeDefinitionDependency(p, item.Key)
			if dependency := p.instanceIndex(p.Engine.Lookup(item.Key)); dependency >= 0 {
				if !p.claimEnvironment(dependency, p.Instances[index].Environment) {
					p.failRule(c, index, p.environmentFailure(dependency, "restored prerequisite has a different recipe environment"))
					return core.ProducerFailed
				}
			}
			if !c.TryDependency(item.Key) {
				return core.ProducerWaiting
			}
			if c.DependencyDiagnostic(item.Key).Code != "" {
				c.ObserveAspect(item.Key, item.Aspect, core.Signature{})
				state.Index++
				continue
			}
			if item.Aspect == core.ObservationValue && (item.Key.Kind == core.ResourceFile || item.Key.Kind == core.ResourceTool) {
				c.Value(item.Key)
				state.Index++
				continue
			}
			if item.Aspect == core.ObservationContent || item.Key.Kind == core.ResourceGlob {
				if signature := p.dependencyContentSignature(item.Key); signature.Mode != core.SignatureUnavailable {
					c.ObserveAspect(item.Key, item.Aspect, signature)
					state.Index++
					continue
				}
			}
			op := host.OpFileContent
			if item.Aspect == core.ObservationExistence {
				op = host.OpExists
			}
			if item.Aspect == core.ObservationMetadata {
				op = host.OpStat
			}
			if item.Key.Kind == core.ResourceGlob {
				op = host.OpWildcard
			}
			if p.Forwarding {
				state.Pending, state.PendingAspect = true, item.Aspect
				state.PendingKey = item.Key.Clone(p.Alloc)
				p.submitFileContextRequest(c, host.RequestReadFile, host.FilePayload(p.Alloc, op, item.Key.Name))
				return core.ProducerSubmitted
			}
			completion := p.fileCompletion(host.Request{}, op, item.Key.Name)
			signature := core.Signature{}
			if completion.Diagnostic.Code == "" {
				if item.Aspect == core.ObservationContent {
					signature = contentObservation(completion.Value)
				} else {
					signature = core.ValueSignature(completion.Value)
				}
			}
			c.ObserveAspect(item.Key, item.Aspect, signature)
			completion.Value.Free(p.Alloc)
			completion.Diagnostic.Free(p.Alloc)
			state.Index++
		}
	}
	entry := &p.Instances[index]
	entry.Plan.Freshness = Stale
	entry.AcceptedRecord.Inputs = p.acceptedInputs(entry)
	if entry.Rule.Kind == rule.CachedTaskRule {
		return p.finishFileContext(c, index)
	}
	state.Phase = 2
	// The same output observer is used before reuse and after execution.
	entry.VerifyIndex, entry.VerifyPending = 0, false
	return p.observeFileOutputs(c, index, false)
}

func (p *Program) finishFileContext(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	state := entry.FileContext
	material := entry.Rule.Kind != rule.FileRule || fileHasMaterialInputs(entry)
	if material && !entry.Rule.Always && !p.Options.Force && !p.cacheBlockedByBareTask(entry) && state.Stored.Matches(&entry.AcceptedRecord) {
		entry.Plan.Freshness = Fresh
		if entry.Rule.Kind == rule.CachedTaskRule {
			p.emitCachedLog(entry, Stdout, state.Cached.Stdout, state.Cached.StdoutTruncated)
			p.emitCachedLog(entry, Stderr, state.Cached.Stderr, state.Cached.StderrTruncated)
			p.releaseCacheLock(entry)
		}
	}
	if entry.Plan.Freshness != Fresh {
		if state.Preflight {
			return p.preflightMiss(c, index)
		}
		c.RestoreDependencies(&state.Checkpoint)
		core.FreeObservations(p.Alloc, entry.AcceptedRecord.Inputs)
		entry.AcceptedRecord.Inputs = p.acceptedInputs(entry)
	}
	if entry.Plan.Freshness == Fresh && entry.Rule.Kind == rule.FileRule && !state.Stored.Guard.Equal(entry.AcceptedRecord.Guard) {
		// A conservative source miss can still yield the same implementation.
		// Refresh its guard so the next invocation need not render again.
		p.saveAcceptedFileRecord(entry)
	}
	rendered := state.Rendered
	state.Rendered = renderResult{}
	p.freeFileContext(state)
	entry.FileContext, entry.FileContextReady = nil, true
	return p.finishRenderedRule(c, index, rendered)
}

func (p *Program) fileContextPath(entry *instance) string {
	hex := cacheHex(p.Alloc, entry.FileContextKey[:])
	root := path.Join(p.Alloc, p.Options.Directory, ".kame/cache/file-context")
	name := path.Join(p.Alloc, root, hex)
	mem.FreeString(p.Alloc, hex)
	mem.FreeString(p.Alloc, root)
	return name
}

func (p *Program) saveAcceptedFileRecord(entry *instance) {
	core.FreeObservations(p.Alloc, entry.AcceptedRecord.Inputs)
	entry.AcceptedRecord.Inputs = p.acceptedInputs(entry)
	for i := range entry.AcceptedRecord.Inputs {
		if entry.AcceptedRecord.Inputs[i].Key.Name == eval.UntrackedHostRead {
			entry.AcceptedRecord.Guard = core.Signature{}
		}
	}
	data := core.EncodeSignatureRecord(p.Alloc, &entry.AcceptedRecord)
	if len(data) == 0 {
		return
	}
	if p.Forwarding {
		p.nextRequest++
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestCachePut, Payload: host.CachePutPayload(p.Alloc, entry.FileContextKey[:], data)})
	} else {
		name := p.fileContextPath(entry)
		if p.mkdirParent(name) {
			_ = p.Host.WriteFileAtomic(name, data, 0o644, true)
		}
		mem.FreeString(p.Alloc, name)
	}
	slices.Free(p.Alloc, data)
}

func fileResultSignature(record *core.SignatureRecord) core.Signature {
	if len(record.Outputs) == 1 {
		return record.Outputs[0].Signature
	}
	d := core.NewDigest()
	d.Text("kame-file-output-set-v1")
	for i := range record.Outputs {
		if !record.Outputs[i].Signature.Equal(record.Outputs[i].Signature) {
			return core.Signature{}
		}
		d.Text(record.Outputs[i].Key.Name)
		d.Write(record.Outputs[i].Signature.Digest[:])
	}
	s := core.Signature{Mode: core.SignatureContent}
	d.Sum(s.Digest[:])
	return s
}

// Timestamp observations remain only for the intentional newer-input selector,
// not for deciding whether a computation or artifact can be reused.
func (p *Program) submitFileTimes(c *core.EngineContext, names []string) {
	b := strings.NewBuilder(p.Alloc)
	e := json.NewEncoder(&b)
	e.BeginArray()
	for i := range names {
		e.Str(names[i])
	}
	e.EndArray()
	e.Flush()
	payload := host.FilePayload(p.Alloc, host.OpFileTimes, b.String())
	b.Free()
	p.submitFileContextRequest(c, host.RequestReadFile, payload)
}

func contextTime(value core.Value, out *int64) bool {
	if value.Kind == core.Int {
		*out = value.Int
		return true
	}
	if value.Kind != core.String {
		return false
	}
	n, err := strconv.ParseInt(value.Text, 10, 64)
	if err != nil {
		return false
	}
	*out = n
	return true
}
