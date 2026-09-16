// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host"
	"littlemake/lang/definition"
	"littlemake/lang/expr"
	"littlemake/lang/script"
	"littlemake/lang/source"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

type Capability int

const (
	Read Capability = iota
	Write
	Run
	Env
)

type Grant struct {
	Capability Capability
	Names      []string
}

type OperationCall func(*Context, any, []core.Value) Result
type ContextFree func(mem.Allocator, any)

type Operation struct {
	Name         string
	Call         OperationCall
	Context      any
	FreeContext  ContextFree
	MinArity     int
	MaxArity     int // -1 accepts any remaining arguments.
	Capabilities []Capability
	Version      string
}

type Registry struct {
	Alloc mem.Allocator
	Items []Operation
}

// TaskKeyResult identifies one operation implementation for a cached task.
// Callers own Key when OK is true.
type TaskKeyResult struct { Key core.ResourceKey; OK bool }

// TaskKey includes the stable operation version so a new implementation cannot
// reuse a cached result produced by an older one.
func (r *Registry) TaskKey(a mem.Allocator, task string, operation string) TaskKeyResult {
	o := r.lookup(operation)
	if o == nil { return TaskKeyResult{} }
	b := strings.NewBuilder(a)
	defer b.Free()
	b.WriteString(task); b.WriteByte('\x00'); b.WriteString(o.Name); b.WriteByte('\x00'); b.WriteString(o.Version)
	return TaskKeyResult{Key: core.NewResourceKey(a, core.ResourceTask, b.String()), OK: true}
}

func NewRegistry(a mem.Allocator) *Registry {
	r := mem.Alloc[Registry](a)
	r.Alloc = a
	return r
}

func (r *Registry) Add(operation Operation) bool {
	if r == nil || operation.Name == "" || operation.Call == nil || r.lookup(operation.Name) != nil { return false }
	operation.Name = owned(r.Alloc, operation.Name)
	operation.Version = owned(r.Alloc, operation.Version)
	operation.Capabilities = slices.Clone(r.Alloc, operation.Capabilities)
	r.Items = slices.Append(r.Alloc, r.Items, operation)
	return true
}

func (r *Registry) lookup(name string) *Operation {
	if r == nil { return nil }
	for i := range r.Items { if r.Items[i].Name == name { return &r.Items[i] } }
	return nil
}

func (r *Registry) Free() {
	if r == nil { return }
	for i := range r.Items {
		o := &r.Items[i]
		if o.FreeContext != nil { o.FreeContext(r.Alloc, o.Context) }
		mem.FreeString(r.Alloc, o.Name)
		mem.FreeString(r.Alloc, o.Version)
		slices.Free(r.Alloc, o.Capabilities)
	}
	slices.Free(r.Alloc, r.Items)
	mem.Free(r.Alloc, r)
}

type Result struct {
	Value      core.Value
	Waiting    bool
	Completed  bool
	Stream     *core.Source
	Diagnostic diagnostic.Diagnostic
}

func (r *Result) Free(a mem.Allocator) {
	freeCallables(a, &r.Value)
	r.Value.Free(a)
	if r.Stream != nil { core.FreeSource(a, r.Stream) }
	r.Diagnostic.Free(a)
	*r = Result{}
}

func failure(code string, span source.Span, message string) Result {
	return Result{Diagnostic: diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: span.Start, End: span.End}}}
}

func diagnosticSeverity(severity source.Severity) diagnostic.Severity {
	if severity == source.Warning { return diagnostic.Warning }
	return diagnostic.Error
}

type bindingKind int

const (
	bindingValue bindingKind = iota
	bindingFunction
	bindingDefinition
)

type binding struct {
	Kind       bindingKind
	Name       string
	Value      core.Value
	Function   *Function
	Definition core.ResourceKey
}

type Scope struct {
	Alloc    mem.Allocator
	Parent   *Scope
	Bindings []binding
	References int
}

func newScope(a mem.Allocator, parent *Scope) *Scope {
	s := mem.Alloc[Scope](a)
	s.Alloc, s.Parent, s.References = a, parent, 1
	if parent != nil { parent.Retain() }
	return s
}

func (s *Scope) Retain() { if s != nil { s.References++ } }

func (s *Scope) setValue(name string, value core.Value) {
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingValue, Name: owned(s.Alloc, name), Value: value.Clone(s.Alloc)})
}

func (s *Scope) setFunction(name string, function *Function) {
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingFunction, Name: owned(s.Alloc, name), Function: function})
}

func (s *Scope) setDefinition(name string) {
	key := core.NewResourceKey(s.Alloc, core.ResourceDefinition, name)
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingDefinition, Name: owned(s.Alloc, name), Definition: key})
}

func (s *Scope) lookup(name string) *binding {
	for current := s; current != nil; current = current.Parent {
		for i := len(current.Bindings) - 1; i >= 0; i-- { if current.Bindings[i].Name == name { return &current.Bindings[i] } }
	}
	return nil
}

func (s *Scope) Free() {
	if s == nil { return }
	s.References--
	if s.References != 0 { return }
	for i := range s.Bindings {
		mem.FreeString(s.Alloc, s.Bindings[i].Name)
		if s.Bindings[i].Kind == bindingValue { freeCallables(s.Alloc, &s.Bindings[i].Value); s.Bindings[i].Value.Free(s.Alloc) }
		if s.Bindings[i].Kind == bindingDefinition { s.Bindings[i].Definition.Free(s.Alloc) }
		if s.Bindings[i].Kind == bindingFunction && s.Bindings[i].Function.Owned {
			if s.Bindings[i].Function.ParametersOwned {
				if s.Bindings[i].Function.ParameterNamesOwned { for j := range s.Bindings[i].Function.Parameters { mem.FreeString(s.Alloc, s.Bindings[i].Function.Parameters[j].Name) } }
				slices.Free(s.Alloc, s.Bindings[i].Function.Parameters)
			}
			if s.Bindings[i].Function.BodyOwned { for j := range s.Bindings[i].Function.Body { expr.Free(s.Alloc, s.Bindings[i].Function.Body[j]) }; slices.Free(s.Alloc, s.Bindings[i].Function.Body) }
			mem.Free(s.Alloc, s.Bindings[i].Function)
		}
	}
	slices.Free(s.Alloc, s.Bindings)
	parent := s.Parent
	mem.Free(s.Alloc, s)
	parent.Free()
}

type Function struct {
	Parameters []expr.Parameter
	Body       []*expr.Expr
	Expression *expr.Expr
	Scope      *Scope
	Temporary  bool
	Owned      bool
	ParametersOwned bool
	ParameterNamesOwned bool
	BodyOwned  bool
	Definition *definition.Definition
}

type Program struct {
	Alloc      mem.Allocator
	Engine     *core.Engine
	Script     *script.Script
	Registry   *Registry
	Requests   *host.Queue
	Scope      *Scope
	Definitions []*definition.Definition
	Nodes       []definitionNode
	Diagnostics []diagnostic.Diagnostic
	Valid       bool
}

// CompileResult separates registration diagnostics from an executable program.
// Program is nil whenever Diagnostics contains an error.
type CompileResult struct {
	Program     *Program
	Diagnostics []diagnostic.Diagnostic
}

func (r *CompileResult) Free(a mem.Allocator) {
	for i := range r.Diagnostics { r.Diagnostics[i].Free(a) }
	slices.Free(a, r.Diagnostics)
	*r = CompileResult{}
}

// CompileChecked performs registration preflight before adding engine nodes.
func CompileChecked(a mem.Allocator, engine *core.Engine, parsed *script.Script, registry *Registry) CompileResult {
	result := CompileResult{}
	if parsed == nil { return result }
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Severity != source.Error { continue }
		result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: parsed.Diagnostics[i].Code, Severity: diagnostic.Error, Message: parsed.Diagnostics[i].Message, Span: diagnostic.Span{Start: parsed.Diagnostics[i].Span.Start, End: parsed.Diagnostics[i].Span.End}})
	}
	var names []string
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Definition || item.Definition == nil { continue }
		duplicate := false
		for j := range names { if names[j] == item.Definition.Name { duplicate = true; break } }
		if duplicate {
			result.Diagnostics = slices.Append(a, result.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "duplicate definition", Span: diagnostic.Span{Start: item.Definition.NameSpan.Start, End: item.Definition.NameSpan.End}})
		} else { names = slices.Append(a, names, item.Definition.Name) }
	}
	slices.Free(a, names)
	if len(result.Diagnostics) != 0 { return result }
	result.Program = Compile(a, engine, parsed, registry)
	return result
}

type definitionNode struct { Name string; Node *core.Node }

func Compile(a mem.Allocator, engine *core.Engine, parsed *script.Script, registry *Registry) *Program {
	if engine == nil || parsed == nil { return nil }
	p := mem.Alloc[Program](a)
	p.Alloc, p.Engine, p.Script, p.Registry = a, engine, parsed, registry
	p.Requests = host.NewQueue(a)
	p.Valid = true
	p.Scope = newScope(a, nil)
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Definition || item.Definition == nil { continue }
		d := item.Definition
		if p.Scope.lookup(d.Name) != nil {
			p.Valid = false
			p.Diagnostics = slices.Append(a, p.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "duplicate definition", Span: diagnostic.Span{Start: d.NameSpan.Start, End: d.NameSpan.End}})
			continue
		}
		if d.Function {
			function := mem.Alloc[Function](a)
			function.Parameters, function.Expression, function.Scope, function.Owned, function.ParametersOwned, function.Definition = convertParameters(a, d.Parameters), d.Expression, p.Scope, true, true, d
			p.Scope.setFunction(d.Name, function)
			continue
		}
		p.Scope.setDefinition(d.Name)
		p.Definitions = slices.Append(a, p.Definitions, d)
		context := mem.Alloc[definitionState](a)
		context.Program, context.Definition = p, d
		node := engine.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: d.Name}, evaluateDefinition, context, freeDefinitionState)
		p.Nodes = slices.Append(a, p.Nodes, definitionNode{Name: owned(a, d.Name), Node: node})
	}
	return p
}

// Free releases evaluator-owned compilation state. The parsed script and registry
// are owned by their callers and must outlive the program's engine nodes.
func (p *Program) Free() {
	if p == nil { return }
	p.Scope.Free()
	p.Requests.Free()
	for i := range p.Nodes { mem.FreeString(p.Alloc, p.Nodes[i].Name) }
	slices.Free(p.Alloc, p.Nodes)
	slices.Free(p.Alloc, p.Definitions)
	for i := range p.Diagnostics { p.Diagnostics[i].Free(p.Alloc) }
	slices.Free(p.Alloc, p.Diagnostics)
	mem.Free(p.Alloc, p)
}

func (p *Program) Definition(name string) *core.Node {
	if p == nil || !p.Valid { return nil }
	for i := range p.Nodes { if p.Nodes[i].Name == name { return p.Nodes[i].Node } }
	return nil
}

type definitionState struct { Program *Program; Definition *definition.Definition }
func freeDefinitionState(a mem.Allocator, value any) { mem.Free(a, value.(*definitionState)) }

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
	Alloc            mem.Allocator
	SkipAfterStream  bool
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
		atom.Kind, atom.Diagnostic = core.AtomFailed, c.Diagnostic()
		return core.PollEmitted
	}
	if result.Waiting { return core.PollWaiting }
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
	if result.Completed { return core.PollWaiting }
	if result.Value.HasCallable() {
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
	Alloc       mem.Allocator
	Source      *core.Source
	Definition  *definitionSourceState
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

func freeCallables(a mem.Allocator, value *core.Value) {
	if value.Kind == core.Callable {
		function := value.Callable.(*Function)
		if function.Temporary { function.Scope.Free(); mem.Free(a, function) }
		value.Callable = nil
		return
	}
	if value.Kind == core.List { for i := range value.List { freeCallables(a, &value.List[i]) } }
	if value.Kind == core.Record { for i := range value.Record { freeCallables(a, &value.Record[i].Value) } }
}

func (p *Program) definition(engine *core.EngineContext, d *definition.Definition) Result {
	context := &Context{Program: p, Engine: engine, Scope: p.Scope, Run: p.Alloc, Requests: p.Requests, Source: p.Script.Source.Name}
	result := p.definitionValue(engine, d, p.Scope, context)
	if engine == nil || !engine.Failed() { attachFrame(&result, context, d.Span, "definition") }
	attachSource(&result, context)
	return result
}

func (p *Program) definitionValue(engine *core.EngineContext, d *definition.Definition, scope *Scope, context *Context) Result {
	if d.ValueKind == definition.ValueExpression { return p.evaluate(engine, scope, d.Expression, context) }
	if d.ValueKind == definition.ValueTemplate { return p.Render(context.Run, d.Template, scope, context) }
	values := slices.Make[core.Value](context.Run, len(d.Words))
	for i := range d.Words {
		r := p.Render(context.Run, d.Words[i].Template, scope, context)
		if r.Waiting || r.Diagnostic.Code != "" { freeValues(context.Run, values); return r }
		values[i] = r.Value
	}
	if len(values) == 1 { result := Result{Value: values[0]}; values[0] = core.Value{}; slices.Free(context.Run, values); return result }
	result := Result{Value: core.NewList(context.Run, values)}
	freeValues(context.Run, values)
	return result
}

func convertParameters(a mem.Allocator, values []definition.Parameter) []expr.Parameter {
	result := slices.Make[expr.Parameter](a, len(values))
	for i := range values { result[i] = expr.Parameter{Name: values[i].Name, Span: values[i].Span, Rest: values[i].Rest} }
	return result
}

type Context struct {
	Program *Program
	Engine  *core.EngineContext
	Scope   *Scope
	Run     mem.Allocator
	Requests *host.Queue
	Cwd     string
	Frames  []diagnostic.Frame
	Source  string
	Grants  []Grant
	Inputs  []core.Value
	Outputs []core.Value
	Args    []core.Value
	RuleFrames []RuleFrame
	denied  bool
	activeCapabilities []Capability
}

// RuleFrame supplies the inputs and outputs for one enclosing rule evaluation.
// Selectors use the most recently pushed frame.
type RuleFrame struct {
	Inputs  []core.Value
	Outputs []core.Value
}

func (c *Context) allowed(capability Capability) bool {
	for i := range c.Grants { if c.Grants[i].Capability == capability { return true } }
	return false
}

// Allows checks an unrestricted grant or a lexical root/name grant. Filesystem
// operations pass their canonical path; environment operations pass the name.
func (c *Context) Allows(capability Capability, name string) bool {
	a := c.Run
	if a == nil { a = mem.System }
	canonicalName := canonicalPath(a, c.Cwd, name)
	defer mem.FreeString(a, canonicalName)
	for i := range c.Grants {
		grant := c.Grants[i]
		if grant.Capability != capability { continue }
		if len(grant.Names) == 0 { return true }
		for j := range grant.Names {
			root := canonicalPath(a, c.Cwd, grant.Names[j])
			allowed := canonicalName == root || (len(canonicalName) > len(root) && len(root) != 0 && canonicalName[:len(root)] == root && canonicalName[len(root)] == '/')
			mem.FreeString(a, root)
			if allowed { return true }
		}
	}
	return false
}

func canonicalPath(a mem.Allocator, cwd string, name string) string {
	if path.IsAbs(name) { return path.Clean(a, name) }
	if cwd == "" { return path.Clean(a, name) }
	return path.Join(a, cwd, name)
}

func (c *Context) Dependency(key core.ResourceKey) bool {
	if c.Engine == nil { return false }
	return c.Engine.Dependency(key)
}

// Value returns a borrowed current dependency value after Dependency accepted it.
func (c *Context) Value(key core.ResourceKey) core.CurrentValue {
	if c.Engine == nil { return core.CurrentValue{} }
	return c.Engine.Value(key)
}

// Submit queues an owned request and records its generated ID on the active
// engine node. The host completes it using the request correlation data.
func (c *Context) Submit(kind host.RequestKind, payload core.Value) int64 {
	if !c.requestAllowed(kind, payload) { c.denied = true; return 0 }
	if c.Engine == nil || c.Requests == nil { return 0 }
	id := c.Requests.Submit(c.Engine.NodeID(), c.Engine.Generation(), c.Engine.Attempt(), kind, payload)
	if id != 0 { c.Engine.Submit(id) }
	return id
}

func (c *Context) requestAllowed(kind host.RequestKind, payload core.Value) bool {
	var capability Capability
	switch kind {
	case host.RequestReadFile: capability = Read
	case host.RequestWriteFile: capability = Write
	case host.RequestProcess: capability = Run
	case host.RequestEnvironment: capability = Env
	default:
		for i := range c.activeCapabilities {
			unrestricted := false
			for j := range c.Grants { if c.Grants[j].Capability == c.activeCapabilities[i] && len(c.Grants[j].Names) == 0 { unrestricted = true; break } }
			if !unrestricted { return false }
		}
		return true
	}
	if payload.Kind != core.String {
		for i := range c.Grants { if c.Grants[i].Capability == capability && len(c.Grants[i].Names) == 0 { return true } }
		return false
	}
	return c.Allows(capability, payload.Text)
}

// Completion returns the host completion that resumed this evaluation, if any.
func (c *Context) Completion() core.Completion {
	if c.Engine == nil { return core.Completion{} }
	return c.Engine.Completion()
}

// Call invokes a lexical function with already evaluated values.
func (c *Context) Call(callable core.Value, values []core.Value) Result {
	if c == nil || c.Program == nil || callable.Kind != core.Callable { return failure("EXPR_INVALID", source.Span{}, "expected a function") }
	return c.Program.callValues(callable.Callable.(*Function), values, c, source.Span{})
}

// FreeCallable releases a temporary function after an operation has finished
// invoking it. Named functions remain owned by their lexical scope.
func (c *Context) FreeCallable(callable *core.Value) { freeCallables(c.Run, callable) }

func (p *Program) Evaluate(run mem.Allocator, expression *expr.Expr, scope *Scope) Result {
	return p.evaluate(nil, scope, expression, &Context{Program: p, Scope: scope, Run: run, Requests: p.Requests})
}

// EvaluateWith evaluates an expression with caller-provided capability grants
// and selector frames. The context remains caller-owned.
func (p *Program) EvaluateWith(expression *expr.Expr, context *Context) Result {
	if p == nil || context == nil { return failure("EXPR_INVALID", source.Span{}, "missing evaluation context") }
	if context.Program == nil { context.Program = p }
	if context.Scope == nil { context.Scope = p.Scope }
	if context.Requests == nil { context.Requests = p.Requests }
	result := p.evaluate(context.Engine, context.Scope, expression, context)
	if result.Diagnostic.Code != "" && len(context.Frames) != 0 {
		frames := slices.Make[diagnostic.Frame](context.Run, len(context.Frames)+len(result.Diagnostic.Frames))
		copy(frames, context.Frames)
		copy(frames[len(context.Frames):], result.Diagnostic.Frames)
		slices.Free(context.Run, result.Diagnostic.Frames)
		result.Diagnostic.Frames = frames
	}
	attachSource(&result, context)
	return result
}

func attachSource(result *Result, context *Context) {
	// Context and program sources outlive their diagnostic results. Diagnostics
	// borrowed from engine nodes must not be cloned or freed here.
	if result.Diagnostic.Code != "" && context.Source != "" && !result.Diagnostic.Owned { result.Diagnostic.Target = context.Source }
}

func (p *Program) evaluate(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
	if expression == nil { return failure("EXPR_INVALID", source.Span{}, "definition needs an expression value") }
	context.Engine, context.Scope, context.Requests = engine, scope, p.Requests
	switch expression.Kind {
	case expr.Nil: return Result{Value: core.Value{Kind: core.Nil}}
	case expr.Boolean: return Result{Value: core.Value{Kind: core.Bool, Bool: expression.Bool}}
	case expr.Integer: return Result{Value: core.Value{Kind: core.Int, Int: expression.Int}}
	case expr.Float: return Result{Value: core.Value{Kind: core.Float, Float: expression.Float}}
	case expr.String: return p.stringValue(scope, expression.Parts, context)
	case expr.Symbol, expr.Path: return Result{Value: core.NewString(context.Run, expression.Text)}
	case expr.Name: return name(scope, expression.Text, expression.Span, context)
	case expr.Reference: return p.reference(scope, expression, context)
	case expr.List: return p.list(scope, expression.Items, context)
	case expr.Record: return p.record(scope, expression.Fields, context)
	case expr.Application: return p.application(scope, expression, context)
	case expr.Lambda:
		function := mem.Alloc[Function](context.Run)
		function.Parameters, function.Body, function.Scope, function.Temporary = expression.Parameters, expression.Body, scope, true
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Selector: return p.selector(expression.Text, expression.Span, context)
	}
	return failure("EXPR_INVALID", expression.Span, "invalid expression")
}

func name(scope *Scope, name string, span source.Span, context *Context) Result {
	b := scope.lookup(name)
	if b == nil { return failure("REF_MISSING", span, "unknown reference: "+name) }
	if b.Kind == bindingValue { return Result{Value: b.Value.Clone(context.Run)} }
	if b.Kind == bindingFunction { return Result{Value: core.Value{Kind: core.Callable, Callable: b.Function}} }
	if context.Engine == nil || !context.Engine.Dependency(b.Definition) {
		if context.Engine != nil && context.Engine.Failed() { return Result{Diagnostic: context.Engine.Diagnostic()} }
		return Result{Waiting: true}
	}
	value := context.Engine.Value(b.Definition)
	if !value.OK { return Result{Waiting: true} }
	return Result{Value: value.Value.Clone(context.Run)}
}

func (p *Program) list(scope *Scope, items []*expr.Expr, context *Context) Result {
	values := slices.Make[core.Value](context.Run, len(items))
	for i := range items {
		r := p.evaluate(context.Engine, scope, items[i], context)
		if r.Waiting || r.Diagnostic.Code != "" { freeValues(context.Run, values); return r }
		values[i] = r.Value
	}
	result := Result{Value: core.NewList(context.Run, values)}
	freeValues(context.Run, values)
	return result
}

func (p *Program) record(scope *Scope, fields []expr.Field, context *Context) Result {
	values := slices.Make[core.RecordField](context.Run, len(fields))
	for i := range fields {
		r := p.evaluate(context.Engine, scope, fields[i].Value, context)
		if r.Waiting || r.Diagnostic.Code != "" { freeRecord(context.Run, values); return r }
		values[i] = core.RecordField{Key: fields[i].Key, Value: r.Value}
	}
	result := Result{Value: core.NewRecord(context.Run, values)}
	freeRecord(context.Run, values)
	return result
}

func (p *Program) stringValue(scope *Scope, parts []expr.StringPart, context *Context) Result {
	b := strings.NewBuilder(context.Run)
	defer b.Free()
	for i := range parts {
		if parts[i].Expr == nil { b.WriteString(parts[i].Text); continue }
		r := p.evaluate(context.Engine, scope, parts[i].Expr, context)
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		text, ok := stringify(context.Run, r.Value)
		r.Value.Free(context.Run)
		if !ok { return failure("EXPR_INVALID", parts[i].Span, "records and bytes require explicit text conversion") }
		b.WriteString(text); mem.FreeString(context.Run, text)
	}
	return Result{Value: core.NewString(context.Run, b.String())}
}

func stringify(a mem.Allocator, value core.Value) (string, bool) {
	if value.Kind == core.String { return owned(a, value.Text), true }
	if value.Kind == core.Nil { return "", true }
	if value.Kind == core.Bool { if value.Bool { return owned(a, "true"), true }; return owned(a, "false"), true }
	if value.Kind == core.Int { var buffer [strconv.MaxIntBase10Len]byte; return owned(a, strconv.FormatInt(buffer[:], value.Int, 10)), true }
	if value.Kind == core.Float { var buffer [strconv.MaxFloat64Len]byte; return owned(a, strconv.FormatFloat(buffer[:], value.Float, 'g', -1, 64)), true }
	if value.Kind == core.List {
		builder := strings.NewBuilder(a)
		for i := range value.List {
			if i != 0 { builder.WriteByte(' ') }
			text, ok := stringify(a, value.List[i])
			if !ok { builder.Free(); return "", false }
			builder.WriteString(text)
			mem.FreeString(a, text)
		}
		result := owned(a, builder.String())
		builder.Free()
		return result, true
	}
	return "", false
}

func freeValues(a mem.Allocator, values []core.Value) { for i := range values { values[i].Free(a) }; slices.Free(a, values) }
func freeRecord(a mem.Allocator, values []core.RecordField) { for i := range values { values[i].Value.Free(a) }; slices.Free(a, values) }
func owned(a mem.Allocator, text string) string { if len(text) == 0 { return "" }; b := mem.AllocSlice[byte](a, len(text), len(text)); copy(b, []byte(text)); return string(b) }

func (p *Program) Render(run mem.Allocator, value *template.String, scope *Scope, context *Context) Result {
	if context == nil { context = &Context{Program: p, Scope: scope, Run: run} }
	b := strings.NewBuilder(run)
	defer b.Free()
	for i := range value.Parts {
		part := value.Parts[i]
		if part.Kind == template.Literal { b.WriteString(part.Text); continue }
		var r Result
		if part.Kind == template.Selector { r = p.selector(part.Text, part.Span, context) } else { r = p.evaluate(context.Engine, scope, part.Expr, context) }
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		text, ok := stringify(run, r.Value); r.Value.Free(run)
		if !ok { return failure("EXPR_INVALID", part.Span, "records and bytes require explicit text conversion") }
		b.WriteString(text); mem.FreeString(run, text)
	}
	return Result{Value: core.NewString(run, b.String())}
}

func (p *Program) application(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Items) == 0 { return failure("EXPR_INVALID", expression.Span, "empty application") }
	head := expression.Items[0]
	if head.Kind == expr.Name {
		if head.Text == "?" { return p.fallback(scope, expression.Items[1:], context) }
		if head.Text == "let" { return p.let(scope, expression.Items[1:], context, expression.Span) }
		if head.Text == "def" { return p.def(scope, expression.Items[1:], context, expression.Span) }
		if head.Text == "eval" { return p.evalText(scope, expression.Items[1:], context, expression.Span) }
	}
	callee := p.evaluate(context.Engine, scope, head, context)
	if callee.Waiting || callee.Diagnostic.Code != "" {
		if callee.Diagnostic.Code == "REF_MISSING" && head.Kind == expr.Name && p.Registry != nil {
			operation := p.Registry.lookup(head.Text)
			if operation != nil {
				result := p.operation(scope, operation, expression.Items[1:], context, expression.Span)
				attachFrame(&result, context, expression.Span, "operation")
				return result
			}
			return failure("OP_UNKNOWN", head.Span, "unknown operation: "+head.Text)
		}
		return callee
	}
	if callee.Value.Kind != core.Callable { callee.Value.Free(context.Run); return failure("EXPR_INVALID", head.Span, "application head is not callable") }
	function := callee.Value.Callable.(*Function)
	result := p.call(function, expression.Items[1:], context, expression.Span)
	freeCallables(context.Run, &callee.Value)
	return result
}

func (p *Program) operation(scope *Scope, operation *Operation, arguments []*expr.Expr, context *Context, span source.Span) Result {
	_, _ = p, scope
	// A dependency may publish while a host operation is outstanding. Keep its
	// single request active until the engine supplies the matching completion;
	// the resumed evaluation then snapshots every dependency at its latest value.
	if context.Engine != nil && context.Engine.Submitted() && context.Completion().RequestID == 0 { return Result{Waiting: true} }
	if len(arguments) < operation.MinArity || (operation.MaxArity >= 0 && len(arguments) > operation.MaxArity) { return failure("EXPR_INVALID", span, "invalid operation arity") }
	for i := range operation.Capabilities { if !context.allowed(operation.Capabilities[i]) { return failure("CAP_DENIED", span, "operation capability denied") } }
	values := slices.Make[core.Value](context.Run, len(arguments))
	for i := range arguments {
		r := p.evaluate(context.Engine, context.Scope, arguments[i], context)
		if r.Waiting || r.Diagnostic.Code != "" { freeValues(context.Run, values); return r }
		values[i] = r.Value
	}
	previousCapabilities := context.activeCapabilities
	context.denied, context.activeCapabilities = false, operation.Capabilities
	result := operation.Call(context, operation.Context, values)
	context.activeCapabilities = previousCapabilities
	freeValues(context.Run, values)
	if context.denied { result.Free(context.Run); return failure("CAP_DENIED", span, "operation capability denied") }
	return result
}

func (p *Program) call(function *Function, arguments []*expr.Expr, context *Context, span source.Span) Result {
	values := slices.Make[core.Value](context.Run, len(arguments))
	defer freeValues(context.Run, values)
	for i := range arguments {
		r := p.evaluate(context.Engine, context.Scope, arguments[i], context)
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		values[i] = r.Value
	}
	return p.callValues(function, values, context, span)
}

func (p *Program) callValues(function *Function, values []core.Value, context *Context, span source.Span) Result {
	fixed := len(function.Parameters)
	rest := fixed != 0 && function.Parameters[fixed-1].Rest
	if rest { fixed-- }
	if len(values) < fixed || (!rest && len(values) != fixed) { return failure("EXPR_INVALID", span, "invalid function arity") }
	child := newScope(context.Run, function.Scope)
	defer child.Free()
	for i := 0; i < fixed; i++ { child.setValue(function.Parameters[i].Name, values[i]) }
	if rest {
		remainder := core.NewList(context.Run, values[fixed:])
		child.setValue(function.Parameters[fixed].Name, remainder)
		remainder.Free(context.Run)
	}
	previousArgs, previousScope := context.Args, context.Scope
	context.Args = values
	var result Result
	if function.Definition != nil { result = p.definitionValue(context.Engine, function.Definition, child, context)
	} else if function.Expression != nil { result = p.evaluate(context.Engine, child, function.Expression, context)
	} else { result = p.body(child, function.Body, context) }
	context.Args, context.Scope = previousArgs, previousScope
	attachFrame(&result, context, span, "call")
	return result
}

func attachFrame(result *Result, context *Context, span source.Span, label string) {
	if result.Diagnostic.Code == "" { return }
	a := context.Run
	if a == nil { a = mem.System }
	result.Diagnostic.Frames = slices.Append(a, result.Diagnostic.Frames, diagnostic.Frame{Label: label, Span: diagnostic.Span{Start: span.Start, End: span.End}})
}

func (p *Program) body(scope *Scope, body []*expr.Expr, context *Context) Result {
	result := Result{Value: core.Value{Kind: core.Nil}}
	for i := range body {
		result.Value.Free(context.Run)
		result = p.evaluate(context.Engine, scope, body[i], context)
		if result.Waiting || result.Diagnostic.Code != "" { return result }
	}
	return result
}

func (p *Program) fallback(scope *Scope, values []*expr.Expr, context *Context) Result {
	for i := range values {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "REF_MISSING" { return r }
		r.Diagnostic.Free(context.Run)
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

func (p *Program) let(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) == 0 || values[0].Kind != expr.List || len(values[0].Items)%2 != 0 { return failure("EXPR_INVALID", span, "let needs name/value pairs") }
	child := newScope(context.Run, scope)
	defer child.Free()
	for i := 0; i < len(values[0].Items); i += 2 {
		name := values[0].Items[i]
		if name.Kind != expr.Name { return failure("DEF_INVALID", name.Span, "let binding needs a name") }
		r := p.evaluate(context.Engine, child, values[0].Items[i+1], context)
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		child.setValue(name.Text, r.Value)
		r.Value.Free(context.Run)
	}
	return p.body(child, values[1:], context)
}

func (p *Program) def(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 2 || values[0].Kind != expr.Name { return failure("DEF_INVALID", span, "def needs a name and value") }
	if len(values) >= 3 && values[1].Kind == expr.List {
		parameters := slices.Make[expr.Parameter](context.Run, len(values[1].Items))
		for i := range values[1].Items {
			parameter := values[1].Items[i]
			if parameter.Kind != expr.Name || (parameter.Rest && i != len(values[1].Items)-1) { for j := 0; j < i; j++ { mem.FreeString(context.Run, parameters[j].Name) }; slices.Free(context.Run, parameters); return failure("DEF_INVALID", parameter.Span, "invalid function parameter") }
			for j := 0; j < i; j++ { if parameters[j].Name == parameter.Text { for j := 0; j < i; j++ { mem.FreeString(context.Run, parameters[j].Name) }; slices.Free(context.Run, parameters); return failure("DEF_INVALID", parameter.Span, "duplicate function parameter") } }
			parameters[i] = expr.Parameter{Name: owned(context.Run, parameter.Text), Span: parameter.Span, Rest: parameter.Rest}
		}
		function := mem.Alloc[Function](context.Run)
		for i := 2; i < len(values); i++ { function.Body = slices.Append(context.Run, function.Body, expr.Clone(context.Run, values[i])) }
		function.Parameters, function.Scope, function.Owned, function.ParametersOwned, function.ParameterNamesOwned, function.BodyOwned = parameters, scope, true, true, true, true
		scope.setFunction(values[0].Text, function)
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	if len(values) != 2 { return failure("DEF_INVALID", span, "invalid def form") }
	r := p.evaluate(context.Engine, scope, values[1], context)
	if r.Waiting || r.Diagnostic.Code != "" { return r }
	scope.setValue(values[0].Text, r.Value)
	return r
}

func (p *Program) evalText(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) != 1 { return failure("EXPR_INVALID", span, "eval needs one string") }
	r := p.evaluate(context.Engine, scope, values[0], context)
	if r.Waiting || r.Diagnostic.Code != "" { return r }
	if r.Value.Kind != core.String { r.Value.Free(context.Run); return failure("EXPR_INVALID", span, "eval needs text") }
	parsed := expr.Parse(context.Run, "<eval>", r.Value.Text)
	r.Value.Free(context.Run)
	if len(parsed.Diagnostics) != 0 {
		parseDiagnostic := parsed.Diagnostics[0]
		parsed.Free()
		return Result{Diagnostic: diagnostic.Diagnostic{Code: parseDiagnostic.Code, Severity: diagnosticSeverity(parseDiagnostic.Severity), Message: parseDiagnostic.Message, Span: diagnostic.Span{Start: parseDiagnostic.Span.Start, End: parseDiagnostic.Span.End}}}
	}
	result := p.evaluate(context.Engine, scope, parsed.Expr, context)
	parsed.Free()
	return result
}

func (p *Program) reference(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Reference) == 0 { return failure("REF_MISSING", expression.Span, "empty reference") }
	result := name(scope, expression.Reference[0].Text, expression.Reference[0].Span, context)
	if result.Waiting || result.Diagnostic.Code != "" { return result }
	for i := 1; i < len(expression.Reference); i++ {
		part := expression.Reference[i]
		next := p.referencePart(result.Value, part, context)
		result.Value.Free(context.Run)
		result = next
		if result.Diagnostic.Code != "" { return result }
	}
	return result
}

func (p *Program) referencePart(value core.Value, part expr.ReferencePart, context *Context) Result {
	_ = p
	if part.Kind == expr.ReferenceName {
		if value.Kind != core.Record { return failure("REF_MISSING", part.Span, "record field not found") }
		for i := range value.Record { if value.Record[i].Key == part.Text { return Result{Value: value.Record[i].Value.Clone(context.Run)} } }
		return failure("REF_MISSING", part.Span, "record field not found")
	}
	if part.Kind == expr.ReferenceSelection {
		if value.Kind != core.Record { return failure("REF_MISSING", part.Span, "selection needs a record") }
		var fields []core.RecordField
		start := 0
		for i := 0; i <= len(part.Text); i++ {
			if i != len(part.Text) && part.Text[i] != ',' { continue }
			key := part.Text[start:i]
			found := false
			for j := range value.Record { if value.Record[j].Key == key { fields = slices.Append(context.Run, fields, core.RecordField{Key: key, Value: value.Record[j].Value.Clone(context.Run)}); found = true; break } }
			if !found { freeRecord(context.Run, fields); return failure("REF_MISSING", part.Span, "record field not found") }
			start = i + 1
		}
		result := Result{Value: core.NewRecord(context.Run, fields)}; freeRecord(context.Run, fields); return result
	}
	if part.Kind == expr.ReferenceIndex {
		index, ok := parseIndex(part.Text)
		if !ok { return failure("SEL_INDEX_INVALID", part.Span, "invalid index") }
		if value.Kind == core.List {
			index = normalizedIndex(index, len(value.List))
			if index < 0 || index >= len(value.List) { return failure("SEL_INDEX_INVALID", part.Span, "index out of range") }
			return Result{Value: value.List[index].Clone(context.Run)}
		}
		if value.Kind == core.String {
			index = normalizedIndex(index, utf8.RuneCountInString(value.Text))
			start := runeOffset(value.Text, index)
			end := runeOffset(value.Text, index+1)
			if start < 0 || end < 0 { return failure("SEL_INDEX_INVALID", part.Span, "index out of range") }
			return Result{Value: core.NewString(context.Run, value.Text[start:end])}
		}
		return failure("REF_MISSING", part.Span, "index needs a list or string")
	}
	if part.Kind == expr.ReferenceSlice {
		bounds := parseSlice(part.Text)
		if !bounds.OK { return failure("SEL_INDEX_INVALID", part.Span, "invalid slice") }
		start, end := bounds.Start, bounds.End
		if value.Kind == core.List {
			bounds = normalizedSlice(start, end, len(value.List))
			if !bounds.OK { return failure("SEL_INDEX_INVALID", part.Span, "slice out of range") }
			start, end = bounds.Start, bounds.End
			return Result{Value: core.NewList(context.Run, value.List[start:end])}
		}
		if value.Kind == core.String {
			bounds = normalizedSlice(start, end, utf8.RuneCountInString(value.Text))
			if !bounds.OK { return failure("SEL_INDEX_INVALID", part.Span, "slice out of range") }
			start, end = bounds.Start, bounds.End
			return Result{Value: core.NewString(context.Run, value.Text[runeOffset(value.Text, start):runeOffset(value.Text, end)])}
		}
		return failure("REF_MISSING", part.Span, "slice needs a list or string")
	}
	return failure("REF_MISSING", part.Span, "invalid reference")
}

func parseIndex(text string) (int, bool) {
	if len(text) == 0 { return 0, false }
	negative, index := false, 0
	if text[0] == '-' { negative = true; text = text[1:] }
	if len(text) == 0 { return 0, false }
	for i := range text { if text[i] < '0' || text[i] > '9' { return 0, false }; index = index*10 + int(text[i]-'0') }
	if negative { index = -index }
	return index, true
}

func normalizedIndex(index int, length int) int { if index < 0 { return length + index }; return index }
func runeOffset(text string, index int) int {
	if index < 0 { return -1 }
	offset := 0
	for current := 0; current < index; current++ { if offset == len(text) { return -1 }; _, width := utf8.DecodeRuneInString(text[offset:]); offset += width }
	return offset
}

type sliceBounds struct { Start int; End int; OK bool }

func parseSlice(text string) sliceBounds {
	cut := -1
	for i := 0; i+1 < len(text); i++ { if text[i] == '.' && text[i+1] == '.' { cut = i; break } }
	if cut < 0 { return sliceBounds{} }
	start, end, ok := 0, -2147483648, true
	if cut != 0 { start, ok = parseIndex(text[:cut]) }
	if !ok { return sliceBounds{} }
	if cut+2 != len(text) { end, ok = parseIndex(text[cut+2:]) }
	return sliceBounds{Start: start, End: end, OK: ok}
}

func normalizedSlice(start int, end int, length int) sliceBounds {
	if start < 0 { start = length + start }
	if end == -2147483648 { end = length } else if end < 0 { end = length + end }
	if start < 0 || end < start || end > length { return sliceBounds{} }
	return sliceBounds{Start: start, End: end, OK: true}
}

func (p *Program) selector(text string, span source.Span, context *Context) Result {
	_ = p
	values := context.Args
	offset := 1
	if len(text) >= 2 && (text[1] == '<' || text[1] == '>') {
		if len(context.RuleFrames) != 0 {
			frame := context.RuleFrames[len(context.RuleFrames)-1]
			if text[1] == '<' { values = frame.Inputs } else { values = frame.Outputs }
		} else if text[1] == '<' { values = context.Inputs } else { values = context.Outputs }
		offset = 2
	}
	if values == nil { return failure("SEL_NO_CONTEXT", span, "selector has no context") }
	suffix := text[offset:]
	if suffix == "*" { return Result{Value: core.NewList(context.Run, values)} }
	if suffix == "#" { return Result{Value: core.Value{Kind: core.Int, Int: int64(len(values))}} }
	if suffix == "" || suffix == "_" {
		if len(values) == 0 { return failure("SEL_INDEX_INVALID", span, "selector index is empty") }
		return Result{Value: values[0].Clone(context.Run)}
	}
	if bounds := parseSlice(suffix); bounds.OK {
		bounds = normalizedSlice(bounds.Start, bounds.End, len(values))
		if !bounds.OK { return failure("SEL_INDEX_INVALID", span, "selector slice out of range") }
		return Result{Value: core.NewList(context.Run, values[bounds.Start:bounds.End])}
	}
	index, ok := parseIndex(suffix)
	if !ok { return failure("SEL_INDEX_INVALID", span, "invalid selector index") }
	index = normalizedIndex(index, len(values))
	if index < 0 || index >= len(values) { return failure("SEL_INDEX_INVALID", span, "selector index out of range") }
	return Result{Value: values[index].Clone(context.Run)}
}
