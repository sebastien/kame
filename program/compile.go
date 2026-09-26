package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/script"
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
	p.nextRequest = 1 << 60 // Evaluator queue request IDs start at one.
	p.Options.Directory, p.Options.DryRun, p.Options.Force, p.Options.RetainBytes, p.Options.Jobs = cloneText(a, options.Directory), options.DryRun, options.Force, options.RetainBytes, options.Jobs
	p.Options.CacheRetainBytes, p.Options.CacheDisabled, p.Options.CacheManifestMax = options.CacheRetainBytes, options.CacheDisabled, options.CacheManifestMax
	p.Options.TimeoutMS, p.Options.RetryCount, p.Options.Verbose = options.TimeoutMS, options.RetryCount, options.Verbose
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
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: "TGT_AMBIG", Severity: diagnostic.Error, Message: "duplicate literal rule target", Span: diagnostic.Span{Start: item.Rule.Header.Start, End: item.Rule.Header.End}})
			continue
		}
		p.Rules = slices.Append(a, p.Rules, registeredRule{Rule: item.Rule})
	}
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity >= diagnostic.Error {
			p.Free()
			return result
		}
	}
	p.nextRequest = 1 << 32
	result.Program = p
	return result
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
	mem.FreeString(a, text)
	result := Compile(a, parsed, registry, options)
	if result.Program == nil {
		parsed.Free()
	} else {
		result.Program.ParsedOwned = true
	}
	for i := range result.Diagnostics {
		position := result.Diagnostics[i].Span.Start
		owner := 0
		for j := 1; j < len(sources); j++ {
			if offsets[j] <= position {
				owner = j
			}
		}
		result.Diagnostics[i].Source = sources[owner].Name
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
	for i := range p.Instances {
		p.Instances[i].Plan.Free(p.Alloc)
		freeCaptures(p.Alloc, p.Instances[i].Captures)
		if p.Instances[i].Script != "" {
			mem.FreeString(p.Alloc, p.Instances[i].Script)
		}
		if len(p.Instances[i].LineSpans) != 0 {
			slices.Free(p.Alloc, p.Instances[i].LineSpans)
		}
		freeStrings(p.Alloc, p.Instances[i].Operations)
		if len(p.Instances[i].CacheManifest) != 0 {
			slices.Free(p.Alloc, p.Instances[i].CacheManifest)
		}
		if len(p.Instances[i].CacheStdout) != 0 {
			slices.Free(p.Alloc, p.Instances[i].CacheStdout)
		}
		if len(p.Instances[i].CacheStderr) != 0 {
			slices.Free(p.Alloc, p.Instances[i].CacheStderr)
		}
	}
	for i := range p.Events {
		p.Events[i].Free(p.Alloc)
	}
	if p.Options.Directory != "" {
		mem.FreeString(p.Alloc, p.Options.Directory)
	}
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
	slices.Free(p.Alloc, p.Pending)
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
