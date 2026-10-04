// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Program struct {
	KashFunctions []*Function
	NextScriptGroup int64
	// DryRun suppresses invocation-owned process, write and output effects.
	DryRun bool
	Alloc           mem.Allocator
	Engine          *core.Engine
	Script          *script.Script
	Registry        *Registry
	Requests        *host.Queue
	Scope           *Scope
	Definitions     []*definition.Definition
	Nodes           []definitionNode
	Diagnostics     []diagnostic.Diagnostic
	OperationStates []operationState
	Processes []*processTask
	SourceParts     []SourcePart
	// Grants provide the ambient capability policy for lazy definitions. Rule
	// rendering supplies its own context policy from the runtime.
	Grants []Grant
	// DefinitionDependencyObserver installs runtime resources needed by lazy
	// definitions evaluated as standalone engine nodes.
	DefinitionDependencyObserver func(any, core.ResourceKey)
	DefinitionDependencyState    any
	// DefinitionEffectSink receives successful standalone-definition effects.
	// Rule rendering keeps effects on its Context for the build runtime instead.
	DefinitionEffectSink  func(any, Effect)
	DefinitionEffectState any
	// DirectHostRequests applies the embedding host's request loop to lazy
	// definitions instead of requiring native build-graph resources.
	DirectHostRequests bool
	DefinitionArgs     []core.Value
	DefinitionArgsSet  bool
	DefinitionCwd      string
	Valid              bool
}

// CompileResult separates registration diagnostics from an executable program.
// Program is nil whenever Diagnostics contains an error.
type CompileResult struct {
	Program     *Program
	Diagnostics []diagnostic.Diagnostic
}

func (r *CompileResult) Free(a mem.Allocator) {
	for i := range r.Diagnostics {
		r.Diagnostics[i].Free(a)
	}
	slices.Free(a, r.Diagnostics)
	*r = CompileResult{}
}

// CompileChecked performs registration preflight before adding engine nodes.
func CompileChecked(a mem.Allocator, engine *core.Engine, parsed *script.Script, registry *Registry) CompileResult {
	result := CompileResult{}
	if parsed == nil {
		return result
	}
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Severity != source.Error {
			continue
		}
		result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: parsed.Diagnostics[i].Code, Severity: diagnostic.Error, Message: parsed.Diagnostics[i].Message, Span: diagnostic.Span{Start: parsed.Diagnostics[i].Span.Start, End: parsed.Diagnostics[i].Span.End}})
	}
	var names []string
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind == script.When || item.Kind == script.Otherwise || item.Kind == script.EndWhen {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: "declaration conditionals require source composition before registration", Span: diagnostic.Span{Start: item.Span.Start, End: item.Span.End}})
			continue
		}
		if item.Kind != script.Definition || item.Definition == nil {
			continue
		}
		duplicate := false
		for j := range names {
			if names[j] == item.Definition.Name {
				duplicate = true
				break
			}
		}
		if duplicate {
			if item.Definition.Default { continue }
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "duplicate definition", Span: diagnostic.Span{Start: item.Definition.NameSpan.Start, End: item.Definition.NameSpan.End}})
		} else {
			names = slices.Append(a, names, item.Definition.Name)
		}
	}
	slices.Free(a, names)
	if len(result.Diagnostics) != 0 {
		return result
	}
	result.Program = Compile(a, engine, parsed, registry)
	return result
}

type definitionNode struct {
	Name string
	Node *core.Node
}

func Compile(a mem.Allocator, engine *core.Engine, parsed *script.Script, registry *Registry) *Program {
	if engine == nil || parsed == nil {
		return nil
	}
	p := mem.Alloc[Program](a)
	p.Alloc, p.Engine, p.Script, p.Registry = a, engine, parsed, registry
	p.Requests = host.NewQueue(a)
	p.Valid = true
	p.Scope = newScope(a, nil)
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind == script.When || item.Kind == script.Otherwise || item.Kind == script.EndWhen {
			p.Valid = false
			p.Diagnostics = slices.Append(a, p.Diagnostics, diagnostic.Diagnostic{Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: "declaration conditionals require source composition before registration", Span: diagnostic.Span{Start: item.Span.Start, End: item.Span.End}})
		}
	}
	if !p.Valid {
		return p
	}
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Definition || item.Definition == nil {
			continue
		}
		d := item.Definition
		if p.Scope.lookup(d.Name) != nil {
			if d.Default { continue }
			p.Valid = false
			p.Diagnostics = slices.Append(a, p.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "duplicate definition", Span: diagnostic.Span{Start: d.NameSpan.Start, End: d.NameSpan.End}})
			continue
		}
		if d.Function {
			function := mem.Alloc[Function](a)
			function.Kind, function.Parameters, function.Expression, function.Scope, function.ParametersOwned, function.Definition = FunctionDefinition, convertParameters(a, d.Parameters), d.Expression, p.Scope, true, d
			p.Scope.setFunction(d.Name, function)
			continue
		}
		p.Scope.setDefinition(d.Name)
		p.Definitions = slices.Append(a, p.Definitions, d)
		context := mem.Alloc[definitionState](a)
		context.Program, context.Definition = p, d
		node := engine.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: d.Name}, evaluateDefinition, context, freeDefinitionState)
		node.Restartable = true
		p.Nodes = slices.Append(a, p.Nodes, definitionNode{Name: owned(a, d.Name), Node: node})
	}
	return p
}

// Free releases evaluator-owned compilation state. The parsed script and registry
// are owned by their callers and must outlive the program's engine nodes.
func (p *Program) Free() {
	if p == nil {
		return
	}
	p.freeSourceParts()
	for i := range p.Processes { freeProcessTask(p.Alloc, p.Processes[i]) }
	slices.Free(p.Alloc, p.Processes)
	p.Processes = nil
	p.Scope.Free()
	p.Requests.Free()
	for i := range p.Nodes {
		mem.FreeString(p.Alloc, p.Nodes[i].Name)
	}
	for i := range p.OperationStates {
		mem.FreeString(p.Alloc, p.OperationStates[i].CallPath)
		if p.OperationStates[i].Free != nil {
			p.OperationStates[i].Free(p.Alloc, p.OperationStates[i].Value)
		}
	}
	slices.Free(p.Alloc, p.Nodes)
	slices.Free(p.Alloc, p.OperationStates)
	p.freeKashFunctions()
	slices.Free(p.Alloc, p.Definitions)
	for i := range p.Diagnostics {
		p.Diagnostics[i].Free(p.Alloc)
	}
	for i := range p.Grants {
		slices.Free(p.Alloc, p.Grants[i].Names)
	}
	for i := range p.DefinitionArgs {
		p.DefinitionArgs[i].Free(p.Alloc)
	}
	mem.FreeString(p.Alloc, p.DefinitionCwd)
	slices.Free(p.Alloc, p.Diagnostics)
	slices.Free(p.Alloc, p.Grants)
	slices.Free(p.Alloc, p.DefinitionArgs)
	mem.Free(p.Alloc, p)
}

// SetGrants installs the capability policy used when lazy definitions run as
// standalone engine nodes.
func (p *Program) SetGrants(grants []Grant) {
	if p == nil {
		return
	}
	for i := range p.Grants {
		slices.Free(p.Alloc, p.Grants[i].Names)
	}
	slices.Free(p.Alloc, p.Grants)
	for i := range grants {
		grant := Grant{Capability: grants[i].Capability, Names: slices.Clone(p.Alloc, grants[i].Names)}
		p.Grants = slices.Append(p.Alloc, p.Grants, grant)
	}
}

// SetDefinitionDependencyObserver configures resource registration for lazy
// definitions. The runtime owns state for at least as long as this Program.
func (p *Program) SetDefinitionDependencyObserver(observer func(any, core.ResourceKey), state any) {
	if p == nil {
		return
	}
	p.DefinitionDependencyObserver, p.DefinitionDependencyState = observer, state
}

// SetDefinitionEffectSink installs the output destination for standalone lazy
// definitions. It is called only after an evaluation completes successfully.
func (p *Program) SetDefinitionEffectSink(sink func(any, Effect), state any) {
	if p == nil {
		return
	}
	p.DefinitionEffectSink, p.DefinitionEffectState = sink, state
}

func (p *Program) SetDefinitionArgs(values []core.Value) {
	if p == nil {
		return
	}
	for i := range p.DefinitionArgs {
		p.DefinitionArgs[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, p.DefinitionArgs)
	for i := range values {
		p.DefinitionArgs = slices.Append(p.Alloc, p.DefinitionArgs, values[i].Clone(p.Alloc))
	}
	p.DefinitionArgsSet = true
}

// SetDefinitionCwd supplies the working directory to standalone lazy
// definitions. Rule rendering supplies Cwd through its own evaluation context.
func (p *Program) SetDefinitionCwd(cwd string) {
	if p == nil {
		return
	}
	mem.FreeString(p.Alloc, p.DefinitionCwd)
	p.DefinitionCwd = owned(p.Alloc, cwd)
}

func (p *Program) Definition(name string) *core.Node {
	if p == nil || !p.Valid {
		return nil
	}
	for i := range p.Nodes {
		if p.Nodes[i].Name == name {
			return p.Nodes[i].Node
		}
	}
	return nil
}

type definitionState struct {
	Program    *Program
	Definition *definition.Definition
	Owned      bool
}

func freeDefinitionState(a mem.Allocator, value any) {
	state := value.(*definitionState)
	if state.Owned { definition.Free(a, state.Definition) }
	mem.Free(a, state)
}

func evaluateDefinition(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := mem.Alloc[definitionSourceState](c.Allocator())
	state.Alloc = c.Allocator()
	if !c.AttachSource(core.NewSource(c.Allocator(), pollDefinition, freeDefinitionSource, state)) {
		mem.Free(c.Allocator(), state)
		return core.ProducerFailed
	}
	return core.ProducerActive
}

// definitionSourceState owns source-lifetime allocation state. Published
// definition values immediately return to dependency waiting, so each wake
// evaluates one latest dependency snapshot without a busy loop.
type definitionSourceState struct {
	Alloc           mem.Allocator
	SkipAfterStream bool
}

func pollDefinition(c *core.EngineContext, source *core.Source, atom *core.Atom) core.PollResult {
	state := source.State.(*definitionSourceState)
	if state.SkipAfterStream {
		state.SkipAfterStream = false
		return core.PollWaiting
	}
	definition := c.Context().(*definitionState)
	result := definition.Program.definition(c, definition.Definition)
	if c.Failed() && result.Diagnostic.Code == "" {
		d := c.Diagnostic()
		atom.Kind, atom.Diagnostic = core.AtomFailed, d.Clone(c.Allocator())
		return core.PollEmitted
	}
	if result.Waiting {
		return core.PollWaiting
	}
	if result.Diagnostic.Code != "" {
		atom.Kind, atom.Diagnostic = core.AtomFailed, result.Diagnostic
		return core.PollEmitted
	}
	if result.Stream != nil {
		stream := mem.Alloc[operationStreamState](state.Alloc)
		stream.Alloc, stream.Source, stream.Definition = state.Alloc, result.Stream, state
		result.Stream = nil
		atom.Kind = core.AtomNested
		atom.Nested = core.NewSource(state.Alloc, pollOperationStream, freeOperationStream, stream)
		return core.PollEmitted
	}
	if result.Completed {
		return core.PollWaiting
	}
	if result.Value.HasTransientCallable() {
		freeCallables(state.Alloc, &result.Value)
		result.Value.Free(state.Alloc)
		atom.Kind = core.AtomFailed
		atom.Diagnostic = diagnostic.Diagnostic{Code: "EXPR_INVALID", Severity: diagnostic.Error, Message: "callable cannot cross evaluation boundary"}
		return core.PollEmitted
	}
	atom.Kind, atom.Value, atom.Wait = core.AtomValue, result.Value, true
	return core.PollEmitted
}

type operationStreamState struct {
	Alloc      mem.Allocator
	Source     *core.Source
	Definition *definitionSourceState
}

func pollOperationStream(c *core.EngineContext, source *core.Source, atom *core.Atom) core.PollResult {
	state := source.State.(*operationStreamState)
	return state.Source.Poll(c, state.Source, atom)
}

func freeOperationStream(source *core.Source) {
	state := source.State.(*operationStreamState)
	core.FreeSource(state.Alloc, state.Source)
	state.Definition.SkipAfterStream = true
	mem.Free(state.Alloc, state)
}

func freeDefinitionSource(source *core.Source) {
	state := source.State.(*definitionSourceState)
	mem.Free(state.Alloc, state)
}

func (p *Program) definition(engine *core.EngineContext, d *definition.Definition) Result {
	context := &Context{Program: p, Engine: engine, Scope: p.Scope, Run: p.Alloc, Requests: p.Requests, Cwd: p.DefinitionCwd, Source: p.Script.Source.Name, Grants: p.Grants, Args: p.DefinitionArgs, HasArgs: p.DefinitionArgsSet, DependencyObserver: p.DefinitionDependencyObserver, ResolverState: p.DefinitionDependencyState, DirectHostRequests: p.DirectHostRequests}
	result := p.definitionValue(engine, d, p.Scope, context)
	if engine == nil || !engine.Failed() {
		attachNamedFrame(&result, context, d.Span, "definition", d.Name)
	}
	attachSource(&result, context)
	if !result.Waiting && !result.Completed && result.Stream == nil && result.Diagnostic.Code == "" && !result.Value.HasCallable() && p.DefinitionEffectSink != nil {
		for i := range context.Effects {
			p.DefinitionEffectSink(p.DefinitionEffectState, context.Effects[i])
		}
	}
	FreeEffects(p.Alloc, context.Effects)
	return result
}

func (p *Program) definitionValue(engine *core.EngineContext, d *definition.Definition, scope *Scope, context *Context) Result {
	if d.ValueKind == definition.ValueExpression {
		return p.evaluate(engine, scope, d.Expression, context)
	}
	if d.ValueKind == definition.ValueTemplate {
		return p.Render(context.Run, d.Template, scope, context)
	}
	values := slices.Make[core.Value](context.Run, len(d.Words))
	for i := range d.Words {
		r := p.Render(context.Run, d.Words[i].Template, scope, context)
		if r.Waiting || r.Diagnostic.Code != "" {
			// Render yields strings only, so no callable shares storage here,
			// but discard deeply for uniformity with other discard paths.
			freeValuesWithCallables(context.Run, values)
			return r
		}
		values[i] = r.Value
	}
	if len(values) == 1 {
		result := Result{Value: values[0]}
		values[0] = core.Value{}
		slices.Free(context.Run, values)
		return result
	}
	result := Result{Value: core.NewList(context.Run, values)}
	freeValues(context.Run, values)
	return result
}

func convertParameters(a mem.Allocator, values []definition.Parameter) []expr.Parameter {
	result := slices.Make[expr.Parameter](a, len(values))
	for i := range values {
		result[i] = expr.Parameter{Name: values[i].Name, Span: values[i].Span, Rest: values[i].Rest}
	}
	return result
}
