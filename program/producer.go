package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/time"
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
	request := host.ProcessRequest{ID: p.nextRequest, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
	if p.Host == nil || !p.Host.Start(request) {
		c.Fail(failure(p.Alloc, "HOST_FAIL", "cannot start recipe"))
		return core.ProducerFailed
	}
	c.Submit(request.ID)
	return core.ProducerSubmitted
}

