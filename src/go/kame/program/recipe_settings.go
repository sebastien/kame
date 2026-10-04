package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

type recipeSettingsResolver struct {
	Program      *Program
	Resolving    []string
	Dependencies []string
}

func invocationRecipePath(a mem.Allocator, retry int) string {
	var buffer [strconv.MaxIntBase10Len]byte
	return cloneText(a, "recipe/"+strconv.Itoa(buffer[:], retry))
}

func resolveRecipeDefinition(value any, key core.ResourceKey, c *eval.Context) eval.Result {
	state := value.(*recipeSettingsResolver)
	if slices.Contains(state.Resolving, key.Name) {
		return eval.Result{Diagnostic: failure(state.Program.Alloc, "DEP_CYCLE", "definition cycle")}
	}
	if !slices.Contains(state.Dependencies, key.Name) {
		state.Dependencies = slices.Append(state.Program.Alloc, state.Dependencies, cloneText(state.Program.Alloc, key.Name))
	}
	state.Resolving = slices.Append(state.Program.Alloc, state.Resolving, key.Name)
	r := state.Program.Eval.EvaluateDefinition(key, c)
	state.Resolving = state.Resolving[:len(state.Resolving)-1]
	return r
}

// Settings are evaluated in planning policy before any dependency recipe runs.
// Registration remains pure; the producer later observes reached definitions.
func (p *Program) recipeSettings(index int) diagnostic.Diagnostic {
	entry := &p.Instances[index]
	state := recipeSettingsResolver{Program: p}
	c := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Args: p.Eval.DefinitionArgs, HasArgs: p.Eval.DefinitionArgsSet, Phase: eval.PlanningPhase, ResolveDefinition: resolveRecipeDefinition, ResolverState: &state}
	scope := p.ruleScope(c, entry.Captures, entry.Plan.Arguments)
	c.Scope = scope
	defer scope.Free()
	defer p.freeSettingsContext(&state, c)
	freeStrings(p.Alloc, entry.SettingsDependencies)
	entry.SettingsDependencies = nil
	defer p.saveSettingsDependencies(index, &state)
	var shell core.Value
	var metadata core.Value
	if entry.Rule.Metadata != nil {
		r := p.Eval.EvaluateWith(entry.Rule.Metadata, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return p.settingsFailure(&r, c)
		}
		metadata = r.Value
	}
	defer metadata.Free(p.Alloc)
	if c.PhaseInvalid() || len(c.Effects) != 0 {
		return failureAt(p.Alloc, "PHASE_INVALID", diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}, "recipe settings cannot perform effects")
	}
	if entry.Rule.Kind == rule.ServiceRule {
		span := diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}
		if entry.Rule.Metadata != nil { span = diagnostic.Span{Start: entry.Rule.Metadata.Span.Start, End: entry.Rule.Metadata.Span.End} }
		var config ServiceConfig
		d := parseServiceConfig(p.Alloc, metadata, span, &config)
		if d.Code != "" { return d }
		entry.Service.Free(p.Alloc)
		entry.Service = config
	}
	explicit := false
	for i := range metadata.Record {
		field := metadata.Record[i]
		if field.Key == "shell" {
			shell, explicit = field.Value, true
		}
	}
	var global eval.Result
	if !explicit && p.Eval.Definition("SHELL") != nil {
		key := core.ResourceKey{Kind: core.ResourceDefinition, Name: "SHELL"}
		global = resolveRecipeDefinition(&state, key, c)
		if global.Waiting || global.Diagnostic.Code != "" {
			return p.settingsFailure(&global, c)
		}
		shell, explicit = global.Value, true
	}
	defer global.Free(p.Alloc)
	if c.PhaseInvalid() || len(c.Effects) != 0 {
		return failureAt(p.Alloc, "PHASE_INVALID", diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}, "recipe settings cannot perform effects")
	}
	freeStrings(p.Alloc, entry.Shell)
	entry.Shell = nil
	entry.Kash = false
	entry.ScopedShell = explicit
	if explicit {
		if p.Eval.IsKashConstructor(shell) {
			entry.Kash = true
		} else if shell.Kind == core.String && shell.Text != "" && strings.IndexByte(shell.Text, 0) < 0 {
			entry.Shell = slices.Append(p.Alloc, entry.Shell, cloneText(p.Alloc, shell.Text))
			entry.Shell = slices.Append(p.Alloc, entry.Shell, cloneText(p.Alloc, "-c"))
		} else if shell.Kind == core.List && len(shell.List) != 0 {
			for i := range shell.List {
				v := shell.List[i]
				if v.Kind != core.String || strings.IndexByte(v.Text, 0) >= 0 || (i == 0 && v.Text == "") {
					return failureAt(p.Alloc, "EXPR_INVALID", diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}, "shell requires kash, an executable string, or a nonempty string argv list")
				}
				entry.Shell = slices.Append(p.Alloc, entry.Shell, cloneText(p.Alloc, v.Text))
			}
		} else {
			return failureAt(p.Alloc, "EXPR_INVALID", diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}, "shell requires kash, an executable string, or a nonempty string argv list")
		}
	} else {
		entry.Shell = cloneStrings(p.Alloc, p.Options.Shell)
	}
	freeStrings(p.Alloc, entry.MetadataEnvironment)
	entry.MetadataEnvironment = nil
	for i := range metadata.Record {
		field := metadata.Record[i]
		if field.Key != "env" {
			continue
		}
		if field.Value.Kind != core.Record {
			return failureAt(p.Alloc, "EXPR_INVALID", diagnostic.Span{Start: entry.Rule.Metadata.Span.Start, End: entry.Rule.Metadata.Span.End}, "env metadata requires a record of strings")
		}
		for j := range field.Value.Record {
			assignment := field.Value.Record[j]
			if !validEnvironmentName(assignment.Key) || assignment.Value.Kind != core.String || strings.IndexByte(assignment.Value.Text, 0) >= 0 {
				return failureAt(p.Alloc, "EXPR_INVALID", diagnostic.Span{Start: entry.Rule.Metadata.Span.Start, End: entry.Rule.Metadata.Span.End}, "env metadata requires valid names and string values without NUL")
			}
			entry.MetadataEnvironment = slices.Append(p.Alloc, entry.MetadataEnvironment, cloneText(p.Alloc, assignment.Key+"="+assignment.Value.Text))
		}
	}
	if c.PhaseInvalid() || len(c.Effects) != 0 {
		return failureAt(p.Alloc, "PHASE_INVALID", diagnostic.Span{Start: entry.Rule.Header.Start, End: entry.Rule.Header.End}, "recipe settings cannot perform effects")
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) freeSettingsContext(state *recipeSettingsResolver, c *eval.Context) {
	slices.Free(p.Alloc, state.Resolving)
	eval.FreeEffects(p.Alloc, c.Effects)
	freeStrings(p.Alloc, c.WritePaths)
}

func (p *Program) saveSettingsDependencies(index int, state *recipeSettingsResolver) {
	p.Instances[index].SettingsDependencies = state.Dependencies
}

func (p *Program) settingsFailure(r *eval.Result, c *eval.Context) diagnostic.Diagnostic {
	d := r.Diagnostic
	r.Diagnostic = diagnostic.Diagnostic{}
	r.Free(p.Alloc)
	if d.Code != "" {
		return d
	}
	if c.PhaseInvalid() {
		return failure(p.Alloc, "PHASE_INVALID", "recipe settings cannot perform effects")
	}
	return failure(p.Alloc, "EXPR_INVALID", "recipe settings could not be resolved without effects")
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i := range name {
		b := name[i]
		if b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (i > 0 && b >= '0' && b <= '9') {
			continue
		}
		return false
	}
	return true
}

func (p *Program) environmentFailure(index int, message string) diagnostic.Diagnostic {
	d := p.Instances[index].SettingsDiagnostic
	if d.Code != "" {
		return d.Clone(p.Alloc)
	}
	return failure(p.Alloc, "ENV_CONFLICT", message)
}

// Context construction is shared with recipe execution so rule frames survive
// template expansion into a structured script.
func (p *Program) kashContext(c *core.EngineContext, index int) *eval.Context {
	entry := &p.Instances[index]
	names, resources := entry.Plan.Inputs, entry.Plan.ResourceInputs
	if entry.Plan.Resolved {
		names, resources = entry.Plan.ResolvedInputs, entry.Plan.ResolvedResourceInputs
	}
	inputs := makeRuleInputValues(p.Alloc, names, resources)
	outputs := makeValues(p.Alloc, entry.Plan.Outputs)
	state := mem.Alloc[renderDependencyState](p.Alloc)
	*state = renderDependencyState{Program: p, Index: index}
	frames := slices.Make[eval.RuleFrame](p.Alloc, 1)
	frames[0] = eval.RuleFrame{Inputs: inputs, Outputs: outputs, FileRule: entry.Rule.Kind == rule.FileRule}
	context := mem.Alloc[eval.Context](p.Alloc)
	*context = eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Environment: entry.Environment, HasEnvironment: true, TimeoutMS: p.Options.TimeoutMS, Args: p.Eval.DefinitionArgs, HasArgs: p.Eval.DefinitionArgsSet, Phase: eval.EvaluatePhase, ResolverState: state, DependencyObserver: observeRenderDependency, OperationObserver: observeRenderOperation, ToolResolver: resolveRenderTool, RuleFrames: frames}
	p.bindDefinitionEnvironment(context)
	context.Scope = p.ruleScope(context, entry.Captures, entry.Plan.Arguments)
	context.RecipeNode = entry.Node.ID
	return context
}

func (p *Program) freeKashContext(context *eval.Context) {
	context.Scope.Free()
	for i := range context.RuleFrames {
		freeValues(p.Alloc, context.RuleFrames[i].Inputs)
		freeValues(p.Alloc, context.RuleFrames[i].Outputs)
	}
	slices.Free(p.Alloc, context.RuleFrames)
	eval.FreeEffects(p.Alloc, context.Effects)
	freeStrings(p.Alloc, context.WritePaths)
	mem.Free(p.Alloc, context.ResolverState.(*renderDependencyState))
	mem.Free(p.Alloc, context)
}

// Translate rendered Kash offsets to the closest authored recipe line.
func (p *Program) kashDiagnostic(index int, d diagnostic.Diagnostic) diagnostic.Diagnostic {
	return p.kashDiagnosticText(index, p.Instances[index].Script, d)
}

func (p *Program) kashDiagnosticText(index int, text string, d diagnostic.Diagnostic) diagnostic.Diagnostic {
	entry := &p.Instances[index]
	line, offset := 0, 0
	for offset < len(text) && offset < d.Span.Start {
		if text[offset] == '\n' {
			line++
		}
		offset++
	}
	span := entry.Rule.Header
	if line < len(entry.LineSpans) {
		authored := entry.LineSpans[line]
		span = source.Span{Start: authored.Start, End: authored.End}
	}
	d.Span = diagnostic.Span{Start: span.Start, End: span.End}
	if d.Owned {
		mem.FreeString(p.Alloc, d.Source)
	}
	d.Source = ""
	p.locateDiagnostic(&d, d.Span)
	return d
}

func (p *Program) validateRenderedKash(index int, text string) diagnostic.Diagnostic {
	parsed := script.ParseKash(p.Alloc, "<recipe>", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) == 0 {
		return diagnostic.Diagnostic{}
	}
	sourceDiagnostic := parsed.Diagnostics[0]
	d := failureAt(p.Alloc, sourceDiagnostic.Code, diagnostic.Span{Start: sourceDiagnostic.Span.Start, End: sourceDiagnostic.Span.End}, sourceDiagnostic.Message)
	return p.kashDiagnosticText(index, text, d)
}

// Resume structured Kash work after each process or dependency completion.
// Freshness and cache checks have already selected this recipe for execution.
func (p *Program) continueKashRecipe(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	if p.Forwarding && entry.Rule.Kind == rule.FileRule && !entry.KashPrepared {
		if entry.KashPreparing {
			completion := c.Completion()
			if completion.RequestID == 0 {
				return core.ProducerWaiting
			}
			if completion.Diagnostic.Code != "" {
				p.failRule(c, index, completion.Diagnostic)
				return core.ProducerFailed
			}
			completion.Value.Free(p.Alloc)
			entry.KashPrepared, entry.KashPreparing = true, false
		} else {
			payload := host.RecipeExecutionPayload(p.Alloc, "", entry.Plan.Outputs, nil, nil)
			p.nextRequest++
			p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestPrepareOutputs, Payload: payload})
			c.Submit(p.nextRequest)
			entry.KashPreparing = true
			return core.ProducerSubmitted
		}
	}
	if entry.KashContext == nil {
		entry.KashContext = p.kashContext(c, index)
	}
	context := entry.KashContext
	context.Resume(c)
	eval.FreeEffects(p.Alloc, context.Effects)
	freeStrings(p.Alloc, context.WritePaths)
	context.Effects, context.WritePaths = nil, nil
	callPath := invocationRecipePath(p.Alloc, entry.retryCount)
	context.CallPath = callPath
	defer mem.FreeString(p.Alloc, callPath)
	result := p.Eval.RunKash(entry.Script, context, entry.Rule.Header)
	defer result.Free(p.Alloc)
	if result.Waiting {
		return core.ProducerWaiting
	}
	entry = &p.Instances[index]
	entry.KashRunning = false
	if result.Diagnostic.Code != "" {
		if entry.retryCount < p.Options.RetryCount && result.Diagnostic.Code != "EXEC_CANCELLED" && result.Diagnostic.Code != "PARSE_ERR" {
			entry.retryCount++
			slices.Free(p.Alloc, entry.CacheStdout)
			slices.Free(p.Alloc, entry.CacheStderr)
			entry.CacheStdout, entry.CacheStderr = nil, nil
			entry.CacheStdoutTruncated, entry.CacheStderrTruncated = false, false
			entry.KashRunning = true
			result.Free(p.Alloc)
			return p.continueKashRecipe(c, index)
		}
		d := p.kashDiagnostic(index, result.Diagnostic)
		result.Diagnostic = diagnostic.Diagnostic{}
		p.failRule(c, index, d)
		return core.ProducerFailed
	}
	mem.FreeString(p.Alloc, entry.Script)
	entry.Script = ""
	if d := validateEffects(p.Alloc, entry, context.Effects); d.Code != "" {
		p.failRule(c, index, d)
		return core.ProducerFailed
	}
	if p.Forwarding {
		pending := mem.Alloc[forwardEffectsState](p.Alloc)
		pending.WritePaths = cloneStrings(p.Alloc, context.WritePaths)
		pending.HasYield = hasYield(context.Effects)
		for i := range context.Effects {
			effect := context.Effects[i]
			pending.Effects = slices.Append(p.Alloc, pending.Effects, eval.Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(p.Alloc, effect.Data)})
		}
		entry.ForwardEffects = pending
		return p.continueForwardEffects(c, index)
	}
	if d := p.commitEffects(entry, context.Effects, context.WritePaths, false); d.Code != "" {
		p.failRule(c, index, d)
		return core.ProducerFailed
	}
	// An executed file recipe publishes its artifact path after verifying outputs.
	// finishRecipe also commits the existing cached-task key and context stamp.
	return p.finishRecipe(c, index, "", hasYield(context.Effects))
}
