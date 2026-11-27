package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"maps"
	"path"
	"strings"
)

type kashSourceState struct {
	parent  *kashSourceState
	unit    ExecutionUnit
	path    string
	args    []core.Value
	request int64
	source  *moduleSource
	pending *kashModule
	failure core.Diagnostic
	defined map[string]bool
	next    int
	done    bool
	operand kashControlState
}

func freeKashSource(a mem.Allocator, value any) {
	state := value.(*kashSourceState)
	state.pending.Free()
	state.failure.Free(a)
	freeValuesWithCallables(a, state.args)
	FreeEffects(a, state.operand.Effects)
	for _, path := range state.operand.WritePaths {
		mem.FreeString(a, path)
	}
}

func (c *Context) directSourceUnit() bool {
	return c.Unit != nil && (c.Unit == c.DirectUnit || c.nativeSource != nil && c.Unit == &c.nativeSource.unit)
}

func sourceArgumentSeparator(words []*expr.Expr) bool {
	return len(words) >= 3 && literalSetupWord(words[2]) && words[2].Parts[0].Text == "--"
}

func (p *Program) nativeSourceCommand(graph, stage *expr.Expr, c *Context) Result {
	if !c.directSourceUnit() || c.asyncHandle != nil || c.Phase != EvaluatePhase ||
		c.Finalizing || c.PrepareDeferred != nil || c.functionStage != nil || c.InputClosed ||
		len(graph.Items) != 1 || graph.Topology || graph.Async || graph.Kind != expr.CommandGraph ||
		len(graph.Body) != 0 || len(c.StageEnvironment) != 0 || c.StageDirectory != "" || !commandOnlyWords(stage) {
		return failure(c.Run, "FEATURE_UNSUP", graph.Span, "native source requires a direct foreground command")
	}
	explicitArgs := sourceArgumentSeparator(stage.Items)
	if len(stage.Items) != 2 && !explicitArgs {
		return failure(c.Run, "PARSE_ERR", graph.Span, "expected source PATH [-- ARG …] or source SHELL PATH as NAME")
	}
	if p.DryRun {
		return Result{}
	}
	start, end, callPath := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, callPath+"/native-source"
	defer func() { c.operationStart, c.operationEnd, c.CallPath = start, end, callPath }()
	state, _ := c.OperationState().(*kashSourceState)
	if state == nil {
		word := stage.Items[1]
		pathWord := *word
		pathWord.CommandGlob, pathWord.CommandHome = false, false
		operands := []*expr.Expr{&pathWord}
		var arguments []*expr.Expr
		if explicitArgs {
			arguments = commandWordArguments(stage.Items[3:])
			operands = append(operands, arguments...)
		}
		before, paths := len(c.Effects), len(c.WritePaths)
		sequenced := c.sequencedOperand
		c.sequencedOperand = true
		evaluated := p.argumentValues(c.Scope, operands, c, graph.Span)
		c.sequencedOperand = sequenced
		r := evaluated.Result
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r
		}
		defer freeValuesWithCallables(c.Run, evaluated.Values)
		value := evaluated.Values[0]
		name := value.Text
		if !value.IsPathText() || name == "" || strings.ContainsRune(name, 0) {
			return failure(c.Run, "EXPR_INVALID", word.Span, "source requires a nonempty local path")
		}
		if rest, ok := strings.CutPrefix(name, "~/"); ok {
			home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
			if !path.IsAbs(home) {
				return failure(c.Run, "RES_INVALID", word.Span, "source requires an absolute HOME for ~/")
			}
			name = path.Join(home, rest)
		}
		filename := c.CanonicalPath(name)
		defer mem.FreeString(c.Run, filename)
		if !path.IsAbs(filename) {
			return failure(c.Run, "RES_INVALID", word.Span, "source requires a local file path")
		}
		if !c.AllowExternalSource && !c.Allows(Read, filename) {
			return failure(c.Run, "CAP_DENIED", word.Span, "source path requires read authority")
		}
		for active := c.nativeSource; active != nil; active = active.parent {
			if active.path == filename {
				return failure(c.Run, "DEP_CYCLE", word.Span, "active Kash source: "+filename)
			}
		}
		id := c.submitSource(filename)
		if id == 0 {
			return failure(c.Run, "HOST_FAIL", word.Span, "source request was not accepted")
		}
		state = &kashSourceState{
			parent: c.nativeSource, path: strings.Clone(filename), request: id,
			unit: ExecutionUnit{environment: maps.Clone(c.Unit.environment), directory: c.Cwd, previous: c.Unit.previous},
		}
		if explicitArgs {
			_, state.args = expandArguments(p.Alloc, arguments, evaluated.Values[1:])
		}
		p.retainControlEffects(&state.operand, c, c.Scope, before, paths)
		c.SetOperationState(state, freeKashSource)
		return Result{Waiting: true}
	}
	replayControlEffects(&state.operand, c)
	if state.done {
		return Result{}
	}
	if state.failure.Code != "" {
		return Result{Diagnostic: state.failure.Clone(c.Run)}
	}
	if state.source == nil {
		if c.Completion().RequestID != state.request {
			return Result{Waiting: true}
		}
		completion := c.TakeCompletion()
		defer completion.Value.Free(c.Run)
		if completion.Diagnostic.Code != "" {
			return Result{Diagnostic: completion.Diagnostic}
		}
		if completion.Value.Kind != core.Bytes {
			return failure(c.Run, "FS_ERR", graph.Span, "cannot read source: "+state.path)
		}
		parsed := script.ParseKash(p.Alloc, state.path, string(completion.Value.Bytes))
		if len(parsed.Diagnostics) != 0 {
			d := parsed.Diagnostics[0]
			r := sourceFailure(c, state.path, d.Code, d.Span, d.Message)
			parsed.Free()
			return r
		}
		for _, item := range parsed.Items {
			if item.Kind == script.Include || item.Kind == script.ModuleExport {
				r := sourceFailure(c, state.path, "PARSE_ERR", item.Span, "runtime source does not support include or export")
				parsed.Free()
				return r
			}
			if item.Definition != nil {
				if state.defined == nil {
					state.defined = make(map[string]bool)
				}
				state.defined[item.Definition.Name] = true
			}
		}
		m := mem.Alloc[kashModule](p.Alloc)
		m.Program = p
		m.Sources = []moduleSource{{Path: owned(p.Alloc, state.path), Parsed: parsed, Signature: core.ContentSignature(completion.Value.Bytes)}}
		state.pending = m
		state.source = &m.Sources[0]
	}
	for state.next < len(state.source.Parsed.Items) {
		item := state.source.Parsed.Items[state.next]
		if item.Kind == script.ModuleImport || item.Kind == script.ModuleFrom {
			r := p.loadImport(item.Module, c, state.path, nil)
			if r.Waiting {
				return r
			}
			if r.Diagnostic.Code != "" {
				return state.failImport(p.Alloc, c, r)
			}
			module := r.Value.Module.Handle.(*kashModule)
			r.Free(c.Run)
			bound := state.source.bindImport(c, item, module, state.defined)
			if bound.Diagnostic.Code != "" {
				return state.failImport(p.Alloc, c, bound)
			}
		}
		state.next++
	}
	if state.pending != nil {
		state.source = p.retainRuntimeSource(state.pending)
		state.pending = nil
	}
	parent, directory := c.Unit, c.Cwd
	c.Unit, c.Cwd, c.nativeSource = &state.unit, state.unit.directory, state
	c.CallPath += "/body"
	args := c.Args
	if explicitArgs {
		args = state.args
	}
	r := p.invokeKashBody(state.source.Parsed, nil, c.Scope, args, c)
	c.Unit, c.Cwd, c.nativeSource = parent, directory, state.parent
	if r.Waiting || r.Completed {
		return r
	}
	if r.Diagnostic.Code != "" {
		if graph.AcceptExit && r.Diagnostic.Cause.HasStatus && functionOutcomeAllowed(r.Diagnostic) {
			r.Free(c.Run)
			state.done = true
			return Result{}
		}
		return r
	}
	r.Free(c.Run)
	parent.environment, parent.directory, parent.previous = state.unit.environment, state.unit.directory, state.unit.previous
	c.Cwd = parent.directory
	state.done = true
	return Result{}
}

func publishSourceDefinitions(caller, loaded *Scope, c *Context) Result {
	// Validate the whole set before publishing any names.
	for _, b := range loaded.Bindings {
		if b.Kind != bindingFunction && b.Kind != bindingLocalDefinition {
			continue
		}
		if existing := caller.localBinding(b.Name); existing != nil && existing.Kind != bindingSourced {
			return failure(c.Run, "DEF_INVALID", c.Span, "source definition conflicts with caller binding: "+b.Name)
		}
	}
	for _, b := range loaded.Bindings {
		if b.Kind == bindingFunction || b.Kind == bindingLocalDefinition {
			caller.hasDescendantCapture = true
			loaded.Retain()
			caller.storeBinding(binding{Kind: bindingSourced, Name: owned(caller.Alloc, b.Name), Source: loaded}, true)
		}
	}
	return Result{}
}

func nameInSource(b *binding, span source.Span, c *Context) Result {
	return name(b.Source, b.Name, nil, span, c)
}

func (state *kashSourceState) failImport(a mem.Allocator, c *Context, r Result) Result {
	if r.Diagnostic.Source == "" {
		r.Diagnostic.Source = owned(c.Run, state.path)
	}
	state.failure = r.Diagnostic.Clone(a)
	state.pending.Free()
	state.pending, state.source = nil, nil
	return r
}

// Reuse immutable syntax and provenance for the same source revision and
// imports. Every invocation still reads the file and resolves imports anew.
// ponytail: keep distinct revisions until session teardown; add eviction when
// all work referencing a source origin can be tracked through retirement.
func (p *Program) retainRuntimeSource(candidate *kashModule) *moduleSource {
	src := &candidate.Sources[0]
	for _, module := range p.Modules {
		if module.Scope != nil {
			continue // ordinary import, not a runtime source
		}
		loaded := &module.Sources[0]
		if loaded.Path == src.Path && loaded.Signature.Equal(src.Signature) &&
			maps.Equal(loaded.imports, src.imports) && maps.Equal(loaded.namespaces, src.namespaces) {
			candidate.Free()
			return loaded
		}
	}
	p.addModuleSourcePart(src)
	p.SourceParts[src.Origin-1].moduleSource = src
	candidate.Handle = candidate
	p.Modules = append(p.Modules, candidate)
	return src
}

func sourceFailure(c *Context, name, code string, span source.Span, message string) Result {
	r := failure(c.Run, code, span, message)
	r.Diagnostic.Source = owned(c.Run, name)
	return r
}
