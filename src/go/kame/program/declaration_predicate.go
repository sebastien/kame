package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/operations"

	"path"
	"strings"
)

type PredicateResult struct {
	Selected    bool
	Pending     bool
	Diagnostic  diagnostic.Diagnostic
	Discoveries []DeclarationSnapshot
	Paths       []string
}

type configurationConditionRequest struct {
	Name            string
	Prefix          string
	Predicate       string
	Defines         []string
	Environment     []string
	Directory       string
	RequireBoolean  bool
	Origins         []CompileSource
	PredicateOffset int
	Located         bool
	Discovery       *Options
	Include         *CompileSource
}

func configurationDiagnostic(a mem.Allocator, value diagnostic.Diagnostic, located bool) diagnostic.Diagnostic {
	view := value
	// So lowers slice literals to C compound literals; keep their backing
	// storage alive until Clone, outside the conditional blocks below.
	tips := []string{""}
	if value.Code == "CAP_DENIED" {
		view.Message = "configuration conditions cannot use host capabilities or execute processes"
		tips[0] = "select source files using literal configuration and pure expressions; runtime grants do not apply to conditions"
		view.Tips = tips
	} else if value.Code == "PHASE_INVALID" {
		view.Message = "configuration conditions must be pure; runtime effects are not allowed"
		tips[0] = "move effects into runtime statements or rule recipes"
		view.Tips = tips
	}
	copy := view.Clone(a)
	if !located {
		// The legacy query has two independent synthetic texts, not an authored
		// source map. Neither its primary nor its frames can claim file offsets.
		mem.FreeString(a, copy.Source)
		copy.Source, copy.Span = "", diagnostic.Span{}
	}
	for i := range copy.Frames {
		if located {
			continue
		}
		mem.FreeString(a, copy.Frames[i].Source)
		copy.Frames[i].Source = ""
		copy.Frames[i].Span = diagnostic.Span{}
	}
	return copy
}

// DeclarationPredicate evaluates a pure configuration predicate against already
// selected declarations. Includes are supplied by the source-composition host;
// this function never reads files, submits requests or launches processes.
// Directory supplies the evaluation cwd without consulting the host.
// Future explicit overrides are validated by final registration, rather than
// rejected merely because their declaration has not been loaded yet.
func DeclarationPredicate(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string, directory string) PredicateResult {
	return configurationCondition(a, configurationConditionRequest{Name: name, Prefix: prefix, Predicate: predicate, Defines: defines, Environment: environment, Directory: directory, RequireBoolean: true})
}

// CompositionCondition uses ordinary expression truth without granting host
// capabilities or loading sources: only nil and false are false. Diagnostics
// retain labels but no synthetic locations; use CompositionConditionSources for
// authored provenance.
func CompositionCondition(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string, directory string) PredicateResult {
	return configurationCondition(a, configurationConditionRequest{Name: name, Prefix: prefix, Predicate: predicate, Defines: defines, Environment: environment, Directory: directory})
}

// CompositionConditionSources accepts declaration-only fragments and an authored
// predicate. Text and inclusion paths stay borrowed until the query returns.
// Directory is the caller's evaluation cwd, not a source-relative include base.
func CompositionConditionSources(a mem.Allocator, sources []CompileSource, condition CompileSource, defines []string, environment []string, directory string) PredicateResult {
	return TrackedCompositionCondition(a, sources, condition, defines, environment, directory, nil)
}

// TrackedCompositionCondition evaluates a file selection using the declaration
// discovery host and grants. Its snapshots also belong to the final program.
func TrackedCompositionCondition(a mem.Allocator, sources []CompileSource, condition CompileSource, defines []string, environment []string, directory string, discovery *Options) PredicateResult {
	return trackedComposition(a, sources, condition, defines, environment, directory, discovery, false)
}

// CompositionIncludePaths selects source paths before any of their rules register.
func CompositionIncludePaths(a mem.Allocator, sources []CompileSource, operand CompileSource, defines []string, environment []string, directory string, discovery *Options) PredicateResult {
	return trackedComposition(a, sources, operand, defines, environment, directory, discovery, true)
}

func trackedComposition(a mem.Allocator, sources []CompileSource, condition CompileSource, defines []string, environment []string, directory string, discovery *Options, include bool) PredicateResult {
	b := strings.Builder{}
	defer b.Reset()
	for i := range sources {
		b.WriteString(sources[i].Text)
		b.WriteByte('\n')
	}
	request := configurationConditionRequest{Name: condition.Name, Prefix: b.String(), Predicate: condition.Text, Defines: defines, Environment: environment, Directory: directory, Origins: sources, PredicateOffset: condition.Offset, Located: true, Discovery: discovery}
	if include {
		request.Include = &condition
	}
	result := configurationCondition(a, request)
	if result.Diagnostic.Code == "" {
		return result
	}
	d := &result.Diagnostic
	frame := diagnostic.Frame{Kind: "condition", Label: "source selection", Source: condition.Name, Span: diagnostic.Span{Start: condition.Offset, End: condition.Offset + len(condition.Text)}}
	appendIncludeFrames(a, d, []diagnostic.Frame{frame})
	// Locations are authored now; include paths must not be remapped again.
	for i := range sources {
		used := d.Source == sources[i].Name && d.Span.Start >= sources[i].Offset && d.Span.Start < sources[i].Offset+len(sources[i].Text)
		for j := range d.Frames {
			f := d.Frames[j]
			if f.Source == sources[i].Name && f.Span.Start >= sources[i].Offset && f.Span.Start < sources[i].Offset+len(sources[i].Text) {
				used = true
			}
		}
		if used {
			appendIncludeFrames(a, d, sources[i].Includes)
		}
	}
	appendIncludeFrames(a, d, condition.Includes)
	return result
}

func configurationCondition(a mem.Allocator, request configurationConditionRequest) (answer PredicateResult) {
	parseName := request.Name
	if request.Located {
		parseName = "<configuration>"
	}
	var parsed *script.Script
	if request.Located {
		offsets := make([]int, len(request.Origins))
		at := 0
		for i := range request.Origins {
			offsets[i] = at
			at += len(request.Origins[i].Text) + 1
		}
		parsed = parseSourceFragments(a, parseName, request.Prefix, request.Origins, offsets)
		mem.FreeSlice(nil, offsets)
	} else {
		parsed = script.ParseKameFile(a, parseName, request.Prefix)
	}
	defer parsed.Free()
	var known []string
	for i := range request.Defines {
		override := definition.ParseOverride(request.Defines[i])
		if override.Name == "" {
			continue
		}
		for j := range parsed.Items {
			d := parsed.Items[j].Definition
			if d != nil && !d.Function && d.Name == override.Name {
				known = append(known, request.Defines[i])
				break
			}
		}
	}
	defer mem.FreeSlice(nil, known)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	options := Options{Defines: known, Environment: request.Environment, Directory: request.Directory}
	if request.Discovery != nil {
		options.Host = request.Discovery.Host
		options.Grants = request.Discovery.Grants
	}
	var origins []CompileSource
	var offsets []int
	if request.Include != nil {
		origins = request.Origins
		at := 0
		for _, origin := range origins {
			offsets = append(offsets, at)
			at += len(origin.Text) + 1
		}
	}
	compiled := compileSources(a, parsed, registry, options, origins, offsets)
	defer compiled.Free(a)
	if compiled.Program == nil {
		if len(compiled.Diagnostics) != 0 {
			d := configurationDiagnostic(a, compiled.Diagnostics[0], request.Located)
			if request.Located {
				offsets := make([]int, len(request.Origins))
				at := 0
				for i := range request.Origins {
					offsets[i] = at
					at += len(request.Origins[i].Text) + 1
				}
				qualifyCompileDiagnostic(a, &d, parseName, request.Origins, offsets)
				mem.FreeSlice(nil, offsets)
			}
			return PredicateResult{Diagnostic: d}
		}
		return PredicateResult{Diagnostic: failure(a, "PARSE_ERR", "conditional configuration could not be compiled")}
	}
	p := compiled.Program
	defer p.Free()
	var expression *expr.Result
	if request.Located {
		if request.Include == nil {
			at := 0
			for i := range request.Origins {
				p.Eval.AddSourcePart(request.Origins[i].Name, at, at+len(request.Origins[i].Text), request.Origins[i].Offset)
				at += len(request.Origins[i].Text) + 1
			}
		}
		// Parse the predicate in a disjoint range, so helper-body offsets cannot
		// accidentally resolve against the predicate's source map.
		combined := strings.Builder{}
		combined.WriteString(request.Prefix)
		combined.WriteByte('\n')
		combined.WriteString(request.Predicate)
		src := source.New(a, parseName, combined.String())
		combined.Reset()
		part := expr.ParseRange(a, src, len(request.Prefix)+1, len(src.Text), "")
		expression = mem.Alloc[expr.Result](a)
		expression.Alloc, expression.Source, expression.Expr, expression.Diagnostics = a, src, part.Expr, part.Diagnostics
		p.Eval.AddSourcePart(request.Name, len(request.Prefix)+1, len(src.Text), request.PredicateOffset)
		if request.Include != nil && expression.Expr != nil {
			p.Eval.SetSourcePartDirectory(request.Include.SourceDirectory)
			scriptView := script.Script{Items: []script.ScriptItem{{Expression: expression.Expr, Span: expression.Expr.Span}}}
			eval.SetScriptSourceOrigin(&scriptView, len(request.Prefix)+1, len(src.Text), len(p.Eval.SourceParts))
		}
	} else {
		expression = expr.Parse(a, request.Name, request.Predicate)
	}
	defer expression.Free()
	if len(expression.Diagnostics) != 0 {
		d := expression.Diagnostics[0]
		location := p.Eval.LocateSource(parseName, d.Span)
		portable := diagnostic.Diagnostic{Source: location.Source, Code: d.Code, Severity: diagnostic.Error, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}, Message: d.Message}
		return PredicateResult{Diagnostic: configurationDiagnostic(a, portable, request.Located)}
	}
	state := planResolverState{Program: p}
	defer mem.FreeSlice(nil, state.Resolving)
	context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: a, Cwd: request.Directory, Source: parseName, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state}
	if request.Discovery != nil {
		p.Options.DeclarationRead, p.Options.DeclarationHost = request.Discovery.DeclarationRead, request.Discovery.DeclarationHost
		declared := &generatedEvaluation{Program: p}
		defer func() {
			answer.Discoveries = p.declarationSnapshots()
			mem.FreeSlice(nil, declared.Stack)
			freeStrings(a, declared.Dependencies)
		}()
		context = p.declarationContext(p.Eval.Scope, declared)
		context.SourceSelection = true
	}
	result := p.Eval.EvaluateWith(expression.Expr, context)
	defer result.Free(a)
	defer eval.FreeEffects(a, context.Effects)
	defer freeStrings(a, context.WritePaths)
	if p.registrationPending {
		return PredicateResult{Pending: true}
	}
	if result.Diagnostic.Code != "" {
		if request.Include != nil {
			return PredicateResult{Diagnostic: result.Diagnostic.Clone(a)}
		}
		return PredicateResult{Diagnostic: configurationDiagnostic(a, result.Diagnostic, request.Located)}
	}
	if result.Waiting || result.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 {
		location := p.Eval.LocateSource(parseName, expression.Expr.Span)
		return PredicateResult{Diagnostic: configurationDiagnostic(a, diagnostic.Diagnostic{Source: location.Source, Code: "PHASE_INVALID", Severity: diagnostic.Error, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}}, request.Located)}
	}
	if request.RequireBoolean && result.Value.Kind != core.Bool {
		d := diagnostic.Diagnostic{Source: request.Name, Code: "EXPR_INVALID", Severity: diagnostic.Error, Span: diagnostic.Span{Start: expression.Expr.Span.Start, End: expression.Expr.Span.End}, Message: "configuration predicate must return bool; got " + core.KindName(result.Value.Kind), Tips: []string{"Use an explicit comparison that returns true or false."}}
		return PredicateResult{Diagnostic: configurationDiagnostic(a, d, request.Located)}
	}
	if request.Include != nil {
		includeError := func(code, message string) PredicateResult {
			d := failure(a, code, message)
			d.Source = cloneText(a, request.Name)
			d.Span = diagnostic.Span{Start: request.PredicateOffset, End: request.PredicateOffset + len(request.Predicate)}
			return PredicateResult{Diagnostic: d}
		}
		const expected = "batch include requires a list of rooted local paths"
		if result.Value.Kind != core.List {
			return includeError("EXPR_INVALID", expected)
		}
		paths := make([]string, 0, len(result.Value.List))
		for _, value := range result.Value.List {
			if !rootedIncludePath(value) {
				return includeError("EXPR_INVALID", expected)
			}
			name := value.Text
			if tail, relative := strings.CutPrefix(name, "~/"); relative {
				home, _ := p.configuredEnvironment("HOME")
				if !path.IsAbs(home) {
					return includeError("FS_ERR", "cannot resolve home directory for include")
				}
				name = path.Join(home, tail)
			} else if !path.IsAbs(name) {
				if request.Include.SourceDirectory == "" {
					return includeError("RES_INVALID", "relative include requires a file-backed source")
				}
				name = path.Join(request.Include.SourceDirectory, name)
			}
			name = path.Clean(name)
			if !context.Allows(eval.Read, name) {
				return includeError("CAP_DENIED", "batch include requires read access; grant the required path with --allow-read=ROOT")
			}
			paths = append(paths, name)
		}
		return PredicateResult{Paths: paths}
	}
	return PredicateResult{Selected: result.Value.Kind != core.Nil && !(result.Value.Kind == core.Bool && !result.Value.Bool)}
}
