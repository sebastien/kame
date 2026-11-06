package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Compile validates a parsed script and registers definitions and rule declarations.
// It deliberately performs no host or filesystem work.
func Compile(a mem.Allocator, parsed *script.Script, registry *eval.Registry, options Options) CompileResult {
	return compileSources(a, parsed, registry, options, nil, nil)
}

func compileSources(a mem.Allocator, parsed *script.Script, registry *eval.Registry, options Options, sources []CompileSource, offsets []int) CompileResult {
	// Attach lexical origins before registration can clone contribution ASTs or
	// evaluate computed declarations. Display names alone never establish a base.
	for i := range sources {
		if sources[i].SourceDirectory != "" {
			eval.SetScriptSourceOrigin(parsed, offsets[i], offsets[i]+len(sources[i].Text), i+1)
		}
	}
	engine := core.NewEngine(a)
	compiled := eval.CompileChecked(a, engine, parsed, registry)
	result := CompileResult{Diagnostics: slices.Clone(a, compiled.Diagnostics)}
	slices.Free(a, compiled.Diagnostics)
	// Parser and evaluator diagnostics are tied to this parsed source. Keep the
	// borrowed source identity with them; the parsed script outlives CompileResult.
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Source == "" && parsed != nil && parsed.Source != nil {
			if result.Diagnostics[i].Owned {
				result.Diagnostics[i].Source = cloneText(a, parsed.Source.Name)
			} else {
				result.Diagnostics[i].Source = parsed.Source.Name
			}
		}
	}
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Severity == 0 {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Source: parsed.Source.Name, Code: parsed.Diagnostics[i].Code, Severity: diagnostic.Warning, Message: parsed.Diagnostics[i].Message, Span: diagnostic.Span{Start: parsed.Diagnostics[i].Span.Start, End: parsed.Diagnostics[i].Span.End}})
		}
	}
	if compiled.Program == nil {
		engine.Free()
		if options.Host != nil {
			options.Host.Free()
		}
		return result
	}
	p := mem.Alloc[Program](a)
	p.Alloc, p.Engine, p.Eval, p.Parsed, p.Host = a, engine, compiled.Program, parsed, options.Host
	p.Eval.ExitBoundary = true
	for i := range sources {
		p.Eval.AddSourcePart(sources[i].Name, offsets[i], offsets[i]+len(sources[i].Text), sources[i].Offset)
		p.Eval.SetSourcePartDirectory(sources[i].SourceDirectory)
		p.Eval.SetSourcePartIncludes(sources[i].Includes)
	}
	p.Eval.ProcessesQuiet, p.Eval.ProcessesQuietState = deferredProcessesQuiet, p
	p.Eval.RecordReaderCloser = p
	// Build invocations have an argument frame even when no operands were given.
	p.Eval.SetDefinitionArgs(nil)
	configurationError := p.configureParameters(options.Defines, options.Parameters, options.Environment)
	if configurationError.Code != "" {
		result.Diagnostics = slices.Append(a, result.Diagnostics, configurationError)
		p.Free()
		return result
	}
	p.nextRequest = 1 << 60 // Evaluator queue request IDs start at one.
	p.Forwarding = options.ForwardRequests
	p.Options.Watch = options.Watch
	if p.Host != nil && !p.Forwarding {
		p.Eval.FinalizationNow, p.Eval.FinalizationQuiet, p.Eval.FinalizationHost = finalizationNow, finalizationQuiet, p
	}
	p.Options.Directory, p.Options.DryRun, p.Options.Force, p.Options.RetainBytes, p.Options.Jobs = cloneText(a, options.Directory), options.DryRun, options.Force, options.RetainBytes, options.Jobs
	p.Options.CacheRetainBytes, p.Options.CacheDisabled, p.Options.CacheManifestMax = options.CacheRetainBytes, options.CacheDisabled, options.CacheManifestMax
	p.Options.HasInput, p.Options.InputFD = options.HasInput, options.InputFD
	p.Eval.InputSupplied = options.HasInput
	if options.HasInput {
		p.Options.CacheDisabled = true
		p.Options.Force = true
	}
	p.Options.TimeoutMS, p.Options.RetryCount, p.Options.Verbose = options.TimeoutMS, options.RetryCount, options.Verbose
	p.Options.ToolOverrides = cloneStrings(a, options.ToolOverrides)
	for i := range options.RemoteExecutors {
		p.Options.RemoteExecutors = slices.Append(a, p.Options.RemoteExecutors, host.ExecutorDescriptor{Name: cloneText(a, options.RemoteExecutors[i].Name), Version: cloneText(a, options.RemoteExecutors[i].Version)})
	}
	p.Options.ResolveTool = options.ResolveTool
	p.Options.TrackTimings, p.Options.TimingNow, p.Options.TimingClock = options.TrackTimings, options.TimingNow, options.TimingClock
	p.Options.Ownership, p.Options.OwnershipState = options.Ownership, options.OwnershipState
	p.Options.LockResources, p.Options.UnlockResources, p.Options.ResourcesState = options.LockResources, options.UnlockResources, options.ResourcesState
	p.Options.CaptureLimit = options.CaptureLimit
	if p.Options.CaptureLimit <= 0 {
		p.Options.CaptureLimit = eval.DefaultCaptureLimit
	}
	if p.Options.RetryCount < 0 {
		p.Options.RetryCount = 0
	}
	if p.Options.CacheRetainBytes <= 0 {
		p.Options.CacheRetainBytes = cacheLogDefault
	}
	for i := range options.Shell {
		p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, options.Shell[i]))
	}
	for i := range options.Environment {
		p.Options.Environment = slices.Append(a, p.Options.Environment, cloneText(a, options.Environment[i]))
	}
	for i := range options.Grants {
		grant := eval.Grant{Capability: options.Grants[i].Capability}
		for j := range options.Grants[i].Names {
			grant.Names = slices.Append(a, grant.Names, cloneText(a, options.Grants[i].Names[j]))
		}
		p.Options.Grants = slices.Append(a, p.Options.Grants, grant)
	}
	p.Eval.SetGrants(p.Options.Grants)
	p.Eval.DryRun = options.DryRun
	p.Eval.CaptureLimit = p.Options.CaptureLimit
	p.Eval.GeneratedFile, p.Eval.GeneratedFileState = generatedFile, p
	p.Eval.SetDefinitionCwd(p.Options.Directory)
	p.Eval.SetDefinitionDependencyObserver(observeDefinitionDependency, p)
	p.Eval.DefinitionDependencyContextObserver = observeScopedDefinitionDependency
	if len(p.Options.Shell) == 0 {
		p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "/bin/sh"))
		p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "-c"))
	}
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Rule || item.Rule == nil {
			continue
		}
		resolved := p.resolveOutputs(item.Rule)
		if resolved.Diagnostic.Code != "" {
			result.Diagnostics = slices.Append(a, result.Diagnostics, resolved.Diagnostic)
			continue
		}
		candidate := resolved.Rule
		if collision := duplicateLiteral(p, candidate); collision.Code != "" {
			result.Diagnostics = slices.Append(a, result.Diagnostics, collision)
			continue
		}
		p.Rules = slices.Append(a, p.Rules, registeredRule{Rule: candidate})
		for j := range item.Rule.Arguments {
			argument := item.Rule.Arguments[j]
			if p.Eval.Definition(argument.Name) != nil {
				d := failureAt(a, "TGT_ARGUMENT", diagnostic.Span{Start: argument.Span.Start, End: argument.Span.End}, "target argument shadows definition: "+argument.Name)
				d.Source = cloneText(a, parsed.Source.Name)
				result.Diagnostics = slices.Append(a, result.Diagnostics, d)
			}
		}
		for j := range item.Rule.Body {
			if item.Rule.Body[j].Template == nil {
				continue
			}
			for k := range item.Rule.Body[j].Template.Parts {
				part := item.Rule.Body[j].Template.Parts[k]
				if part.Kind != template.Tool || p.toolIndex(part.Text) >= 0 {
					continue
				}
				p.Tools = slices.Append(a, p.Tools, Tool{Name: cloneText(a, part.Text)})
			}
		}
	}
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity >= diagnostic.Error {
			p.Free()
			return result
		}
	}
	p.declareBuildTools(parsed)
	if generated := p.compileGeneratedDeclarations(); generated.Code != "" {
		result.Diagnostics = slices.Append(a, result.Diagnostics, generated)
		p.Free()
		return result
	}
	if invalid := p.validateParameters(options.Parameters); invalid.Code != "" {
		result.Diagnostics = slices.Append(a, result.Diagnostics, invalid)
		p.Free()
		return result
	}
	if options.AutomaticClean != nil {
		p.EnableAutomaticClean(options.AutomaticClean, options.AutomaticCleanState)
	}
	p.nextRequest = 1 << 32
	result.Program = p
	return result
}

func (p *Program) toolIndex(name string) int {
	for i := range p.Tools {
		if p.Tools[i].Name == name {
			return i
		}
	}
	return -1
}

// SetToolPath records a preflight-resolved executable for a declared tool.
func (p *Program) SetToolPath(name, executable string) bool {
	i := p.toolIndex(name)
	if i < 0 {
		return false
	}
	mem.FreeString(p.Alloc, p.Tools[i].Path)
	p.Tools[i].Path = cloneText(p.Alloc, executable)
	p.Tools[i].Resolved = true
	return true
}

func (p *Program) toolPath(name string) (string, bool) {
	i := p.toolIndex(name)
	if i < 0 {
		return "", false
	}
	if !p.Tools[i].Resolved && p.Options.ResolveTool != nil {
		p.Tools[i].Resolved = true
		p.Tools[i].Path = p.Options.ResolveTool(p.Alloc, p.toolRequestName(name), p.Options.Directory, p.Options.Environment)
	}
	if p.Tools[i].Path == "" {
		return "", false
	}
	return p.Tools[i].Path, true
}

// ResolveTool resolves one declared tool on demand and returns a borrowed path.
func (p *Program) ResolveTool(name string) string {
	path, _ := p.toolPath(name)
	return path
}

// HasTarget reports whether a literal rule or definition can be selected.
// It performs no materialization and creates no rule instance.
func (p *Program) HasTarget(target string) bool {
	if p == nil {
		return false
	}
	selected := p.selectRule(target)
	found := selected.Rule != nil || p.Eval.Definition(target) != nil
	freeCaptures(p.Alloc, selected.Captures)
	freeArguments(p.Alloc, selected.Arguments)
	mem.FreeString(p.Alloc, selected.Target)
	selected.Diagnostic.Free(p.Alloc)
	return found
}

// NamedTargets returns literal non-file rule outputs in declaration order.
// Template and path targets require an explicit request and are omitted.
func (p *Program) NamedTargets() []string {
	if p == nil {
		return nil
	}
	var targets []string
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if r == nil || r.Kind == rule.FileRule || r == p.automaticCleanRule {
			continue
		}
		for j := range r.Outputs {
			output := r.Outputs[j]
			if output.Template || isFileName(output.Text) {
				continue
			}
			duplicate := false
			for k := range targets {
				if targets[k] == output.Text {
					duplicate = true
					break
				}
			}
			if !duplicate {
				targets = slices.Append(p.Alloc, targets, cloneText(p.Alloc, output.Text))
			}
		}
	}
	return targets
}

// FreeStrings releases a string list returned by a Program query.
func FreeStrings(a mem.Allocator, values []string) { freeStrings(a, values) }

// CompileMany combines source texts for parsing while qualifying diagnostics
// with their originating source. Compile remains the zero-overhead one-source API.
func CompileMany(a mem.Allocator, sources []CompileSource, registry *eval.Registry, options Options) CompileResult {
	if len(sources) == 0 {
		return CompileResult{}
	}
	var builder strings.Builder = strings.NewBuilder(a)
	offsets := make([]int, len(sources))
	for i := range sources {
		offsets[i] = builder.Len()
		builder.WriteString(sources[i].Text)
		if i+1 < len(sources) {
			builder.WriteByte('\n')
		}
	}
	text := cloneText(a, builder.String())
	builder.Free()
	parsed := script.Parse(a, "<sources>", text)
	parsed.Source.Expanded = len(sources) > 1 || sources[0].Offset != 0
	mem.FreeString(a, text)
	result := compileSources(a, parsed, registry, options, sources, offsets)
	if result.Program != nil {
		result.Program.ParsedOwned = true
	}
	for i := range result.Diagnostics {
		// Diagnostics must outlive the temporary combined parser and the caller's
		// source descriptors, including on failed compilation.
		owned := result.Diagnostics[i].Clone(a)
		result.Diagnostics[i].Free(a)
		result.Diagnostics[i] = owned
		qualifyCompileDiagnostic(a, &result.Diagnostics[i], parsed.Source.Name, sources, offsets)
	}
	if result.Program == nil {
		parsed.Free()
	}
	return result
}

// Remap every located fact before appending already-authored include frames.
// Primary and related locations can belong to different include occurrences.
func qualifyCompileDiagnostic(a mem.Allocator, d *diagnostic.Diagnostic, combined string, sources []CompileSource, offsets []int) {
	if len(sources) == 0 {
		return
	}
	for i := range d.Frames {
		if d.Frames[i].Source == combined {
			qualifyCompileLocation(a, &d.Frames[i].Source, &d.Frames[i].Span, sources, offsets)
		}
	}
	if d.Source == combined {
		owner := qualifyCompileLocation(a, &d.Source, &d.Span, sources, offsets)
		appendIncludeFrames(a, d, sources[owner].Includes)
	}
	for i := range d.Related {
		if d.Related[i].Source != combined {
			continue
		}
		owner := qualifyCompileLocation(a, &d.Related[i].Source, &d.Related[i].Span, sources, offsets)
		appendIncludeFrames(a, d, sources[owner].Includes)
	}
}

func qualifyCompileLocation(a mem.Allocator, name *string, span *diagnostic.Span, sources []CompileSource, offsets []int) int {
	owner := 0
	for i := 1; i < len(sources); i++ {
		if offsets[i] <= span.Start {
			owner = i
		}
	}
	mem.FreeString(a, *name)
	*name = cloneText(a, sources[owner].Name)
	span.Start += sources[owner].Offset - offsets[owner]
	span.End += sources[owner].Offset - offsets[owner]
	return owner
}

func appendIncludeFrames(a mem.Allocator, d *diagnostic.Diagnostic, frames []diagnostic.Frame) {
	for i := len(frames) - 1; i >= 0; i-- {
		frame := frames[i]
		found := false
		for j := range d.Frames {
			other := d.Frames[j]
			if other.Kind == frame.Kind && other.Label == frame.Label && other.Source == frame.Source && other.Span.Start == frame.Span.Start && other.Span.End == frame.Span.End {
				found = true
				break
			}
		}
		if !found {
			d.Frames = slices.Append(a, d.Frames, diagnostic.Frame{Kind: cloneText(a, frame.Kind), Label: cloneText(a, frame.Label), Source: cloneText(a, frame.Source), Span: frame.Span})
		}
	}
}

func duplicateLiteral(p *Program, candidate *rule.Rule) diagnostic.Diagnostic {
	for i := range candidate.Outputs {
		if candidate.Outputs[i].Template {
			continue
		}
		for j := 0; j < i; j++ {
			if candidate.Outputs[j].Template {
				continue
			}
			first := p.canonicalTarget(candidate.Outputs[j].Text, candidate.Kind == rule.FileRule)
			second := p.canonicalTarget(candidate.Outputs[i].Text, candidate.Kind == rule.FileRule)
			equal := first == second
			mem.FreeString(p.Alloc, first)
			mem.FreeString(p.Alloc, second)
			if equal {
				return p.outputDiagnostic(candidate.Outputs[i].Span, "PARSE_ERR", "duplicate rule output")
			}
		}
		for j := range p.Rules {
			other := p.Rules[j].Rule
			if (other.Kind == rule.FileRule) != (candidate.Kind == rule.FileRule) {
				continue
			}
			for k := range other.Outputs {
				if !other.Outputs[k].Template {
					first := p.canonicalTarget(other.Outputs[k].Text, candidate.Kind == rule.FileRule)
					second := p.canonicalTarget(candidate.Outputs[i].Text, candidate.Kind == rule.FileRule)
					equal := first == second
					mem.FreeString(p.Alloc, first)
					mem.FreeString(p.Alloc, second)
					if equal {
						return p.producerAmbiguity(candidate.Outputs[i].Text, other.Outputs[k], candidate.Outputs[i])
					}
				}
			}
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) Free() {
	if p == nil {
		return
	}
	if !p.Forwarding {
		p.Eval.BeginFinalization()
		for p.Eval.Finalizing() {
			p.tick(10)
		}
	}
	p.Eval.CancelProcesses()
	p.Eval.CancelDefers()
	slices.Free(p.Alloc, p.RecordReaders)
	p.clearProductionChanges()
	p.RecordReaders = nil
	for i := range p.RecordRequests {
		p.RecordRequests[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, p.RecordRequests)
	p.RecordRequests = nil
	slices.Free(p.Alloc, p.workReady)
	p.workReady = nil
	rule.FreeRule(p.Alloc, p.automaticCleanRule)
	p.clearInspection()
	freeStrings(p.Alloc, p.Configuration)
	freeStrings(p.Alloc, p.ParameterDefinitions)
	freeStrings(p.Alloc, p.Options.ToolOverrides)
	for i := range p.Options.RemoteExecutors {
		mem.FreeString(p.Alloc, p.Options.RemoteExecutors[i].Name)
		mem.FreeString(p.Alloc, p.Options.RemoteExecutors[i].Version)
	}
	slices.Free(p.Alloc, p.Options.RemoteExecutors)
	for i := range p.Instances {
		if p.Instances[i].InspectionStatus != nil {
			p.Instances[i].InspectionStatus.free(p.Alloc)
		}
		slices.Free(p.Alloc, p.Instances[i].Artifacts)
		p.Instances[i].Plan.Free(p.Alloc)
		p.Instances[i].Service.Free(p.Alloc)
		freeStrings(p.Alloc, p.Instances[i].Environment)
		freeStrings(p.Alloc, p.Instances[i].Shell)
		expr.Free(p.Alloc, p.Instances[i].KameExpression)
		mem.FreeString(p.Alloc, p.Instances[i].Executor)
		mem.FreeString(p.Alloc, p.Instances[i].ExecutorVersion)
		for j := range p.Instances[i].ExecutionInputs {
			p.Instances[i].ExecutionInputs[j].Free(p.Alloc)
		}
		slices.Free(p.Alloc, p.Instances[i].ExecutionInputs)
		freeStrings(p.Alloc, p.Instances[i].ExecutionOutputs)
		freeStrings(p.Alloc, p.Instances[i].MetadataEnvironment)
		freeStrings(p.Alloc, p.Instances[i].SettingsDependencies)
		p.Instances[i].SettingsDiagnostic.Free(p.Alloc)
		p.clearReasonIdentities(&p.Instances[i])
		if p.Instances[i].KashContext != nil {
			p.freeKashContext(p.Instances[i].KashContext)
		}
		p.freeFileContext(p.Instances[i].FileContext)
		p.releaseResources(&p.Instances[i])
		freeStrings(p.Alloc, p.Instances[i].OwnershipPaths)
		p.Instances[i].AcceptedRecord.Free(p.Alloc)
		p.freeNewerInputs(p.Instances[i].NewerInputs)
		freeCaptures(p.Alloc, p.Instances[i].Captures)
		mem.FreeString(p.Alloc, p.Instances[i].Script)
		p.freeForwardEffects(p.Instances[i].ForwardEffects)
		slices.Free(p.Alloc, p.Instances[i].LineSpans)
		freeStrings(p.Alloc, p.Instances[i].Operations)
		slices.Free(p.Alloc, p.Instances[i].CacheManifest)
		slices.Free(p.Alloc, p.Instances[i].CacheStdout)
		slices.Free(p.Alloc, p.Instances[i].CacheStderr)
	}
	for i := range p.Tools {
		mem.FreeString(p.Alloc, p.Tools[i].Name)
		mem.FreeString(p.Alloc, p.Tools[i].Path)
	}
	for i := range p.Events {
		p.Events[i].Free(p.Alloc)
	}
	mem.FreeString(p.Alloc, p.Options.Directory)
	for i := range p.Options.Shell {
		mem.FreeString(p.Alloc, p.Options.Shell[i])
	}
	for i := range p.Options.Environment {
		mem.FreeString(p.Alloc, p.Options.Environment[i])
	}
	for i := range p.Options.Grants {
		for j := range p.Options.Grants[i].Names {
			mem.FreeString(p.Alloc, p.Options.Grants[i].Names[j])
		}
		slices.Free(p.Alloc, p.Options.Grants[i].Names)
	}
	slices.Free(p.Alloc, p.Options.Shell)
	slices.Free(p.Alloc, p.Options.Environment)
	slices.Free(p.Alloc, p.Options.Grants)
	slices.Free(p.Alloc, p.Instances)
	slices.Free(p.Alloc, p.Events)
	slices.Free(p.Alloc, p.Rules)
	slices.Free(p.Alloc, p.Tools)
	for i := range p.Pending {
		freePendingRequest(p.Alloc, &p.Pending[i])
	}
	slices.Free(p.Alloc, p.Pending)
	for i := range p.Outbound {
		p.Outbound[i].Free(p.Alloc)
	}
	// An empty Outbound can still own a backing array after requests are popped.
	slices.Free(p.Alloc, p.Outbound)
	if p.Host != nil {
		p.Eval.RecordReaderCloser = nil
		p.Host.Free()
	}
	p.Engine.Free()
	for i := range p.GeneratedRules {
		freeGeneratedRule(p.Alloc, p.GeneratedRules[i])
	}
	for i := range p.ResolvedRules {
		freeResolvedRule(p.Alloc, p.ResolvedRules[i])
	}
	slices.Free(p.Alloc, p.ResolvedRules)
	for i := range p.GeneratedRuleMeta {
		mem.FreeString(p.Alloc, p.GeneratedRuleMeta[i].Name)
		freeStrings(p.Alloc, p.GeneratedRuleMeta[i].Dependencies)
	}
	slices.Free(p.Alloc, p.GeneratedRules)
	slices.Free(p.Alloc, p.GeneratedRuleMeta)
	p.Eval.RecordReaderCloser = nil
	p.Eval.Free()
	if p.ParsedOwned && p.Parsed != nil {
		p.Parsed.Free()
	}
	mem.Free(p.Alloc, p)
}
