package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// produce advances one rule instance through dependency resolution, rendering,
// cache lookup, effects, and process submission. Materialize and Handle lifecycle
// operations remain in materialize.go.

func produce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*instanceState)
	p := state.Program
	entry := &p.Instances[state.Index]
	if !entry.started || entry.startedGeneration != c.Generation() {
		p.clearReasons(entry)
		slices.Free(p.Alloc, entry.CacheStdout)
		slices.Free(p.Alloc, entry.CacheStderr)
		entry.CacheStdout, entry.CacheStderr, entry.CacheStdoutTruncated, entry.CacheStderrTruncated, entry.CacheReady = nil, nil, false, false, false
		entry.cachePending = false
		entry.KashRunning = false
		entry.KashPrepared, entry.KashPreparing = false, false
		if entry.KashContext != nil {
			p.freeKashContext(entry.KashContext)
			entry.KashContext = nil
		}
		entry.EnvironmentConflict = false
		entry.ServiceReady = false
		entry.ServiceState = ""
		entry.ServiceProcessID = 0
		entry.ServiceReadyDeadline, entry.ServiceNextProbe, entry.ServiceProbeID = 0, 0, 0
		entry.ServiceClockID, entry.ServiceTimerID = 0, 0
		entry.ServiceProbeHealth, entry.ServiceNextHealth, entry.ServiceHealthFailures = false, 0, 0
		entry.ServiceRestartTimerID = 0
		if !entry.ServiceRestartPending {
			entry.ServiceRestartCount = 0
		}
		p.freeNewerInputs(entry.NewerInputs)
		entry.NewerInputs = nil
		p.freeFileContext(entry.FileContext)
		entry.FileContext = nil
		entry.FileContextReady = false
		entry.PreflightChecked = false
		p.freeForwardEffects(entry.ForwardEffects)
		entry.ForwardEffects = nil
		entry.VerifyOutputs, entry.VerifyPending, entry.VerifyIndex = false, false, 0
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
		p.setServiceState(entry, "provisioning")
	}
	if entry.FileContext != nil {
		return p.continueFileContext(c, state.Index)
	}
	if entry.VerifyOutputs {
		return p.verifyForwardOutputs(c, state.Index)
	}
	if entry.ForwardEffects != nil {
		return p.continueForwardEffects(c, state.Index)
	}
	if entry.KashRunning {
		return p.continueKashRecipe(c, state.Index)
	}
	if c.Completion().RequestID != 0 && entry.Script != "" {
		mem.FreeString(p.Alloc, entry.Script)
		entry.Script = ""
		if entry.Rule.Kind == rule.ServiceRule {
			completion := c.Completion()
			message := "service exited before becoming ready"
			code := "SERVICE_START_FAILED"
			if entry.ServiceReady {
				message, code = "service exited unexpectedly", "SERVICE_EXITED"
			}
			if p.prepareServiceRestart(entry) {
				completion.Value.Free(p.Alloc)
				completion.Diagnostic.Free(p.Alloc)
				return core.ProducerRestart
			}
			completion.Value.Free(p.Alloc)
			completion.Diagnostic.Free(p.Alloc)
			if entry.ServiceRestartCount > 0 {
				message, code = "service restart attempts were exhausted", "SERVICE_RESTART_EXHAUSTED"
			}
			p.failRule(c, state.Index, failure(p.Alloc, code, message))
			return core.ProducerFailed
		}
		if c.Completion().Diagnostic.Code != "" {
			p.failRule(c, state.Index, c.Completion().Diagnostic)
			return core.ProducerFailed
		}
		if entry.Rule.Kind == rule.FileRule {
			return p.beginVerifyFileOutputs(c, state.Index)
		}
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	for i := range entry.SettingsDependencies {
		dependency := p.Eval.Definition(entry.SettingsDependencies[i])
		// Resolved settings are fingerprinted by fileImplementation. Scheduling
		// their definitions is order-only: constructors can be unhashable, and
		// an unobserved normal edge would prevent equal-result watch revalidation.
		if dependency != nil && !c.OrderDependency(dependency.Key) {
			return core.ProducerWaiting
		}
	}
	resolvedInputs := p.resolveInputs(c, entry)
	inputs, resourceInputs := resolvedInputs.Inputs, resolvedInputs.ResourceInputs
	defer freeOwnedStrings(p.Alloc, inputs, resolvedInputs.Owned)
	defer freeStrings(p.Alloc, resolvedInputs.DynamicInputs)
	defer freePlanInputs(p.Alloc, resourceInputs, resolvedInputs.Owned)
	if p.Instances[state.Index].EnvironmentConflict {
		p.failRule(c, state.Index, failure(p.Alloc, "ENV_CONFLICT", "shared prerequisite has a different recipe environment"))
		return core.ProducerFailed
	}
	if resolvedInputs.Waiting {
		return core.ProducerWaiting
	}
	if resolvedInputs.Diagnostic.Code != "" {
		p.failRule(c, state.Index, resolvedInputs.Diagnostic)
		return core.ProducerFailed
	}
	groupReady := true
	for i := range inputs {
		input := inputs[i]
		kind := core.ResourceTarget
		ordered := i < len(resourceInputs) && resourceInputs[i].OrderOnly
		if i < len(resourceInputs) {
			kind = resourceInputs[i].Key.Kind
		}
		if kind == core.ResourceFile || (kind == core.ResourceTarget && isFileName(input)) {
			resolved := p.instanceFor(input)
			entry = &p.Instances[state.Index]
			if resolved.Diagnostic.Code != "" && resolved.Diagnostic.Code != "TGT_NO_RULE" {
				resolved.Plan.Free(p.Alloc)
				p.failRule(c, state.Index, resolved.Diagnostic)
				return core.ProducerFailed
			}
			resolved.Diagnostic.Free(p.Alloc)
			if resolved.Node != nil {
				resolved.Plan.Free(p.Alloc)
				if !p.prepareDependency(c, state.Index, resolved.Node, ordered) {
					groupReady = false
				}
				if i < len(resourceInputs) && resourceInputs[i].SequenceEnd {
					if !groupReady {
						return core.ProducerWaiting
					}
					groupReady = true
				}
				continue
			}
			resolved.Plan.Free(p.Alloc)
			name := p.canonicalTarget(input, true)
			key := core.ResourceKey{Kind: core.ResourceFile, Name: name}
			dependency := p.Engine.Lookup(key)
			if dependency == nil {
				external := mem.Alloc[externalFileState](p.Alloc)
				external.Program, external.Name = p, cloneText(p.Alloc, name)
				dependency = p.Engine.AddOwned(key, produceExternalFile, external, freeExternalFileState)
			}
			ready := p.prepareDependency(c, state.Index, dependency, ordered)
			if !ready {
				groupReady = false
			}
			mem.FreeString(p.Alloc, name)
			if ready && (!dependency.Current || dependency.Latest.Kind == core.Nil) {
				p.failRule(c, state.Index, failure(p.Alloc, "TGT_NO_RULE", "required input does not exist: "+input))
				return core.ProducerFailed
			}
			if i < len(resourceInputs) && resourceInputs[i].SequenceEnd {
				if !groupReady {
					return core.ProducerWaiting
				}
				groupReady = true
			}
			continue
		}
		resolved := p.instanceFor(input)
		entry = &p.Instances[state.Index]
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code == "" && dep != nil {
			if !p.prepareDependency(c, state.Index, dep, ordered) {
				groupReady = false
			}
			if i < len(resourceInputs) && resourceInputs[i].SequenceEnd {
				if !groupReady {
					return core.ProducerWaiting
				}
				groupReady = true
			}
			continue
		}
		if d.Code != "" && d.Code != "TGT_NO_RULE" {
			p.failRule(c, state.Index, d)
			return core.ProducerFailed
		}
		d.Free(p.Alloc)
		definition := p.Eval.Definition(input)
		if definition == nil {
			p.failRule(c, state.Index, failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+input))
			return core.ProducerFailed
		}
		definitionContext := &eval.Context{Environment: entry.Environment, HasEnvironment: entry.EnvironmentClaimed, Cwd: p.Options.Directory, Phase: eval.EvaluatePhase}
		p.bindDefinitionEnvironment(definitionContext)
		definitionNode := p.Eval.DefinitionWith(definition.Key.Name, definitionContext)
		if definitionNode == nil {
			return core.ProducerWaiting
		}
		if !p.addPurposeDependency(c, entry, definitionNode, ordered) {
			groupReady = false
		}
		if i < len(resourceInputs) && resourceInputs[i].SequenceEnd {
			if !groupReady {
				return core.ProducerWaiting
			}
			groupReady = true
		}
	}
	if !groupReady {
		return core.ProducerWaiting
	}
	entry = &p.Instances[state.Index]
	if entry.Rule.Kind == rule.FileRule && !entry.PreflightChecked && !entry.Rule.Always && !p.Options.Force && !p.Options.DryRun {
		return p.beginFileContext(c, state.Index, renderResult{}, true)
	}
	// Scan the combined source so selectors used through definitions are covered.
	// False positives only add metadata reads; literal source is never evaluated.
	if entry.Rule.Kind == rule.FileRule && strings.Contains(p.Parsed.Source.Text, "@<?") {
		prepared := p.prepareNewerInputs(c, state.Index, inputs, resourceInputs)
		if prepared != core.ProducerCompleted {
			return prepared
		}
	}
	rendered := p.render(c, entry, inputs)
	// Rendering may discover a file producer and grow the instance slice.
	entry = &p.Instances[state.Index]
	return p.finishRenderedRule(c, state.Index, rendered)
}

func (p *Program) finishRenderedRule(c *core.EngineContext, index int, rendered renderResult) core.ProducerResult {
	entry := &p.Instances[index]
	if !entry.Kash && rendered.Commands != "" && !rendered.Waiting && rendered.Diagnostic.Code == "" {
		// Opaque recipes consume the child snapshot; Kash records it on process submission.
		environment := entry.Environment
		if entry.Executor != "" && entry.Executor != "local" { environment = entry.MetadataEnvironment }
		c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: eval.ProcessEnvironmentName}, eval.ProcessEnvironmentSignature(p.Alloc, environment))
	}
	if (entry.Rule.Kind == rule.FileRule || (entry.Rule.Kind == rule.CachedTaskRule && !p.Options.CacheDisabled && !p.Options.Force)) && !rendered.Waiting && rendered.Diagnostic.Code == "" && !entry.EnvironmentConflict && !entry.FileContextReady && !p.Options.DryRun {
		return p.beginFileContext(c, index, rendered, false)
	}
	commands, effects, writePaths, d := rendered.Commands, rendered.Effects, rendered.WritePaths, rendered.Diagnostic
	slices.Free(p.Alloc, entry.LineSpans)
	entry.LineSpans = rendered.LineSpans
	defer eval.FreeEffects(p.Alloc, effects)
	defer freeStrings(p.Alloc, writePaths)
	if entry.EnvironmentConflict {
		mem.FreeString(p.Alloc, commands)
		d.Free(p.Alloc)
		p.failRule(c, index, failure(p.Alloc, "ENV_CONFLICT", "shared prerequisite has a different recipe environment"))
		return core.ProducerFailed
	}
	if rendered.Waiting {
		return core.ProducerWaiting
	}
	if d.Code != "" {
		p.failRule(c, index, d)
		return core.ProducerFailed
	}
	if entry.Kash {
		if parsed := p.validateRenderedKash(index, commands); parsed.Code != "" {
			mem.FreeString(p.Alloc, commands)
			p.failRule(c, index, parsed)
			return core.ProducerFailed
		}
	}
	if !entry.FileContextReady {
		entry.Plan.Freshness = Stale
	}
	if entry.Rule.Kind == rule.ServiceRule && entry.ServiceRestartPending && entry.ServiceRestartTimerID == 0 {
		mem.FreeString(p.Alloc, commands)
		return p.waitServiceRestart(c, entry)
	}
	if entry.Plan.Freshness == Fresh && !p.Options.Force && !p.Options.DryRun {
		mem.FreeString(p.Alloc, commands)
		if entry.Rule.Kind == rule.FileRule {
			c.PublishSigned(core.NewString(c.Allocator(), entry.Plan.Outputs[0]), fileResultSignature(&entry.AcceptedRecord))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
	}
	if hasYield(effects) && commands != "" {
		mem.FreeString(p.Alloc, commands)
		p.failRule(c, index, failureAt(p.Alloc, "OUTPUT_CONFLICT", yieldSpan(effects), "yield cannot be combined with shell commands"))
		return core.ProducerFailed
	}
	if effectDiagnostic := validateEffects(p.Alloc, entry, effects); effectDiagnostic.Code != "" {
		mem.FreeString(p.Alloc, commands)
		p.failRule(c, index, effectDiagnostic)
		return core.ProducerFailed
	}
	if p.Options.DryRun {
		p.commitEffects(entry, effects, writePaths, true)
		mem.FreeString(p.Alloc, commands)
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	if p.Options.Force {
		p.reason(entry, "execute", "forced", "forced execution", core.ResourceKey{}, "")
	} else if entry.Rule.Always {
		p.reason(entry, "execute", "always", "always rule", core.ResourceKey{}, "")
	} else if entry.Rule.Kind == rule.TaskRule {
		p.reason(entry, "execute", "uncached-task", "uncached task", core.ResourceKey{}, "")
	} else if entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheDisabled {
		p.reason(entry, "execute", "proof-unverifiable", "cache disabled", core.ResourceKey{}, "")
	}
	if p.SessionPolicy && commands != "" && !entry.Kash {
		allowed := false
		for i := range p.Options.Grants {
			if p.Options.Grants[i].Capability == eval.Run && len(p.Options.Grants[i].Names) == 0 {
				allowed = true
			}
		}
		if !allowed {
			mem.FreeString(p.Alloc, commands)
			p.failRule(c, index, failure(p.Alloc, "CAP_DENIED", "shell recipes require unrestricted run capability"))
			return core.ProducerFailed
		}
	}
	if p.Forwarding {
		pending := mem.Alloc[forwardEffectsState](p.Alloc)
		pending.Commands = commands
		pending.WritePaths = cloneStrings(p.Alloc, writePaths)
		pending.HasYield = hasYield(effects)
		for i := range effects {
			pending.Effects = slices.Append(p.Alloc, pending.Effects, eval.Effect{Kind: effects[i].Kind, Span: effects[i].Span, Data: slices.Clone(p.Alloc, effects[i].Data)})
		}
		entry.ForwardEffects = pending
		return p.continueForwardEffects(c, index)
	}
	if effectDiagnostic := p.commitEffects(entry, effects, writePaths, false); effectDiagnostic.Code != "" {
		mem.FreeString(p.Alloc, commands)
		p.failRule(c, index, effectDiagnostic)
		return core.ProducerFailed
	}
	return p.finishRecipe(c, index, commands, hasYield(effects))
}

func (p *Program) finishRecipe(c *core.EngineContext, index int, commands string, yielded bool) core.ProducerResult {
	_ = yielded
	entry := &p.Instances[index]
	if commands == "" {
		if entry.Rule.Kind == rule.FileRule {
			return p.beginVerifyFileOutputs(c, index)
		}
		if entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && !p.Options.CacheDisabled {
			p.cacheCommit(entry, nil, nil, false, false)
		}
		c.Publish(core.Value{Kind: core.Nil})
		return core.ProducerCompleted
	}
	if entry.Rule.Kind == rule.FileRule && !p.Forwarding {
		for i := range entry.Plan.Outputs {
			name := p.canonicalTarget(entry.Plan.Outputs[i], true)
			ok := p.mkdirParent(name)
			mem.FreeString(p.Alloc, name)
			if !ok {
				// commands transfers to entry.Script below; it never got
				// there on this path, so release it here like every other
				// early exit above.
				mem.FreeString(p.Alloc, commands)
				p.failRule(c, index, failure(p.Alloc, "FS_ERR", "cannot create output directory"))
				return core.ProducerFailed
			}
		}
	}
	if entry.Kash {
		entry.retryCount = 0
		entry.cacheStartedAt = p.Host.Now()
		entry.Script, entry.KashRunning = commands, true
		return p.continueKashRecipe(c, index)
	}
	p.nextRequest++
	entry.Script = commands
	retain := p.Options.RetainBytes
	if entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain {
		retain = p.Options.CacheRetainBytes
	}
	timeout := p.Options.TimeoutMS
	if entry.Rule.Kind == rule.ServiceRule {
		retain = int(entry.Service.LogBytes)
		timeout = 0
	}
	entry.cacheStartedAt = p.Host.Now()
	entry.retryCount = 0
	d := p.prepareRemoteExecution(entry)
	if d.Code != "" {
		p.failRule(c, index, d)
		return core.ProducerFailed
	}
	processEnvironment := entry.Environment
	processDirectory, targetIdentity := p.Options.Directory, entry.Plan.Target
	if entry.Executor != "" && entry.Executor != "local" {
		processEnvironment, processDirectory = entry.MetadataEnvironment, "."
		if len(entry.ExecutionOutputs) != 0 {
			targetIdentity = entry.ExecutionOutputs[0]
		}
	}
	request := host.ProcessRequest{ID: p.nextRequest, Target: targetIdentity, Generation: c.Generation(), Attempt: c.Attempt(), Executor: entry.Executor, ExecutorVersion: entry.ExecutorVersion, Shell: entry.Shell, Script: []byte(entry.Script), Directory: processDirectory, Environment: processEnvironment, Inputs: entry.ExecutionInputs, Outputs: entry.ExecutionOutputs, TimeoutMS: timeout, RetainBytes: retain}
	request.IdempotencyKey = entry.ExecutionKey
	if entry.Rule.Kind == rule.ServiceRule {
		entry.ServiceProcessID = request.ID
		p.setServiceState(entry, "starting")
	}
	if p.Forwarding {
		// The embedding host runs the recipe; correlation uses the node so the
		// completion resumes this producer.
		payload := host.RecipeExecutionPayload(p.Alloc, entry.Script, entry.Plan.Outputs, entry.Environment, entry.Shell)
		display := processDisplayFromPayload(p.Alloc, payload, entry.Shell)
		p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: request.ID, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Program: display.Program, Argv: display.Argv, DisplayTruncated: display.Truncated})
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: request.ID, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestProcess, Payload: payload})
		c.Submit(request.ID)
		return core.ProducerSubmitted
	}
	if p.Host == nil || !p.Host.Start(request) {
		p.releaseRemoteExecution(entry)
		if entry.Rule.Kind == rule.ServiceRule && p.prepareServiceRestart(entry) {
			mem.FreeString(p.Alloc, entry.Script)
			entry.Script = ""
			return core.ProducerRestart
		}
		p.failRule(c, index, failure(p.Alloc, "HOST_FAIL", "cannot start recipe"))
		return core.ProducerFailed
	}
	display := processDisplayFromHost(p.Alloc, request)
	p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: request.ID, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Program: display.Program, Argv: display.Argv, DisplayTruncated: display.Truncated})
	c.Submit(request.ID)
	return core.ProducerSubmitted
}
