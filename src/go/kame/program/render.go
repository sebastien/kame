package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) render(c *core.EngineContext, entry *instance, names []string) renderResult {
	inputs := makeValues(p.Alloc, names)
	outputs := makeValues(p.Alloc, entry.Plan.Outputs)
	defer freeValues(p.Alloc, inputs)
	defer freeValues(p.Alloc, outputs)
	dependencyState := renderDependencyState{Program: p, Index: p.instanceIndex(entry.Node)}
	context := mem.Alloc[eval.Context](p.Alloc)
	*context = eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.RenderingPhase, ResolverState: &dependencyState, DependencyObserver: observeRenderDependency, OperationObserver: observeRenderOperation, ToolResolver: resolveRenderTool, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	if entry.Rule.BodyDoc != nil {
		return p.renderDocument(entry, context)
	}
	b := strings.NewBuilder(p.Alloc)
	defer b.Free()
	var spans []diagnostic.Span
	for i := range entry.Rule.Body {
		if entry.Rule.Body[i].Template == nil {
			continue
		}
		result := p.Eval.Render(p.Alloc, entry.Rule.Body[i].Template, p.Eval.Scope, context)
		if result.Waiting {
			slices.Free(p.Alloc, spans)
			eval.FreeEffects(p.Alloc, context.Effects)
			freeStrings(p.Alloc, context.WritePaths)
			mem.Free(p.Alloc, context)
			return renderResult{Waiting: true}
		}
		if result.Diagnostic.Code != "" {
			slices.Free(p.Alloc, spans)
			eval.FreeEffects(p.Alloc, context.Effects)
			freeStrings(p.Alloc, context.WritePaths)
			mem.Free(p.Alloc, context)
			return renderResult{Diagnostic: result.Diagnostic}
		}
		if result.Value.Kind != core.String && result.Value.Kind != core.Pattern {
			result.Value.Free(p.Alloc)
			slices.Free(p.Alloc, spans)
			eval.FreeEffects(p.Alloc, context.Effects)
			freeStrings(p.Alloc, context.WritePaths)
			mem.Free(p.Alloc, context)
			return renderResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "recipe line is not text")}
		}
		if result.Value.Text != "" {
			if b.Len() != 0 {
				b.WriteByte('\n')
			}
			b.WriteString(result.Value.Text)
			spans = slices.Append(p.Alloc, spans, diagnostic.Span{Start: entry.Rule.Body[i].Span.Start, End: entry.Rule.Body[i].Span.End})
		}
		result.Value.Free(p.Alloc)
	}
	var effects []eval.Effect
	for i := range context.Effects {
		effects = slices.Append(p.Alloc, effects, eval.Effect{Kind: context.Effects[i].Kind, Data: slices.Clone(p.Alloc, context.Effects[i].Data), Span: context.Effects[i].Span})
	}
	eval.FreeEffects(p.Alloc, context.Effects)
	var writePaths []string
	for i := range context.WritePaths {
		writePaths = slices.Append(p.Alloc, writePaths, cloneText(p.Alloc, context.WritePaths[i]))
		mem.FreeString(p.Alloc, context.WritePaths[i])
	}
	slices.Free(p.Alloc, context.WritePaths)
	result := renderResult{Commands: cloneText(p.Alloc, b.String()), Effects: effects, WritePaths: writePaths, LineSpans: spans}
	mem.Free(p.Alloc, context)
	return result
}

func diagnosticFromSource(a mem.Allocator, code string, message string, start int, end int, src string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{Code: cloneText(a, code), Severity: diagnostic.Error, Message: cloneText(a, message), Source: cloneText(a, src), Span: diagnostic.Span{Start: start, End: end}, Owned: true}
}

func (p *Program) renderDocument(entry *instance, context *eval.Context) renderResult {
	doc := entry.Rule.BodyDoc
	if len(doc.Diagnostics) != 0 {
		d := doc.Diagnostics[0]
		eval.FreeEffects(p.Alloc, context.Effects)
		freeStrings(p.Alloc, context.WritePaths)
		mem.Free(p.Alloc, context)
		return renderResult{Diagnostic: diagnosticFromSource(p.Alloc, d.Code, d.Message, d.Span.Start, d.Span.End, doc.Source.Name)}
	}
	result := p.Eval.EvaluateWith(doc.Root, context)
	if result.Waiting {
		eval.FreeEffects(p.Alloc, context.Effects)
		freeStrings(p.Alloc, context.WritePaths)
		mem.Free(p.Alloc, context)
		return renderResult{Waiting: true}
	}
	if result.Diagnostic.Code != "" {
		eval.FreeEffects(p.Alloc, context.Effects)
		freeStrings(p.Alloc, context.WritePaths)
		mem.Free(p.Alloc, context)
		return renderResult{Diagnostic: result.Diagnostic}
	}
	if result.Value.Kind != core.String {
		result.Value.Free(p.Alloc)
		eval.FreeEffects(p.Alloc, context.Effects)
		freeStrings(p.Alloc, context.WritePaths)
		mem.Free(p.Alloc, context)
		return renderResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "recipe document is not text")}
	}
	script := result.Value.Text
	// Trim a single trailing newline for shell parity with per-line rendering.
	// Document bodies preserve endings; per-line joins without trailing NL.
	if len(script) != 0 && script[len(script)-1] == '\n' {
		trimmed := ""
		if len(script)-1 != 0 {
			trimmed = cloneText(p.Alloc, script[:len(script)-1])
		}
		result.Value.Free(p.Alloc)
		// Strip a trailing \r for CRLF bodies.
		if len(trimmed) != 0 && trimmed[len(trimmed)-1] == '\r' {
			rerim := ""
			if len(trimmed)-1 != 0 {
				rerim = cloneText(p.Alloc, trimmed[:len(trimmed)-1])
			}
			mem.FreeString(p.Alloc, trimmed)
			trimmed = rerim
		}
		script = trimmed
		// Transfer: script now owns trimmed storage; prevent double free below.
		// result.Value already freed; set empty to keep Free safe.
		result.Value = core.Value{}
		// Re-wrap trimmed as owned value for uniform handling below.
		if script != "" {
			// script already owned; create value referencing it without copy?
			// Use NewString which copies; free the intermediate.
			owned := core.NewString(p.Alloc, script)
			mem.FreeString(p.Alloc, script)
			result.Value = owned
			script = result.Value.Text
		}
	}
	var spans []diagnostic.Span
	if result.Value.Text != "" && len(entry.Rule.Body) != 0 {
		// Single authored span covering the recipe body.
		first := entry.Rule.Body[0].Span
		last := entry.Rule.Body[len(entry.Rule.Body)-1].Span
		spans = slices.Append(p.Alloc, spans, diagnostic.Span{Start: first.Start, End: last.End})
	}
	var effects []eval.Effect
	for i := range context.Effects {
		effects = slices.Append(p.Alloc, effects, eval.Effect{Kind: context.Effects[i].Kind, Data: slices.Clone(p.Alloc, context.Effects[i].Data), Span: context.Effects[i].Span})
	}
	eval.FreeEffects(p.Alloc, context.Effects)
	var writePaths []string
	for i := range context.WritePaths {
		writePaths = slices.Append(p.Alloc, writePaths, cloneText(p.Alloc, context.WritePaths[i]))
		mem.FreeString(p.Alloc, context.WritePaths[i])
	}
	slices.Free(p.Alloc, context.WritePaths)
	out := renderResult{Commands: cloneText(p.Alloc, result.Value.Text), Effects: effects, WritePaths: writePaths, LineSpans: spans}
	result.Value.Free(p.Alloc)
	mem.Free(p.Alloc, context)
	return out
}

func observeRenderDependency(value any, key core.ResourceKey) {
	state := value.(*renderDependencyState)
	if key.Name == "" {
		return
	}
	p := state.Program
	if state.Inspection {
		observeInspectionDependency(p, key)
		return
	}
	if key.Kind == core.ResourceEnvironment || key.Kind == core.ResourceGlob {
		state := mem.Alloc[externalValueState](p.Alloc)
		state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
		if p.Engine.AddOwned(key, produceExternalValue, state, freeExternalValueState) == nil {
			freeExternalValueState(p.Alloc, state)
		}
		return
	}
	resolved := p.instanceFor(key.Name)
	if resolved.Diagnostic.Code == "" || (resolved.Diagnostic.Code == "TGT_NO_RULE" && key.Kind == core.ResourceFile) {
		if resolved.Node == nil && key.Kind == core.ResourceFile {
			external := mem.Alloc[externalFileState](p.Alloc)
			external.Program, external.Name = p, cloneText(p.Alloc, key.Name)
			if p.Engine.AddOwned(key, produceExternalFile, external, freeExternalFileState) == nil {
				freeExternalFileState(p.Alloc, external)
			}
		}
	}
	resolved.Plan.Free(p.Alloc)
	resolved.Diagnostic.Free(p.Alloc)
	if state.Index < 0 || state.Index >= len(p.Instances) {
		return
	}
	if resolved.Node != nil {
		p.adoptTask(resolved.Node, p.Instances[state.Index].runEpoch)
	}
	entry := &p.Instances[state.Index]
	p.emit(Event{Kind: DependencyDiscovered, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, DependencyKey: key})
}

func resolveRenderTool(value any, name string) (string, bool) {
	state := value.(*renderDependencyState)
	return state.Program.toolPath(name)
}

// observeInspectionDependency provides only external resources to span
// expansion. In particular it never calls instanceFor: discovering a file
// rule here would turn a read-only inspection into recipe execution.
func observeInspectionDependency(value any, key core.ResourceKey) {
	p := value.(*Program)
	if key.Name == "" {
		return
	}
	if key.Kind == core.ResourceEnvironment || key.Kind == core.ResourceGlob {
		state := mem.Alloc[externalValueState](p.Alloc)
		state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
		if p.Engine.AddOwned(key, produceExternalValue, state, freeExternalValueState) == nil {
			freeExternalValueState(p.Alloc, state)
		}
		return
	}
	if key.Kind == core.ResourceFile {
		state := mem.Alloc[externalFileState](p.Alloc)
		state.Program, state.Name = p, cloneText(p.Alloc, key.Name)
		if p.Engine.AddOwned(key, produceExternalFile, state, freeExternalFileState) == nil {
			freeExternalFileState(p.Alloc, state)
		}
	}
}

// observeDefinitionDependency supplies external resources to lazy definitions.
// Definitions have no rule-instance event identity, so this intentionally only
// registers resources and never emits a target dependency event.
func observeDefinitionDependency(value any, key core.ResourceKey) {
	p := value.(*Program)
	if key.Name == "" {
		return
	}
	if key.Kind == core.ResourceEnvironment || key.Kind == core.ResourceGlob {
		state := mem.Alloc[externalValueState](p.Alloc)
		state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
		if p.Engine.AddOwned(key, produceExternalValue, state, freeExternalValueState) == nil {
			freeExternalValueState(p.Alloc, state)
		}
		return
	}
	resolved := p.instanceFor(key.Name)
	if resolved.Diagnostic.Code == "" || (resolved.Diagnostic.Code == "TGT_NO_RULE" && key.Kind == core.ResourceFile) {
		if resolved.Node == nil && key.Kind == core.ResourceFile {
			state := mem.Alloc[externalFileState](p.Alloc)
			state.Program, state.Name = p, cloneText(p.Alloc, key.Name)
			if p.Engine.AddOwned(key, produceExternalFile, state, freeExternalFileState) == nil {
				freeExternalFileState(p.Alloc, state)
			}
		}
	}
	resolved.Plan.Free(p.Alloc)
	resolved.Diagnostic.Free(p.Alloc)
}

func observeRenderOperation(value any, name string, version string) {
	state := value.(*renderDependencyState)
	if state.Index < 0 || state.Index >= len(state.Program.Instances) {
		return
	}
	entry := &state.Program.Instances[state.Index]
	identity := name + "\x00" + version
	for i := range entry.Operations {
		if entry.Operations[i] == identity {
			return
		}
	}
	entry.Operations = slices.Append(state.Program.Alloc, entry.Operations, cloneText(state.Program.Alloc, identity))
}
