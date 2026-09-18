// Package program compiles LittleMake scripts and materializes their targets.
package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type Freshness int

const (
	Fresh Freshness = iota
	Stale
	Unknown
)

type Plan struct {
	Target    string
	Key       core.ResourceKey
	Rule      *rule.Rule
	RuleSpan  diagnostic.Span
	Body      []rule.RecipeLine
	Captures  []template.CaptureValue
	Inputs    []string
	ResourceInputs []PlanInput
	ResolvedInputs []string
	ResolvedResourceInputs []PlanInput
	Resolved bool
	Outputs   []string
	Freshness Freshness
}

type PlanInput struct { Display string; Key core.ResourceKey }

type PlanResult struct { Plan Plan; Diagnostic diagnostic.Diagnostic }

func (p *Plan) Free(a mem.Allocator) {
	if p == nil { return }
	if p.Key.Name != "" { p.Key.Free(a) }
	for i := range p.Captures { mem.FreeString(a, p.Captures[i].Name); mem.FreeString(a, p.Captures[i].Text) }
	for i := range p.Inputs { mem.FreeString(a, p.Inputs[i]) }
	for i := range p.ResourceInputs { if p.ResourceInputs[i].Display != "" { mem.FreeString(a, p.ResourceInputs[i].Display) }; p.ResourceInputs[i].Key.Free(a) }
	for i := range p.ResolvedInputs { mem.FreeString(a, p.ResolvedInputs[i]) }
	for i := range p.ResolvedResourceInputs { if p.ResolvedResourceInputs[i].Display != "" { mem.FreeString(a, p.ResolvedResourceInputs[i].Display) }; p.ResolvedResourceInputs[i].Key.Free(a) }
	for i := range p.Outputs { mem.FreeString(a, p.Outputs[i]) }
	slices.Free(a, p.Captures); slices.Free(a, p.Inputs); slices.Free(a, p.ResourceInputs); slices.Free(a, p.ResolvedInputs); slices.Free(a, p.ResolvedResourceInputs); slices.Free(a, p.Outputs)
	*p = Plan{}
}

type EventKind int

const (
	TargetStarted EventKind = iota
	ProcessStarted
	ProcessExited
	Stdout
	Stderr
	DependencyDiscovered
	Effect
	TargetValue
	TargetCompleted
	TargetFailed
	TargetCancelled
)

type Event struct {
	Kind       EventKind
	Target     string
	Key        core.ResourceKey
	NodeID     int64
	Generation int64
	Attempt    int64
	RequestID  int64
	DependencyID int64
	DependencyKey core.ResourceKey
	Span       diagnostic.Span
	Data       []byte
	Value      core.Value
	Diagnostic diagnostic.Diagnostic
}

type EventResult struct { Event Event; OK bool }

type Handle struct {
	Program *Program
	Root    *core.Root
	Node    *core.Node
	Target  string
	Definition bool
}

type HandleResult struct { Result Result; Done bool }
type HandleStart struct { Handle *Handle; Diagnostic diagnostic.Diagnostic }

func (e *Event) Free(a mem.Allocator) { if e.Target != "" { mem.FreeString(a, e.Target) }; if e.Key.Name != "" { e.Key.Free(a) }; if e.DependencyKey.Name != "" { e.DependencyKey.Free(a) }; if len(e.Data) != 0 { slices.Free(a, e.Data) }; e.Value.Free(a); e.Diagnostic.Free(a); *e = Event{} }

func (h *Handle) Free() { if h == nil || h.Program == nil { return }; p := h.Program; if h.Root != nil { p.Engine.Release(h.Root) }; if h.Target != "" { mem.FreeString(p.Alloc, h.Target) }; *h = Handle{}; mem.Free(p.Alloc, h) }

type Options struct {
	Directory string
	Shell     []string
	Environment []string
	DryRun    bool
	RetainBytes int
	Jobs int
}

type Program struct {
	Alloc    mem.Allocator
	Engine   *core.Engine
	Eval     *eval.Program
	Parsed   *script.Script
	ParsedOwned bool
	Host     *posix.Host
	Options  Options
	Rules    []registeredRule
	Instances []instance
	Events   []Event
	nextRequest int64
}

type registeredRule struct { Rule *rule.Rule }
type instance struct { Rule *rule.Rule; Captures []template.CaptureValue; Node *core.Node; Plan Plan; Script string; LineSpans []diagnostic.Span; started bool; startedGeneration int64; terminalEmitted bool; terminalGeneration int64; valueRevision int64 }
type selection struct { Rule *rule.Rule; Captures []template.CaptureValue; Ambiguous bool }
type planResolverState struct { Program *Program; Resolving []string }

type CompileResult struct { Program *Program; Diagnostics []diagnostic.Diagnostic }
type CompileSource struct { Name string; Text string }

func (r *CompileResult) Free(a mem.Allocator) { for i := range r.Diagnostics { r.Diagnostics[i].Free(a) }; slices.Free(a, r.Diagnostics); *r = CompileResult{} }

// Compile validates a parsed script and registers definitions and rule declarations.
// It deliberately performs no host or filesystem work.
func Compile(a mem.Allocator, parsed *script.Script, registry *eval.Registry, options Options) CompileResult {
	engine := core.NewEngine(a)
	compiled := eval.CompileChecked(a, engine, parsed, registry)
	result := CompileResult{Diagnostics: slices.Clone(a, compiled.Diagnostics)}
	slices.Free(a, compiled.Diagnostics)
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Severity == 0 {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Source: parsed.Source.Name, Code: parsed.Diagnostics[i].Code, Severity: diagnostic.Warning, Message: parsed.Diagnostics[i].Message, Span: diagnostic.Span{Start: parsed.Diagnostics[i].Span.Start, End: parsed.Diagnostics[i].Span.End}})
		}
	}
	if compiled.Program == nil { engine.Free(); return result }
	p := mem.Alloc[Program](a)
	p.Alloc, p.Engine, p.Eval, p.Parsed = a, engine, compiled.Program, parsed
	p.Options.Directory, p.Options.DryRun, p.Options.RetainBytes, p.Options.Jobs = cloneText(a, options.Directory), options.DryRun, options.RetainBytes, options.Jobs
	for i := range options.Shell { p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, options.Shell[i])) }
	for i := range options.Environment { p.Options.Environment = slices.Append(a, p.Options.Environment, cloneText(a, options.Environment[i])) }
	if len(p.Options.Shell) == 0 { p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "/bin/sh")); p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "-c")) }
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Rule || item.Rule == nil { continue }
		if duplicateLiteral(p, item.Rule) {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: "TGT_AMBIG", Severity: diagnostic.Error, Message: "duplicate literal rule target", Span: diagnostic.Span{Start: item.Rule.Header.Start, End: item.Rule.Header.End}})
			continue
		}
		p.Rules = slices.Append(a, p.Rules, registeredRule{Rule: item.Rule})
	}
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity >= diagnostic.Error { p.Free(); return result }
	}
	p.Host = posix.New(a)
	result.Program = p
	return result
}

// CompileMany combines source texts for parsing while qualifying diagnostics
// with their originating source. Compile remains the zero-overhead one-source API.
func CompileMany(a mem.Allocator, sources []CompileSource, registry *eval.Registry, options Options) CompileResult {
	if len(sources) == 0 { return CompileResult{} }
	var builder strings.Builder = strings.NewBuilder(a)
	offsets := make([]int, len(sources))
	for i := range sources { offsets[i] = builder.Len(); builder.WriteString(sources[i].Text); if i+1 < len(sources) { builder.WriteByte('\n') } }
	text := cloneText(a, builder.String())
	builder.Free()
	parsed := script.Parse(a, sources[0].Name, text)
	mem.FreeString(a, text)
	result := Compile(a, parsed, registry, options)
	if result.Program == nil { parsed.Free() } else { result.Program.ParsedOwned = true }
	for i := range result.Diagnostics {
		position := result.Diagnostics[i].Span.Start
		owner := 0
		for j := 1; j < len(sources); j++ { if offsets[j] <= position { owner = j } }
		result.Diagnostics[i].Source = sources[owner].Name
	}
	return result
}

func duplicateLiteral(p *Program, candidate *rule.Rule) bool {
	for i := range candidate.Outputs {
		if candidate.Outputs[i].Template { continue }
		for j := range p.Rules {
			other := p.Rules[j].Rule
			if (other.Kind == rule.FileRule) != (candidate.Kind == rule.FileRule) { continue }
			for k := range other.Outputs { if !other.Outputs[k].Template && other.Outputs[k].Text == candidate.Outputs[i].Text { return true } }
		}
	}
	return false
}

func (p *Program) Free() {
	if p == nil { return }
	for i := range p.Instances { p.Instances[i].Plan.Free(p.Alloc); freeCaptures(p.Alloc, p.Instances[i].Captures); if p.Instances[i].Script != "" { mem.FreeString(p.Alloc, p.Instances[i].Script) }; if len(p.Instances[i].LineSpans) != 0 { slices.Free(p.Alloc, p.Instances[i].LineSpans) } }
	for i := range p.Events { p.Events[i].Free(p.Alloc) }
	if p.Options.Directory != "" { mem.FreeString(p.Alloc, p.Options.Directory) }
	for i := range p.Options.Shell { mem.FreeString(p.Alloc, p.Options.Shell[i]) }
	for i := range p.Options.Environment { mem.FreeString(p.Alloc, p.Options.Environment[i]) }
	slices.Free(p.Alloc, p.Options.Shell); slices.Free(p.Alloc, p.Options.Environment)
	slices.Free(p.Alloc, p.Instances); slices.Free(p.Alloc, p.Events); slices.Free(p.Alloc, p.Rules)
	if p.Host != nil { p.Host.Free() }
	p.Engine.Free(); p.Eval.Free(); if p.ParsedOwned && p.Parsed != nil { p.Parsed.Free() }; mem.Free(p.Alloc, p)
}

// Cancel releases the runtime's request interest for a target and forwards any
// resulting process cancellation to the host.
func (p *Program) Cancel(target string) diagnostic.Diagnostic {
	resolved := p.instanceFor(target)
	if resolved.Diagnostic.Code != "" { return resolved.Diagnostic }
	if resolved.Node == nil { resolved.Plan.Free(p.Alloc); return failure("TGT_NO_RULE", "no rule for target: "+target) }
	p.Engine.Cancel(resolved.Node)
	resolved.Plan.Free(p.Alloc)
	p.drainCancellations()
	return diagnostic.Diagnostic{}
}

func (p *Program) Plan(target string) PlanResult {
	selected := p.selectRule(target)
	if selected.Ambiguous { return PlanResult{Diagnostic: failure("TGT_AMBIG", "multiple rules match target")} }
	if selected.Rule == nil {
		if p.Eval.Definition(target) != nil { return PlanResult{Plan: Plan{Target: target, Key: core.NewResourceKey(p.Alloc, core.ResourceDefinition, target), Freshness: Unknown}} }
		return PlanResult{Diagnostic: failure("TGT_NO_RULE", "no rule for target: "+target)}
	}
	plan := Plan{Target: target, Rule: selected.Rule, RuleSpan: diagnostic.Span{Start: selected.Rule.Span.Start, End: selected.Rule.Span.End}, Body: selected.Rule.Body, Captures: cloneCaptures(p.Alloc, selected.Captures), Freshness: Unknown}
	defer freeCaptures(p.Alloc, selected.Captures)
	if selected.Rule.Kind == rule.FileRule {
		canonical := p.canonicalTarget(target, true)
		plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), canonical)
		mem.FreeString(p.Alloc, canonical)
	} else { plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), target) }
	for i := range selected.Rule.Outputs {
		output := renderTarget(p.Alloc, selected.Rule.Outputs[i], selected.Captures)
		plan.Outputs = slices.Append(p.Alloc, plan.Outputs, output)
	}
	for i := range plan.Outputs {
		for j := 0; j < i; j++ {
			if plan.Outputs[i] == plan.Outputs[j] {
				plan.Free(p.Alloc)
				return PlanResult{Diagnostic: failureAt("PARSE_ERR", diagnostic.Span{Start: selected.Rule.Span.Start, End: selected.Rule.Span.End}, "rule outputs resolve to the same path")}
			}
		}
	}
	for i := range selected.Rule.Inputs {
		input := selected.Rule.Inputs[i]
		if input.Kind == rule.InputExpression {
			plan.Freshness = Unknown
			if d := p.planInputExpression(input, &plan); d.Code != "" { plan.Free(p.Alloc); return PlanResult{Diagnostic: d} }
			continue
		}
	if input.Kind == rule.InputTemplate {
		text := renderInput(p.Alloc, input, selected.Captures)
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
		plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, core.ResourceTarget, text)})
		continue
	}
		text := cloneText(p.Alloc, input.Text)
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
		kind := core.ResourceTarget
		if input.Kind == rule.InputPath { kind = core.ResourceFile }
		plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text)})
	}
	if selected.Rule.Kind == rule.FileRule && len(selected.Rule.Body) == 0 { plan.Freshness = p.freshness(&plan, nil) } else { plan.Freshness = Unknown }
	return PlanResult{Plan: plan}
}

func (p *Program) planInputExpression(input rule.Input, plan *Plan) diagnostic.Diagnostic {
	if input.Template == nil || len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil {
		return failure("EXPR_INVALID", "invalid rule input expression")
	}
	inputs, outputs := makeValues(p.Alloc, plan.Inputs), makeValues(p.Alloc, plan.Outputs)
	defer freeValues(p.Alloc, inputs); defer freeValues(p.Alloc, outputs)
	state := planResolverState{Program: p}
	context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	result := p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
	if state.Resolving != nil { slices.Free(p.Alloc, state.Resolving) }
	if context.PhaseInvalid() || len(context.Effects) != 0 { eval.FreeEffects(p.Alloc, context.Effects); result.Free(p.Alloc); return failure("PHASE_INVALID", "build effects are invalid while planning") }
	if result.Waiting { result.Free(p.Alloc); return failure("PHASE_INVALID", "operation is invalid while planning") }
	if result.Diagnostic.Code != "" { return result.Diagnostic }
	if !appendPlanInputValue(p.Alloc, plan, result.Value) { result.Value.Free(p.Alloc); return failure("INPUT_INVALID", "rule input expression must produce strings, resources, lists, or nil") }
	result.Value.Free(p.Alloc)
	return diagnostic.Diagnostic{}
}

func resolvePlanDefinition(value any, key core.ResourceKey, context *eval.Context) eval.Result {
	state := value.(*planResolverState)
	for i := range state.Resolving { if state.Resolving[i] == key.Name { return eval.Result{Diagnostic: failure("DEP_CYCLE", "definition cycle")} } }
	state.Resolving = slices.Append(state.Program.Alloc, state.Resolving, key.Name)
	result := state.Program.Eval.EvaluateDefinition(key, context)
	state.Resolving = state.Resolving[:len(state.Resolving)-1]
	return result
}

func appendPlanInputValue(a mem.Allocator, plan *Plan, value core.Value) bool {
	if value.Kind == core.Nil { return true }
	if value.Kind == core.String { text := cloneText(a, value.Text); plan.Inputs = slices.Append(a, plan.Inputs, text); plan.ResourceInputs = slices.Append(a, plan.ResourceInputs, PlanInput{Display: cloneText(a, text), Key: core.NewResourceKey(a, core.ResourceTarget, text)}); return true }
	if value.Kind == core.Resource { text := cloneText(a, value.Resource.Name); plan.Inputs = slices.Append(a, plan.Inputs, text); plan.ResourceInputs = slices.Append(a, plan.ResourceInputs, PlanInput{Display: cloneText(a, text), Key: value.Resource.Clone(a)}); return true }
	if value.Kind != core.List { return false }
	for i := range value.List { if !appendPlanInputValue(a, plan, value.List[i]) { return false } }
	return true
}

func resourceKind(r *rule.Rule) core.ResourceKind {
	if r.Kind == rule.FileRule { return core.ResourceFile }
	if r.Kind == rule.CachedTaskRule { return core.ResourceTask }
	if r.Kind == rule.ServiceRule { return core.ResourceService }
	return core.ResourceTarget
}

func (p *Program) canonicalTarget(target string, file bool) string {
	if !file { return cloneText(p.Alloc, target) }
	if path.IsAbs(target) { return path.Clean(p.Alloc, target) }
	return path.Join(p.Alloc, p.Options.Directory, target)
}

func (p *Program) selectRule(target string) selection {
	requested := target
	file := isPath(target) || hasSlash(target)
	if file { target = p.canonicalTarget(target, true) }
	var matched *rule.Rule
	var captures []template.CaptureValue
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if (r.Kind == rule.FileRule) != file { continue }
		for j := range r.Outputs {
			output := r.Outputs[j]
			if output.Template { continue }
			if !file && output.Text == target { return selection{Rule: r} }
			if file {
				literal := p.canonicalTarget(output.Text, true)
				matches := literal == target
				mem.FreeString(p.Alloc, literal)
				if matches { mem.FreeString(p.Alloc, target); return selection{Rule: r} }
			}
		}
	}
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if (r.Kind == rule.FileRule) != file { continue }
		for j := range r.Outputs {
			output := r.Outputs[j]
			if !output.Template { continue }
			matchTarget := requested
			if file && !isPath(matchTarget) { matchTarget = "./" + matchTarget }
			match := output.TargetForm.MatchTarget(p.Alloc, matchTarget)
			if match == nil { continue }
			if matched != nil {
				if matched != r || !sameCaptures(captures, match.Captures) { match.Free(p.Alloc); freeCaptures(p.Alloc, captures); if file { mem.FreeString(p.Alloc, target) }; return selection{Ambiguous: true} }
				match.Free(p.Alloc)
				continue
			}
			matched, captures = r, cloneCaptures(p.Alloc, match.Captures)
			match.Free(p.Alloc)
		}
	}
	if file { mem.FreeString(p.Alloc, target) }
	return selection{Rule: matched, Captures: captures}
}

func (p *Program) freshness(plan *Plan, node *core.Node) Freshness {
	if len(plan.Outputs) == 0 { return Stale }
	inputs := plan.Inputs
	if plan.Resolved { inputs = plan.ResolvedInputs }
	fileInputs := 0
	var oldest os.FileInfo
	for i := range plan.Outputs {
		name := p.canonicalTarget(plan.Outputs[i], true)
		info, err := os.Stat(name); mem.FreeString(p.Alloc, name); if err != nil { return Stale }
		if i == 0 || info.ModTime().Before(oldest.ModTime()) { oldest = info }
	}
	for i := range inputs {
		if !isFileName(inputs[i]) { continue }
		fileInputs++
		name := p.canonicalTarget(inputs[i], true)
		info, err := os.Stat(name); mem.FreeString(p.Alloc, name); if err != nil { return Stale }
		if oldest.ModTime().Before(info.ModTime()) { return Stale }
	}
	if node != nil {
		for i := range node.Dynamic {
			dependency := node.Dynamic[i]
			if dependency.Key.Kind != core.ResourceFile { continue }
			fileInputs++
			name := p.canonicalTarget(dependency.Key.Name, true)
			info, err := os.Stat(name); mem.FreeString(p.Alloc, name); if err != nil { return Stale }
			if oldest.ModTime().Before(info.ModTime()) { return Stale }
		}
	}
	if fileInputs == 0 { return Stale }
	return Fresh
}

func isPath(value string) bool { return len(value) != 0 && (value[0] == '/' || (len(value) > 1 && value[0] == '.' && value[1] == '/')) }
func isFileName(value string) bool { return isPath(value) || hasSlash(value) }
func hasSlash(value string) bool { for i := range value { if value[i] == '/' { return true } }; return false }
func failure(code string, message string) diagnostic.Diagnostic { return diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message} }
func failureAt(code string, span diagnostic.Span, message string) diagnostic.Diagnostic { return diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message, Span: span} }
func cloneText(a mem.Allocator, text string) string {
	if text == "" { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
func cloneCaptures(a mem.Allocator, in []template.CaptureValue) []template.CaptureValue { var out []template.CaptureValue; for i := range in { out = slices.Append(a, out, template.CaptureValue{Name: cloneText(a, in[i].Name), Text: cloneText(a, in[i].Text)}) }; return out }
func freeCaptures(a mem.Allocator, values []template.CaptureValue) { for i := range values { mem.FreeString(a, values[i].Name); mem.FreeString(a, values[i].Text) }; slices.Free(a, values) }

func renderTarget(a mem.Allocator, target rule.Target, captures []template.CaptureValue) string {
	if !target.Template { return cloneText(a, target.Text) }
	b := strings.NewBuilder(a); defer b.Free()
	for i := range target.TargetForm.Parts { part := target.TargetForm.Parts[i]; if part.Kind == template.TargetLiteral { b.WriteString(part.Text) } else { for j := range captures { if captures[j].Name == part.Name { b.WriteString(captures[j].Text); break } } } }
	return cloneText(a, b.String())
}

func renderInput(a mem.Allocator, input rule.Input, captures []template.CaptureValue) string {
	target := rule.Target{Text: input.Text, Template: true, TargetForm: input.TargetForm}
	return renderTarget(a, target, captures)
}
