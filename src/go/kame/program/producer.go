package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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
		slices.Free(p.Alloc, entry.CacheStdout)
		slices.Free(p.Alloc, entry.CacheStderr)
		entry.CacheStdout, entry.CacheStderr, entry.CacheStdoutTruncated, entry.CacheStderrTruncated, entry.CacheReady = nil, nil, false, false, false
		entry.cachePending = false
		entry.EnvironmentConflict = false
		p.freeFileContext(entry.FileContext)
		entry.FileContext = nil
		entry.FileContextReady, entry.FileContextWanted = false, false
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
	}
	if entry.Rule.Kind == rule.ServiceRule {
		p.failRule(c, state.Index, failure(p.Alloc, "FEATURE_UNSUP", "service execution is not supported"))
		return core.ProducerFailed
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
	if c.Completion().RequestID != 0 && entry.Script != "" {
		mem.FreeString(p.Alloc, entry.Script)
		entry.Script = ""
		if c.Completion().Diagnostic.Code != "" {
			p.failRule(c, state.Index, c.Completion().Diagnostic)
			return core.ProducerFailed
		}
		if entry.Rule.Kind == rule.FileRule && p.Forwarding {
			entry.VerifyOutputs = true
			return p.verifyForwardOutputs(c, state.Index)
		}
		// When requests are forwarded, the embedding host owns the filesystem and
		// reports recipe failures through the completion; the local synchronous
		// output check cannot see files the host wrote.
		if entry.Rule.Kind == rule.FileRule && !p.Forwarding {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				result := p.Host.Stat(name)
				mem.FreeString(p.Alloc, name)
				if !result.Exists {
					p.failRule(c, state.Index, failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i]))
					return core.ProducerFailed
				}
			}
		}
		if entry.Rule.Kind == rule.FileRule {
			p.saveNativeFileContext(entry)
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
		return core.ProducerCompleted
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
					return core.ProducerWaiting
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
			if !p.prepareDependency(c, state.Index, dependency, ordered) {
				mem.FreeString(p.Alloc, name)
				return core.ProducerWaiting
			}
			mem.FreeString(p.Alloc, name)
			if !dependency.Current || dependency.Latest.Kind == core.Nil {
				p.failRule(c, state.Index, failure(p.Alloc, "TGT_NO_RULE", "required input does not exist: "+input))
				return core.ProducerFailed
			}
			continue
		}
		resolved := p.instanceFor(input)
		entry = &p.Instances[state.Index]
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code == "" && dep != nil {
			if !p.prepareDependency(c, state.Index, dep, ordered) {
				return core.ProducerWaiting
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
		definitionNode := p.definitionNode(definition.Key.Name)
		if definitionNode == nil || !p.addPurposeDependency(c, entry, definitionNode, ordered) {
			return core.ProducerWaiting
		}
	}
	entry = &p.Instances[state.Index]
	rendered := p.render(c, entry, inputs)
	// Rendering may discover a file producer and grow the instance slice.
	entry = &p.Instances[state.Index]
	return p.finishRenderedRule(c, state.Index, rendered)
}

func (p *Program) finishRenderedRule(c *core.EngineContext, index int, rendered renderResult) core.ProducerResult {
	entry := &p.Instances[index]
	if entry.Rule.Kind == rule.FileRule && !rendered.Waiting && rendered.Diagnostic.Code == "" && !entry.EnvironmentConflict && !entry.FileContextReady && !p.Options.DryRun {
		return p.beginFileContext(c, index, rendered)
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
	if entry.Rule.Kind == rule.FileRule {
		if !entry.FileContextReady {
			entry.Plan.Freshness = p.freshness(&entry.Plan, entry.Node)
		}
		beforeInvalidation := entry.Plan.Freshness
  if entry.Node.InvalidatedForOrderOnly { entry.Plan.Freshness = beforeInvalidation }
		if hasYield(effects) && len(entry.Plan.Outputs) == 1 && !p.Forwarding && !entry.FileContextWanted && !entry.Rule.Always {
			entry.Plan.Freshness = p.yieldFreshness(entry, effects)
		}
	} else {
		entry.Plan.Freshness = Stale
	}
	if entry.Rule.Kind == rule.CachedTaskRule && !p.Options.CacheDisabled && !p.Options.Force && !p.Options.DryRun {
		entry.CacheReady = p.cacheFingerprint(entry, commands)
		if !p.cacheBlockedByBareTask(entry) {
			lookup := p.cacheLookup(c, entry)
			if lookup.Waiting {
				mem.FreeString(p.Alloc, commands)
				return core.ProducerSubmitted
			}
			if lookup.Hit {
				record := lookup.Record
				entry.Plan.Freshness = Fresh
				p.emitCachedLog(entry, Stdout, record.Stdout, record.StdoutTruncated)
				p.emitCachedLog(entry, Stderr, record.Stderr, record.StderrTruncated)
				record.Free(p.Alloc)
				mem.FreeString(p.Alloc, commands)
				c.Publish(core.Value{Kind: core.Nil})
				return core.ProducerCompleted
			}
			lookup.Record.Free(p.Alloc)
		}
	}
	if entry.Plan.Freshness == Fresh && !p.Options.Force && !p.Options.DryRun {
		mem.FreeString(p.Alloc, commands)
		if entry.Rule.Kind == rule.FileRule {
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
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
	if p.SessionPolicy && commands != "" {
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
	entry := &p.Instances[index]
	if commands == "" {
		if entry.Rule.Kind == rule.FileRule && p.Forwarding {
			entry.VerifyOutputs = true
			return p.verifyForwardOutputs(c, index)
		}
		if entry.Rule.Kind == rule.FileRule && !yielded && !p.Forwarding {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				result := p.Host.Stat(name)
				mem.FreeString(p.Alloc, name)
				if !result.Exists {
					p.failRule(c, index, failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i]))
					return core.ProducerFailed
				}
			}
		}
		if entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && !p.Options.CacheDisabled {
			p.cacheCommit(entry, nil, nil, false, false)
		}
		if entry.Rule.Kind == rule.FileRule && !p.Forwarding {
			p.saveNativeFileContext(entry)
		}
		if entry.Rule.Kind == rule.FileRule && yielded {
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else {
			c.Publish(core.Value{Kind: core.Nil})
		}
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
	p.nextRequest++
	entry.Script = commands
	retain := p.Options.RetainBytes
	if entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain {
		retain = p.Options.CacheRetainBytes
	}
	entry.cacheStartedAt = p.Host.Now()
	entry.retryCount = 0
	request := host.ProcessRequest{ID: p.nextRequest, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: entry.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
	if p.Forwarding {
		// The embedding host runs the recipe; correlation uses the node so the
		// completion resumes this producer.
		payload := core.Value{}
		if entry.Rule.Kind == rule.FileRule || entry.ScopedEnvironment {
			payload = host.RecipeEnvironmentPayload(p.Alloc, entry.Script, entry.Plan.Outputs, entry.Environment)
		} else {
			payload = host.ProcessPayload(p.Alloc, entry.Script)
		}
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: request.ID, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestProcess, Payload: payload})
		c.Submit(request.ID)
		return core.ProducerSubmitted
	}
	if p.Host == nil || !p.Host.Start(request) {
		p.failRule(c, index, failure(p.Alloc, "HOST_FAIL", "cannot start recipe"))
		return core.ProducerFailed
	}
	c.Submit(request.ID)
	return core.ProducerSubmitted
}
