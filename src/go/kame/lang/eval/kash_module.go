package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"path"
	"strings"
)

type moduleSource struct {
	Path       string
	Parsed     *script.Script
	Frames     []diagnostic.Frame
	Signature  core.Signature
	Origin     int
	namespaces map[string]*kashModule
	imports    map[string]moduleBinding
}

type kashModule struct {
	Program   *Program
	Scope     *Scope
	Sources   []moduleSource
	Fields    []string
	Signature core.Signature
}

func (m *kashModule) Free() {
	if m == nil {
		return
	}
	m.Scope.Free()
	for i := range m.Sources {
		s := &m.Sources[i]
		s.Parsed.Free()
		mem.FreeString(m.Program.Alloc, s.Path)
		for j := range s.Frames {
			mem.FreeString(m.Program.Alloc, s.Frames[j].Label)
			mem.FreeString(m.Program.Alloc, s.Frames[j].Source)
		}
		mem.FreeSlice(nil, s.Frames)
	}
	mem.FreeSlice(nil, m.Sources)
	for i := range m.Fields {
		mem.FreeString(m.Program.Alloc, m.Fields[i])
	}
	mem.FreeSlice(nil, m.Fields)
	mem.Free(m.Program.Alloc, m)
}

type moduleFrame struct {
	Path      string
	Origin    int
	Blocks    []moduleBlock
	IncludeAt source.Span
	Optional  bool
}

type moduleBlock struct {
	Items []script.ScriptItem
	Next  int
}

type moduleLoad struct {
	Module       *kashModule
	Completed    *kashModule // Program owns this after a successful load.
	Stack        []moduleFrame
	Definitions  []*definition.Definition
	Exports      []moduleExport
	ImportSource int
	ImportItem   int
	Dependencies []core.Signature
}

type moduleExport struct {
	Source string
	Name   script.ModuleName
}

func freeModuleLoad(a mem.Allocator, value any) {
	state := value.(*moduleLoad)
	if state.Module != nil {
		state.Module.Free()
	}
	for i := range state.Stack {
		mem.FreeString(a, state.Stack[i].Path)
	}
	for _, export := range state.Exports {
		mem.FreeString(a, export.Source)
	}
	mem.FreeSlice(nil, state.Stack)
	mem.FreeSlice(nil, state.Definitions)
	mem.Free(a, state)
}

func restoreModuleOperation(c *Context, start int, end int) {
	c.operationStart, c.operationEnd = start, end
}

func (p *Program) loadModule(name string, c *Context, span source.Span) Result {
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = span.Start, span.End
	defer restoreModuleOperation(c, start, end)
	if c.Engine == nil || c.Requests == nil {
		return failure(c.Run, "FEATURE_UNSUP", span, "Kash module loading requires a source-loading host")
	}
	var state *moduleLoad
	if stored := c.OperationState(); stored != nil {
		state = stored.(*moduleLoad)
	}
	if state == nil {
		state = mem.Alloc[moduleLoad](p.Alloc)
		state.Module = mem.Alloc[kashModule](p.Alloc)
		state.Module.Program, state.Module.Scope = p, newScope(p.Alloc, nil)
		state.Stack = append(state.Stack, moduleFrame{Path: owned(p.Alloc, name)})
		c.SetOperationState(state, freeModuleLoad)
	}
	if state.Completed != nil {
		return Result{Value: state.Completed.Value()}
	}
	for len(state.Stack) != 0 {
		frame := &state.Stack[len(state.Stack)-1]
		if frame.Origin == 0 {
			loadedSource := p.moduleSource(c, frame.Path, true)
			if loadedSource.Waiting {
				return loadedSource
			}
			if loadedSource.Diagnostic.Code != "" {
				loadedSource.Value.Free(c.Run)
				c.ClearOperationState()
				return Result{Diagnostic: loadedSource.Diagnostic}
			}
			if loadedSource.Value.Kind == core.Nil {
				if frame.Optional {
					mem.FreeString(p.Alloc, frame.Path)
					state.Stack = state.Stack[:len(state.Stack)-1]
					continue
				}
				result := failure(c.Run, "FS_ERR", frame.IncludeAt, "cannot read source: "+frame.Path)
				if len(state.Stack) > 1 {
					result.Diagnostic.Source = owned(c.Run, state.Stack[len(state.Stack)-2].Path)
				}
				c.ClearOperationState()
				return result
			}
			if loadedSource.Value.Kind != core.Bytes {
				loadedSource.Value.Free(c.Run)
				c.ClearOperationState()
				return failure(c.Run, "HOST_FAIL", span, "source host did not return bytes")
			}
			var parsed *script.Script
			if script.IsKashSource(frame.Path) {
				parsed = script.ParseKash(p.Alloc, frame.Path, string(loadedSource.Value.Bytes))
				script.ValidateKashModule(parsed)
			} else {
				parsed = script.ParseKameFile(p.Alloc, frame.Path, string(loadedSource.Value.Bytes))
			}
			bytesSignature := core.ContentSignature(loadedSource.Value.Bytes)
			loadedSource.Value.Free(c.Run)
			if len(parsed.Diagnostics) != 0 {
				d := parsed.Diagnostics[0]
				result := failure(c.Run, d.Code, d.Span, d.Message)
				result.Diagnostic.Source = owned(c.Run, frame.Path)
				for i := len(state.Stack) - 1; i > 0; i-- {
					parent := state.Stack[i-1]
					result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: owned(c.Run, "include"), Label: owned(c.Run, state.Stack[i].Path), Source: owned(c.Run, parent.Path), Span: diagnostic.Span{Start: state.Stack[i].IncludeAt.Start, End: state.Stack[i].IncludeAt.End}})
				}
				parsed.Free()
				c.ClearOperationState()
				return result
			}
			module := state.Module
			loaded := moduleSource{Path: owned(p.Alloc, frame.Path), Parsed: parsed, Signature: bytesSignature}
			for i := len(state.Stack) - 1; i > 0; i-- {
				entry := state.Stack[i]
				parent := state.Stack[i-1]
				loaded.Frames = append(loaded.Frames, diagnostic.Frame{Kind: "include", Label: owned(p.Alloc, entry.Path), Source: owned(p.Alloc, parent.Path), Span: diagnostic.Span{Start: entry.IncludeAt.Start, End: entry.IncludeAt.End}})
			}
			p.addModuleSourcePart(&loaded)
			module.Sources = append(module.Sources, loaded)
			frame.Origin = len(module.Sources)
			frame.Blocks = append(frame.Blocks, moduleBlock{Items: parsed.Items})
			continue
		}
		if len(frame.Blocks) == 0 {
			mem.FreeString(p.Alloc, frame.Path)
			state.Stack = state.Stack[:len(state.Stack)-1]
			continue
		}
		block := &frame.Blocks[len(frame.Blocks)-1]
		if block.Next == len(block.Items) {
			frame.Blocks = frame.Blocks[:len(frame.Blocks)-1]
			continue
		}
		current := block.Items[block.Next]
		block.Next++
		if current.Kind == script.Composition {
			items, r := state.selectModuleBranch(p, c, current.Branches)
			if r.Diagnostic.Code != "" {
				c.ClearOperationState()
				return r
			}
			if len(items) != 0 {
				frame.Blocks = append(frame.Blocks, moduleBlock{Items: items})
			}
			continue
		}
		if current.Kind == script.Definition && current.Definition != nil {
			state.Definitions = append(state.Definitions, current.Definition)
		}
		if current.Module != nil {
			if current.Kind == script.ModuleExport {
				for _, export := range current.Module.Names {
					state.Exports = append(state.Exports, moduleExport{Source: owned(p.Alloc, frame.Path), Name: export})
				}
			}
			continue
		}
		if current.Kind == script.Include {
			if current.Expression != nil {
				result := failure(c.Run, "FEATURE_UNSUP", current.Span, "computed includes are only supported during declaration registration")
				result.Diagnostic.Source = owned(c.Run, frame.Path)
				c.ClearOperationState()
				return result
			}
			if script.IsKashSource(frame.Path) && !script.IsKashSource(current.Include) {
				result := failure(c.Run, "RES_INVALID", current.Span, "Kash includes require a .kash or .ksh source")
				result.Diagnostic.Source = owned(c.Run, frame.Path)
				c.ClearOperationState()
				return result
			}
			base := ""
			var sourceDirectory string
			if strings.HasPrefix(current.Include, "~/") {
				home := ""
				for _, entry := range c.Environment {
					if value, ok := strings.CutPrefix(entry, "HOME="); ok {
						home = value
					}
				}
				if !path.IsAbs(home) {
					result := failure(c.Run, "FS_ERR", current.Span, "cannot resolve the home directory for include")
					result.Diagnostic.Source = owned(c.Run, frame.Path)
					c.ClearOperationState()
					return result
				}
				base, current.Include = home, current.Include[2:]
			} else if !path.IsAbs(current.Include) {
				sourceDirectory = path.Dir(frame.Path)
				base = sourceDirectory
			}
			child := canonicalPath(p.Alloc, base, current.Include)
			mem.FreeString(p.Alloc, sourceDirectory)
			for i := range state.Stack {
				if state.Stack[i].Path == child {
					var sources []string
					for _, entry := range state.Stack[i:] {
						sources = append(sources, entry.Path)
					}
					cycle := strings.Join(append(sources, child), " -> ")
					mem.FreeString(p.Alloc, child)
					result := failure(c.Run, "DEP_CYCLE", current.Span, "include cycle: "+cycle)
					result.Diagnostic.Source = owned(c.Run, frame.Path)
					for j := range state.Module.Sources[frame.Origin-1].Frames {
						f := state.Module.Sources[frame.Origin-1].Frames[j]
						result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: owned(c.Run, f.Kind), Label: owned(c.Run, f.Label), Source: owned(c.Run, f.Source), Span: f.Span})
					}
					c.ClearOperationState()
					return result
				}
			}
			state.Stack = append(state.Stack, moduleFrame{Path: child, IncludeAt: current.Span, Optional: current.OptionalInclude})
		}
	}
	m := state.Module
	defined := make(map[string]bool, len(state.Definitions))
	for _, definition := range state.Definitions {
		defined[definition.Name] = true
	}
	r := state.bindImports(p, c, defined, p.authoredModuleNode(name))
	if r.Waiting {
		return r
	}
	if r.Diagnostic.Code != "" {
		c.ClearOperationState()
		return r
	}
	exported := make(map[string]bool, len(state.Exports))
	for _, export := range state.Exports {
		name := export.Name.Name
		if !defined[name] {
			result := failure(c.Run, "DEF_INVALID", export.Name.Span, "cannot export unknown definition: "+name)
			result.Diagnostic.Source = owned(c.Run, export.Source)
			c.ClearOperationState()
			return result
		}
		if !exported[name] {
			exported[name] = true
			m.Fields = append(m.Fields, owned(p.Alloc, name))
		}
	}
	digest := core.NewDigest()
	digest.Text("kash-module-v1")
	// Watch reloads use the fingerprint to replace linked scopes.
	if len(m.Sources) != 0 {
		digest.Text(m.Sources[0].Path)
		digest.Write(m.Sources[0].Signature.Digest[:])
	}
	digest.Text("authored")
	digest.Uint64(uint64(len(m.Sources) - 1))
	for _, included := range m.Sources[1:] {
		digest.Text(included.Path)
		digest.Write(included.Signature.Digest[:])
	}
	digest.Uint64(uint64(len(state.Dependencies)))
	for _, linked := range state.Dependencies {
		digest.Write(linked.Digest[:])
	}
	m.Signature.Mode = core.SignatureContent
	digest.Sum(m.Signature.Digest[:])
	for _, loaded := range p.Modules {
		if loaded.Signature.Equal(m.Signature) {
			m.Free()
			state.Module, state.Completed = nil, loaded
			return Result{Value: loaded.Value()}
		}
	}
	for i := range state.Definitions {
		d := state.Definitions[i]
		duplicate := false
		for j := range m.Scope.Bindings {
			if m.Scope.Bindings[j].Name == d.Name {
				duplicate = true
				break
			}
		}
		if duplicate {
			result := failure(c.Run, "DEF_INVALID", d.NameSpan, "duplicate definition: "+d.Name)
			for j := range m.Sources {
				s := &m.Sources[j]
				for k := range s.Parsed.Items {
					if s.Parsed.Items[k].Definition != d {
						continue
					}
					result.Diagnostic.Source = owned(c.Run, s.Path)
					for l := range s.Frames {
						f := s.Frames[l]
						result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: owned(c.Run, f.Kind), Label: owned(c.Run, f.Label), Source: owned(c.Run, f.Source), Span: f.Span})
					}
					break
				}
				if result.Diagnostic.Source != "" {
					break
				}
			}
			c.ClearOperationState()
			return result
		}
		addModuleDefinition(m.Scope, d)
	}
	for i := range m.Sources {
		s := &m.Sources[i]
		p.SourceParts[s.Origin-1].moduleSource = s
		for _, item := range s.Parsed.Items {
			if d := item.Definition; d != nil && d.Function {
				function := m.Scope.lookup(d.Name).Function
				digest := core.NewDigest()
				digest.Text("module-function-v1")
				digest.Text(s.Path)
				digest.Write(s.Signature.Digest[:])
				digest.Text(d.Name)
				function.Owner = p
				function.Signature.Mode = core.SignatureContent
				digest.Sum(function.Signature.Digest[:])
			}
		}
	}
	p.Modules = append(p.Modules, m)
	state.Module, state.Completed = nil, m
	return Result{Value: m.Value()}
}

func (p *Program) addModuleSourcePart(s *moduleSource) {
	p.AddSourcePart(s.Path, 0, len(s.Parsed.Source.Text), 0)
	parent := path.Dir(s.Path)
	p.SetSourcePartDirectory(parent)
	mem.FreeString(p.Alloc, parent)
	part := &p.SourceParts[len(p.SourceParts)-1]
	part.Signature, part.Module = s.Signature, true
	p.SetSourcePartIncludes(s.Frames)
	s.Origin = len(p.SourceParts)
	SetScriptSourceOrigin(s.Parsed, 0, len(s.Parsed.Source.Text), s.Origin)
}

func addModuleDefinition(scope *Scope, d *definition.Definition) {
	a := scope.Alloc
	if d.Function {
		f := mem.Alloc[Function](a)
		f.Kind, f.Parameters, f.Expression, f.Scope, f.ParametersOwned, f.Definition = FunctionDefinition, convertParameters(a, d.Parameters), d.Expression, scope, true, d
		scope.setFunction(d.Name, f)
		return
	}
	scope.setLocalDefinition(d.Name, d.Expression)
}

func (state *moduleLoad) selectModuleBranch(p *Program, c *Context, branches []script.DeclarationBranch) ([]script.ScriptItem, Result) {
	if !moduleBranchesIncludeSource(branches) {
		return nil, Result{}
	}
	scope := newScope(c.Run, nil)
	defer scope.Free()
	for _, d := range state.Definitions {
		// Report duplicate definitions during module registration, even when
		// the selector never reads a conflicting name.
		if scope.lookup(d.Name) == nil {
			addModuleDefinition(scope, d)
		}
	}
	for _, branch := range branches {
		if branch.Condition == nil {
			return branch.Items, Result{}
		}
		selected, r := p.moduleCondition(c, scope, branch.Condition)
		if r.Diagnostic.Code != "" {
			return nil, r
		}
		if selected {
			return branch.Items, Result{}
		}
	}
	return nil, Result{}
}

func moduleBranchesIncludeSource(branches []script.DeclarationBranch) bool {
	for _, branch := range branches {
		for _, item := range branch.Items {
			if item.Kind == script.Include || item.Kind == script.Composition && moduleBranchesIncludeSource(item.Branches) {
				return true
			}
		}
	}
	return false
}

func (p *Program) moduleCondition(c *Context, scope *Scope, condition *expr.Expr) (bool, Result) {
	// Configuration predicates use the invocation directory and have no host grants.
	context := &Context{Program: p, Run: c.Run, Cwd: p.DefinitionCwd,
		Source: p.Script.Source.Name, Scope: scope, Phase: PlanningPhase, pureValueBlock: true}
	r := p.EvaluateWith(condition, context)
	if r.Diagnostic.Code != "" {
		return false, r
	}
	defer r.Free(c.Run)
	if r.Waiting || r.Stream != nil || context.PhaseInvalid() {
		failed := failure(c.Run, "PHASE_INVALID", condition.Span, "configuration conditions cannot execute runtime work")
		context.SourceOrigin = condition.SourceOrigin
		attachSource(&failed, context)
		return false, failed
	}
	return truthValue(r.Value), Result{}
}

func (state *moduleLoad) bindImports(p *Program, c *Context, defined map[string]bool, parent *core.Node) Result {
	for state.ImportSource < len(state.Module.Sources) {
		src := &state.Module.Sources[state.ImportSource]
		for state.ImportItem < len(src.Parsed.Items) {
			item := src.Parsed.Items[state.ImportItem]
			if item.Kind != script.ModuleImport && item.Kind != script.ModuleFrom {
				state.ImportItem++
				continue
			}
			r := p.loadImport(item.Module, c, src.Path, parent)
			if r.Waiting {
				return r
			}
			if r.Diagnostic.Code == "" {
				module := r.Value.Module.(*kashModule)
				r = src.bindImport(c, item, module, defined)
				if r.Diagnostic.Code == "" {
					state.Dependencies = append(state.Dependencies, module.Signature)
				}
			}
			if r.Diagnostic.Code != "" {
				if r.Diagnostic.Source == "" {
					r.Diagnostic.Source = owned(c.Run, src.Path)
				}
				for _, frame := range src.Frames {
					r.Diagnostic.Frames = append(r.Diagnostic.Frames, diagnostic.Frame{
						Kind: frame.Kind, Label: owned(c.Run, frame.Label), Source: owned(c.Run, frame.Source), Span: frame.Span,
					})
				}
				return r
			}
			state.ImportItem++
		}
		state.ImportSource++
		state.ImportItem = 0
	}
	return Result{}
}

func (src *moduleSource) bindImport(c *Context, item script.ScriptItem, module *kashModule, defined map[string]bool) Result {
	if item.Kind == script.ModuleImport {
		alias := item.Module.Namespace()
		if src.namespaces[alias] != nil {
			return failure(c.Run, "DEF_INVALID", item.Span, "duplicate module namespace: "+alias)
		}
		if src.namespaces == nil {
			src.namespaces = make(map[string]*kashModule)
		}
		src.namespaces[alias] = module
		return Result{}
	}
	if src.imports == nil {
		src.imports = make(map[string]moduleBinding)
	}
	for _, selected := range item.Module.Names {
		name, span := selected.Name, selected.Span
		if selected.Alias != "" {
			name, span = selected.Alias, selected.AliasSpan
		}
		if defined[name] || src.imports[name].module != nil {
			return failure(c.Run, "DEF_INVALID", span, "duplicate imported binding: "+name)
		}
		src.imports[name] = moduleBinding{module: module, member: selected.Name}
	}
	return Result{}
}

func (m *kashModule) Value() core.Value {
	return core.Value{Kind: core.Module, Module: m, ModuleOwner: m.Program, ModuleSignature: &m.Signature}
}

func (m *kashModule) exports(name string) bool {
	for _, field := range m.Fields {
		if field == name {
			return true
		}
	}
	return false
}

// ModuleField is shared by dotted references and get. Missing fields are
// reported by the caller so get can retain its existing optional default.
type ModuleFieldResult struct {
	Result Result
	Found  bool
}

func (c *Context) ModuleField(value core.Value, key string, span source.Span) ModuleFieldResult {
	if c.Program == nil || value.Kind != core.Module || value.ModuleOwner != c.Program || value.Module == nil {
		return ModuleFieldResult{Result: failure(c.Run, "EXPR_INVALID", span, "module belongs to another invocation"), Found: true}
	}
	m := value.Module.(*kashModule)
	if !m.exports(key) {
		return ModuleFieldResult{}
	}
	previous := c.Source
	c.Source = c.Program.Script.Source.Name
	r := name(m.Scope, key, nil, span, c)
	c.Source = previous
	return ModuleFieldResult{Result: r, Found: true}
}

// UsesChangedInputs checks authored source, including helpers reached through modules.
func (p *Program) UsesChangedInputs() bool {
	uses := func(text string) bool {
		return strings.Contains(text, "changed-inputs")
	}
	if uses(p.Script.Source.Text) {
		return true
	}
	for _, part := range p.SourceParts {
		if part.Included != nil && uses(part.Included.Text) {
			return true
		}
	}
	for _, module := range p.Modules {
		for _, src := range module.Sources {
			if uses(src.Parsed.Source.Text) {
				return true
			}
		}
	}
	return false
}
