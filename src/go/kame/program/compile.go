package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
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
	configurationError := p.configureDefinitions(options.Defines, options.Environment)
	if configurationError.Code != "" {
		result.Diagnostics = slices.Append(a, result.Diagnostics, configurationError)
		p.Free()
		return result
	}
	p.nextRequest = 1 << 60 // Evaluator queue request IDs start at one.
	p.Forwarding = options.ForwardRequests
	p.Options.Directory, p.Options.DryRun, p.Options.Force, p.Options.RetainBytes, p.Options.Jobs = cloneText(a, options.Directory), options.DryRun, options.Force, options.RetainBytes, options.Jobs
	p.Options.CacheRetainBytes, p.Options.CacheDisabled, p.Options.CacheManifestMax = options.CacheRetainBytes, options.CacheDisabled, options.CacheManifestMax
	p.Options.TimeoutMS, p.Options.RetryCount, p.Options.Verbose = options.TimeoutMS, options.RetryCount, options.Verbose
	p.Options.ToolOverrides = cloneStrings(a, options.ToolOverrides)
	p.Options.ResolveTool = options.ResolveTool
	p.Options.CaptureLimit = options.CaptureLimit
	if p.Options.CaptureLimit <= 0 { p.Options.CaptureLimit = 1024 * 1024 }
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
	p.Eval.SetDefinitionCwd(p.Options.Directory)
	p.Eval.SetDefinitionDependencyObserver(observeDefinitionDependency, p)
	if len(p.Options.Shell) == 0 {
		p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "/bin/sh"))
		p.Options.Shell = slices.Append(a, p.Options.Shell, cloneText(a, "-c"))
	}
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Rule || item.Rule == nil {
			continue
		}
		if duplicateLiteral(p, item.Rule) {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Source: parsed.Source.Name, Code: "TGT_AMBIG", Severity: diagnostic.Error, Message: "duplicate literal rule target", Span: diagnostic.Span{Start: item.Rule.Header.Start, End: item.Rule.Header.End}})
			continue
		}
		p.Rules = slices.Append(a, p.Rules, registeredRule{Rule: item.Rule})
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
		if r == nil || r.Kind == rule.FileRule {
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
	parsed := script.Parse(a, sources[0].Name, text)
	parsed.Source.Expanded = len(sources) > 1 || sources[0].Offset != 0
	mem.FreeString(a, text)
	result := Compile(a, parsed, registry, options)
	if result.Program != nil {
		result.Program.ParsedOwned = true
		for i := range sources {
			result.Program.Eval.AddSourcePart(sources[i].Name, offsets[i], offsets[i]+len(sources[i].Text), sources[i].Offset)
		}
	}
	for i := range result.Diagnostics {
		position := result.Diagnostics[i].Span.Start
		// Diagnostics must outlive the temporary combined parser and the caller's
		// source descriptors, including on failed compilation.
		owned := result.Diagnostics[i].Clone(a)
		result.Diagnostics[i].Free(a)
		result.Diagnostics[i] = owned
		owner := 0
		for j := 1; j < len(sources); j++ {
			if offsets[j] <= position {
				owner = j
			}
		}
		mem.FreeString(a, result.Diagnostics[i].Source)
		result.Diagnostics[i].Source = cloneText(a, sources[owner].Name)
		result.Diagnostics[i].Span.Start -= offsets[owner]
		result.Diagnostics[i].Span.End -= offsets[owner]
		result.Diagnostics[i].Span.Start += sources[owner].Offset
		result.Diagnostics[i].Span.End += sources[owner].Offset
	}
	if result.Program == nil {
		parsed.Free()
	}
	return result
}

func duplicateLiteral(p *Program, candidate *rule.Rule) bool {
	for i := range candidate.Outputs {
		if candidate.Outputs[i].Template {
			continue
		}
		for j := range p.Rules {
			other := p.Rules[j].Rule
			if (other.Kind == rule.FileRule) != (candidate.Kind == rule.FileRule) {
				continue
			}
			for k := range other.Outputs {
				if !other.Outputs[k].Template && other.Outputs[k].Text == candidate.Outputs[i].Text {
					return true
				}
			}
		}
	}
	return false
}

func (p *Program) Free() {
	if p == nil {
		return
	}
	p.Eval.CancelProcesses()
	freeStrings(p.Alloc, p.Configuration)
	freeStrings(p.Alloc, p.Options.ToolOverrides)
	for i := range p.Instances {
		p.Instances[i].Plan.Free(p.Alloc)
  slices.Free(p.Alloc, p.Instances[i].Environment)
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
	slices.Free(p.Alloc, p.Pending)
	for i := range p.Outbound {
		p.Outbound[i].Free(p.Alloc)
	}
	// An empty Outbound can still own a backing array after requests are popped.
	slices.Free(p.Alloc, p.Outbound)
	if p.Host != nil {
		p.Host.Free()
	}
	p.Engine.Free()
	p.Eval.Free()
	if p.ParsedOwned && p.Parsed != nil {
		p.Parsed.Free()
	}
	mem.Free(p.Alloc, p)
}
