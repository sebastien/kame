package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"slices"
	"strings"
)

// Compile validates a parsed script and registers definitions and rule declarations.
// Authored declaration discovery observes filesystem membership during registration.
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
	compiled := eval.CompileCheckedForProgram(a, engine, parsed, registry)
	result := CompileResult{Diagnostics: slices.Clone(compiled.Diagnostics)}
	mem.FreeSlice(nil, compiled.Diagnostics)
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
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{Source: parsed.Source.Name, Code: parsed.Diagnostics[i].Code, Severity: diagnostic.Warning, Message: parsed.Diagnostics[i].Message, Span: diagnostic.Span{Start: parsed.Diagnostics[i].Span.Start, End: parsed.Diagnostics[i].Span.End}})
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
	engine.CanRun = p.canRunJob
	p.Eval.ExitBoundary = true
	for i := range sources {
		p.Eval.AddSourcePart(sources[i].Name, offsets[i], offsets[i]+len(sources[i].Text), sources[i].Offset)
		p.Eval.SetSourcePartDirectory(sources[i].SourceDirectory)
		p.Eval.SetSourcePartIncludes(sources[i].Includes)
		p.Eval.SourceParts[len(p.Eval.SourceParts)-1].Signature = sources[i].Signature
	}
	p.Eval.ProcessesQuiet, p.Eval.ProcessesQuietState = deferredProcessesQuiet, p
	p.Eval.RecordReaderCloser = p
	// Build invocations have an argument frame even when no operands were given.
	p.Eval.SetDefinitionArgs(nil)
	configurationError := p.configureParameters(options.Defines, options.Parameters, options.Environment)
	if configurationError.Code != "" {
		result.Diagnostics = append(result.Diagnostics, configurationError)
		p.Free()
		return result
	}
	p.nextRequest = 1 << 60 // Evaluator queue request IDs start at one.
	p.Forwarding = options.ForwardRequests
	p.Options.DeclarationRead, p.Options.DeclarationHost = options.DeclarationRead, options.DeclarationHost
	p.Options.DeclarationSource = options.DeclarationSource
	p.Options.CompositionDiscoveries = options.CompositionDiscoveries
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
	p.Eval.ToolOverrides = p.Options.ToolOverrides
	for i := range options.RemoteExecutors {
		p.Options.RemoteExecutors = append(p.Options.RemoteExecutors, host.ExecutorDescriptor{Name: cloneText(a, options.RemoteExecutors[i].Name), Version: cloneText(a, options.RemoteExecutors[i].Version)})
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
		p.Options.Shell = append(p.Options.Shell, cloneText(a, options.Shell[i]))
	}
	for i := range options.Environment {
		p.Options.Environment = append(p.Options.Environment, cloneText(a, options.Environment[i]))
	}
	p.Eval.ModuleEnvironment = p.Options.Environment
	p.Eval.ModuleWatch = p.Options.Watch
	if invalid := p.Eval.RegisterImports(); invalid.Code != "" {
		result.Diagnostics = append(result.Diagnostics, invalid)
		p.Free()
		return result
	}
	for i := range options.Grants {
		grant := eval.Grant{Capability: options.Grants[i].Capability}
		for j := range options.Grants[i].Names {
			grant.Names = append(grant.Names, cloneText(a, options.Grants[i].Names[j]))
		}
		p.Options.Grants = append(p.Options.Grants, grant)
	}
	p.Eval.SetGrants(p.Options.Grants)
	p.Eval.DryRun = options.DryRun
	p.Eval.CaptureLimit = p.Options.CaptureLimit
	p.Eval.GeneratedFile, p.Eval.GeneratedFileState = generatedFile, p
	p.Eval.SetDefinitionCwd(p.Options.Directory)
	p.Eval.SetDefinitionDependencyObserver(observeDefinitionDependency, p)
	p.Eval.DefinitionDependencyContextObserver = observeScopedDefinitionDependency
	if len(p.Options.Shell) == 0 {
		p.Options.Shell = append(p.Options.Shell, cloneText(a, "/bin/sh"))
		p.Options.Shell = append(p.Options.Shell, cloneText(a, "-c"))
	}
	return p.registerProgram(result, options)
}

func (p *Program) registerProgram(result CompileResult, options Options) (finished CompileResult) {
	defer func() {
		finished.RegistrationPending = p.registrationPending
		if options.Watch || options.DeclarationRead != nil || options.DeclarationSource != nil {
			finished.Discoveries = p.declarationSnapshots()
		}
		p.Options.CompositionDiscoveries = nil
		for _, root := range p.declarationImportRoots {
			p.Engine.Release(root)
		}
		p.declarationImportRoots = nil
		for _, completion := range p.declarationSourceValues {
			completion.Value.Free(p.Alloc)
			completion.Diagnostic.Free(p.Alloc)
		}
		p.declarationSourceValues = nil
		if finished.Program == nil {
			p.Free()
		}
	}()
	parsed := p.Parsed
	for i := range parsed.Items {
		if d := p.registerDeclarationBlock(parsed.Items[i:i+1], declarationScope{Scope: p.Eval.Scope}); d.Code != "" {
			result.Diagnostics = append(result.Diagnostics, d)
		}
		if p.registrationPending {
			return result
		}
	}
	p.GeneratedBatchUsage = nil
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity >= diagnostic.Error {
			return result
		}
	}
	p.declareBuildTools(parsed)
	if overlap := p.validateDiscoveryOutputs(); overlap.Code != "" {
		result.Diagnostics = append(result.Diagnostics, overlap)
		return result
	}
	if invalid := p.validateDefinitionOverrides(options.Defines); invalid.Code != "" {
		result.Diagnostics = append(result.Diagnostics, invalid)
		return result
	}
	includedOverrides := p.Eval.IncludedConfiguration()
	for _, override := range includedOverrides {
		p.recordDefinitionOverride(override)
	}
	mem.FreeSlice(nil, includedOverrides)
	if invalid := p.validateParameters(options.Parameters); invalid.Code != "" {
		result.Diagnostics = append(result.Diagnostics, invalid)
		return result
	}
	if options.AutomaticClean != nil {
		p.EnableAutomaticClean(options.AutomaticClean, options.AutomaticCleanState)
	}
	p.nextRequest = 1 << 32
	p.Options.DeclarationRead, p.Options.DeclarationHost = nil, nil
	p.Options.DeclarationSource = nil
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
				targets = append(targets, cloneText(p.Alloc, output.Text))
			}
		}
	}
	return targets
}

// FreeStrings releases a string list returned by a Program query.
func FreeStrings(a mem.Allocator, values []string) { freeStrings(a, values) }

// CompileMany parses independently bounded fragments and qualifies authored
// diagnostics. Fragments without a language use Kame file grammar.
func CompileMany(a mem.Allocator, sources []CompileSource, registry *eval.Registry, options Options) CompileResult {
	if len(sources) == 0 {
		return CompileResult{}
	}
	var builder strings.Builder = strings.Builder{}
	offsets := make([]int, len(sources))
	for i := range sources {
		offsets[i] = builder.Len()
		builder.WriteString(sources[i].Text)
		if i+1 < len(sources) {
			builder.WriteByte('\n')
		}
	}
	text := cloneText(a, builder.String())
	builder.Reset()
	parsed := parseSourceFragments(a, "<sources>", text, sources, offsets)
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

func parseSourceFragments(a mem.Allocator, name string, text string, sources []CompileSource, offsets []int) *script.Script {
	parsed := mem.Alloc[script.Script](a)
	parsed.Alloc, parsed.Source = a, source.New(a, name, text)
	for i := range sources {
		input := sources[i]
		if input.Lang == "" || (input.Lang == "km" && input.SourceDirectory != "") {
			input.Lang = script.KameFileLanguage
		}
		part := parseSourceFragment(a, parsed.Source, input, offsets[i])
		appendSourceFragment(parsed, part)
	}
	return parsed
}

func parseSourceFragment(a mem.Allocator, original *source.Source, input CompileSource, start int) *script.Script {
	part := script.ParseFragment(a, original, input.Lang, start, start+len(input.Text))
	if input.Lang == "kash" && len(input.Includes) != 0 {
		script.ValidateKashModule(part)
	}
	return part
}

// Transfer child ownership while retaining the original source storage.
func appendSourceFragment(parsed *script.Script, part *script.Script) {
	_ = parsed.Alloc
	for i := range part.Diagnostics {
		parsed.Diagnostics = append(parsed.Diagnostics, part.Diagnostics[i])
	}
	for i := range part.Items {
		parsed.Items = append(parsed.Items, part.Items[i])
	}
	mem.FreeSlice(nil, part.Diagnostics)
	mem.FreeSlice(nil, part.Items)
	part.Diagnostics, part.Items = nil, nil
	part.Free()
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
		appendDiagnosticFrame(a, d, frames[i])
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
						return p.producerAmbiguity(candidate.Outputs[i].Text,
							producerCandidate{Rule: other, Output: other.Outputs[k]},
							producerCandidate{Rule: candidate, Output: candidate.Outputs[i]})
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
	mem.FreeString(p.Alloc, p.definitionEnvironment.cwd)
	p.definitionEnvironment = definitionEnvironment{}
	if !p.Forwarding {
		p.Eval.BeginFinalization()
		for p.Eval.Finalizing() {
			p.tick(10)
		}
	}
	p.Eval.CancelProcesses()
	p.Eval.CancelDefers()
	mem.FreeSlice(nil, p.activeJobs)
	mem.FreeSlice(nil, p.RecordReaders)
	p.clearProductionChanges()
	for i := range p.declarationDiscoveries {
		item := &p.declarationDiscoveries[i]
		mem.FreeString(p.Alloc, item.Name)
		mem.FreeString(p.Alloc, item.Pattern)
		item.Files.Free(p.Alloc)
		item.Failure.Free(p.Alloc)
	}
	mem.FreeSlice(nil, p.declarationDiscoveries)
	for i := range p.declarationSources {
		p.declarationSources[i].Free(p.Alloc)
	}
	mem.FreeSlice(nil, p.declarationSources)
	mem.FreeSlice(nil, p.deferredTerminal)
	p.RecordReaders = nil
	for i := range p.RecordRequests {
		p.RecordRequests[i].Free(p.Alloc)
	}
	mem.FreeSlice(nil, p.RecordRequests)
	p.RecordRequests = nil
	mem.FreeSlice(nil, p.workReady)
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
	mem.FreeSlice(nil, p.Options.RemoteExecutors)
	for i := range p.Instances {
		if p.Instances[i].InspectionStatus != nil {
			p.Instances[i].InspectionStatus.free(p.Alloc)
		}
		mem.FreeSlice(nil, p.Instances[i].Artifacts)
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
		mem.FreeSlice(nil, p.Instances[i].ExecutionInputs)
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
		mem.FreeSlice(nil, p.Instances[i].LineSpans)
		freeStrings(p.Alloc, p.Instances[i].Operations)
		mem.FreeSlice(nil, p.Instances[i].CacheManifest)
		mem.FreeSlice(nil, p.Instances[i].CacheStdout)
		mem.FreeSlice(nil, p.Instances[i].CacheStderr)
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
		mem.FreeSlice(nil, p.Options.Grants[i].Names)
	}
	mem.FreeSlice(nil, p.Options.Shell)
	mem.FreeSlice(nil, p.Options.Environment)
	mem.FreeSlice(nil, p.Options.Grants)
	mem.FreeSlice(nil, p.Instances)
	mem.FreeSlice(nil, p.Events)
	mem.FreeSlice(nil, p.Rules)
	mem.FreeSlice(nil, p.Tools)
	for i := range p.Pending {
		freePendingRequest(p.Alloc, &p.Pending[i])
	}
	mem.FreeSlice(nil, p.Pending)
	for i := range p.Outbound {
		p.Outbound[i].Free(p.Alloc)
	}
	mem.
		// An empty Outbound can still own a backing array after requests are popped.
		FreeSlice(nil, p.Outbound)
	if p.Host != nil {
		p.Eval.RecordReaderCloser = nil
		p.Host.Free()
	}
	p.Engine.Free()
	for _, scoped := range p.RuleDeclarations {
		scoped.Scope.Free()
	}
	for _, r := range p.ScopedRules {
		mem.Free(p.Alloc, r)
	}
	for i := range p.ResolvedRules {
		freeResolvedRule(p.Alloc, p.ResolvedRules[i])
	}
	mem.FreeSlice(nil, p.ResolvedRules)
	for i := range p.GeneratedRuleMeta {
		freeStrings(p.Alloc, p.GeneratedRuleMeta[i].Dependencies)
	}
	mem.FreeSlice(nil, p.GeneratedRuleMeta)
	p.Eval.RecordReaderCloser = nil
	p.Eval.Free()
	for _, included := range p.declarationIncludes {
		included.Free()
	}
	if p.ParsedOwned && p.Parsed != nil {
		p.Parsed.Free()
	}
	mem.Free(p.Alloc, p)
}
