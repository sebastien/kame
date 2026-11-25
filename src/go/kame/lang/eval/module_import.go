package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/script"
	"kame/lang/source"
	"path"
	"strconv"
	"strings"
)

type authoredImport struct {
	program *Program
	node    *core.Node
	module  *script.ModuleDeclaration
	alias   string
	author  string
	names   map[string]script.ModuleName
}

type moduleSourceLoad struct {
	program *Program
	path    string
}

type authoredModuleLoad struct {
	program *Program
	path    string
}

func (p *Program) HasAuthoredSourceRequest(name string) bool {
	return p.sourceRequests[name]
}

// IsModuleLoad identifies compiler dependencies. Runtime reads of module
// definitions record their authored source files separately.
func IsModuleLoad(node *core.Node) bool {
	switch node.Context.(type) {
	case *authoredImport, *authoredModuleLoad, *moduleSourceLoad:
		return true
	}
	return false
}

func (p *Program) RegisterImports() diagnostic.Diagnostic {
	if d := p.registerImports(p.Script.Items, p.Scope); d.Code != "" {
		return d
	}
	for _, definition := range p.Nodes {
		p.AttachImports(definition.Node)
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) registerImports(items []script.ScriptItem, scope *Scope) diagnostic.Diagnostic {
	for _, item := range items {
		m := item.Module
		if m == nil {
			continue
		}
		c := &Context{Program: p, Run: p.Alloc, Source: p.Script.Source.Name, SourceOrigin: m.SourceOrigin}
		if item.Kind == script.ModuleExport {
			for _, name := range m.Names {
				if scope.lookup(name.Name) == nil {
					r := failure(p.Alloc, "DEF_INVALID", name.Span, "cannot export unknown definition: "+name.Name)
					attachSource(&r, c)
					return r.Diagnostic
				}
			}
			continue
		}
		imp := &authoredImport{program: p, module: m}
		if part := c.authoringSource(); part != nil {
			imp.author = path.Join(part.Directory, path.Base(part.Name))
		} else if !path.IsAbs(m.Path) && !strings.HasPrefix(m.Path, "~/") {
			r := failure(p.Alloc, "RES_INVALID", m.PathSpan, "relative import requires file-backed source")
			attachSource(&r, c)
			return r.Diagnostic
		}
		if item.Kind == script.ModuleImport {
			imp.alias = m.Namespace()
			if other, _ := p.findImport(m.SourceOrigin, imp.alias+"/"); other != nil {
				r := failure(p.Alloc, "DEF_INVALID", item.Span, "duplicate module namespace: "+imp.alias)
				attachSource(&r, c)
				return r.Diagnostic
			}
		} else {
			imp.names = make(map[string]script.ModuleName, len(m.Names))
			for _, member := range m.Names {
				name, span := member.Name, member.Span
				if member.Alias != "" {
					name, span = member.Alias, member.AliasSpan
				}
				other, _ := p.findImport(m.SourceOrigin, name)
				if _, duplicate := imp.names[name]; duplicate || other != nil || scope.lookup(name) != nil {
					r := failure(p.Alloc, "DEF_INVALID", span, "duplicate imported binding: "+name)
					attachSource(&r, c)
					return r.Diagnostic
				}
				imp.names[name] = member
			}
		}
		imp.node = p.Engine.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "<import:" + strconv.Itoa(len(p.authoredImports)) + ">"}, runImport, imp)
		imp.node.Restartable = true
		p.authoredImports = append(p.authoredImports, imp)
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) findImport(origin int, name string) (*authoredImport, string) {
	if origin > 0 && origin <= len(p.SourceParts) && p.SourceParts[origin-1].Module {
		return nil, ""
	}
	namespace, member, qualified := strings.Cut(name, "/")
	for _, imp := range p.authoredImports {
		if origin != imp.module.SourceOrigin {
			if origin <= 0 || origin > len(p.SourceParts) || imp.module.SourceOrigin <= 0 || imp.module.SourceOrigin > len(p.SourceParts) ||
				p.SourceParts[origin-1].Included != nil || p.SourceParts[imp.module.SourceOrigin-1].Included != nil ||
				p.SourceParts[origin-1].Name != p.SourceParts[imp.module.SourceOrigin-1].Name {
				continue
			}
		}
		if qualified {
			if imp.alias != "" && imp.alias == namespace {
				return imp, member
			}
		} else if selected, ok := imp.names[name]; ok {
			return imp, selected.Name
		}
	}
	return nil, ""
}

func (p *Program) AttachImports(node *core.Node) {
	for _, imp := range p.authoredImports {
		origin := imp.module.SourceOrigin
		if origin > 0 && origin <= len(p.SourceParts) && p.SourceParts[origin-1].Included != nil {
			continue
		}
		p.Engine.AddStatic(node, imp.node)
	}
}

func runImport(engine *core.EngineContext, _ int64) core.ProducerResult {
	imp := engine.Context().(*authoredImport)
	p := imp.program
	c := &Context{Program: p, Engine: engine, Run: engine.Allocator(), Requests: p.Requests,
		Scope: p.Scope, Source: p.Script.Source.Name, SourceOrigin: imp.module.SourceOrigin, Environment: p.ModuleEnvironment}
	c.Resume(engine)
	r := p.loadImport(imp.module, c, imp.author, nil)
	defer r.Free(c.Run)
	if r.Waiting {
		if engine.Submitted() {
			return core.ProducerSubmitted
		}
		return core.ProducerWaiting
	}
	if r.Diagnostic.Code != "" {
		attachSource(&r, c)
		engine.Fail(r.Diagnostic)
		r.Diagnostic = diagnostic.Diagnostic{}
		return core.ProducerFailed
	}
	engine.Publish(r.Value)
	r.Value = core.Value{}
	return core.ProducerCompleted
}

func (p *Program) loadImport(m *script.ModuleDeclaration, c *Context, author string, parent *core.Node) Result {
	span := m.PathSpan
	operand, base := m.Path, ""
	if strings.HasPrefix(operand, "~/") {
		home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
		if !path.IsAbs(home) {
			return failure(c.Run, "FS_ERR", span, "cannot resolve home directory for import")
		}
		base, operand = home, operand[2:]
	} else if !path.IsAbs(operand) {
		if !path.IsAbs(author) {
			return failure(c.Run, "RES_INVALID", span, "relative import requires file-backed source")
		}
		base = path.Dir(author)
	}
	resolved := path.Join(base, operand)
	paths := []string{resolved}
	switch path.Ext(resolved) {
	case ".km", ".kame", ".kash", ".ksh":
	default:
		paths = []string{resolved + ".km", resolved + ".kame", resolved + ".kash", resolved + ".ksh"}
	}
	var matches []string
	for _, candidate := range paths {
		result := p.moduleSource(c, candidate, false)
		if result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
		if result.Value.Kind == core.Bytes {
			matches = append(matches, candidate)
		}
		result.Free(c.Run)
	}
	if len(matches) == 0 {
		return failure(c.Run, "FS_ERR", span, "cannot find module; tried "+strings.Join(paths, ", "))
	}
	if len(matches) > 1 {
		return failure(c.Run, "RES_INVALID", span, "ambiguous module: "+strings.Join(matches, ", "))
	}
	name := matches[0]
	if name == author {
		return importCycle(c, span, []string{name, name})
	}
	moduleNode := p.authoredModuleNode(name)
	p.refreshDormantModuleNode(moduleNode)
	if parent != nil {
		if chain := p.importBackEdge(moduleNode, parent, name); len(chain) != 0 {
			if author != chain[len(chain)-1] {
				chain = append(chain, author)
			}
			return importCycle(c, span, append(chain, name))
		}
	}
	if !c.Engine.TryDependency(moduleNode.Key) {
		return Result{Waiting: true}
	}
	if d := c.Engine.DependencyDiagnostic(moduleNode.Key); d.Code != "" {
		d = d.Clone(c.Run)
		d.Frames = append(d.Frames, diagnostic.Frame{
			Kind: "import", Label: owned(c.Run, name), Source: owned(c.Run, author),
			Span: diagnostic.Span{Start: span.Start, End: span.End},
		})
		return Result{Diagnostic: d}
	}
	value := c.Engine.Value(moduleNode.Key).Value
	module := value.Module.Handle.(*kashModule)
	for _, selected := range m.Names {
		if !module.exports(selected.Name) {
			return failure(c.Run, "REF_MISSING", selected.Span, "module does not export "+selected.Name)
		}
	}
	return Result{Value: value.Clone(c.Run)}
}

func importCycle(c *Context, span source.Span, chain []string) Result {
	return failure(c.Run, "DEP_CYCLE", span, "module import cycle: "+strings.Join(chain, " -> "))
}

func (p *Program) importBackEdge(target, parent *core.Node, name string) []string {
	if chain := moduleImportChain(target, parent); len(chain) != 0 {
		return chain
	}
	for _, imp := range p.authoredImports {
		if imp.author != name || imp.node.Interest == 0 {
			continue
		}
		if chain := moduleImportDependencies(imp.node, parent); len(chain) != 0 {
			return append([]string{name}, chain...)
		}
	}
	return nil
}

// Module dependency edges are acyclic. Trace existing edges before adding a
// back edge so the diagnostic can name the sources in the cycle.
func moduleImportChain(from, to *core.Node) []string {
	load := from.Context.(*authoredModuleLoad)
	if from == to {
		return []string{load.path}
	}
	if chain := moduleImportDependencies(from, to); len(chain) != 0 {
		return append([]string{load.path}, chain...)
	}
	return nil
}

func moduleImportDependencies(from, to *core.Node) []string {
	for _, edge := range from.Dynamic {
		if _, ok := edge.Context.(*authoredModuleLoad); !ok {
			continue
		}
		if chain := moduleImportChain(edge, to); len(chain) != 0 {
			return chain
		}
	}
	return nil
}

func moduleSourceKey(name string) core.ResourceKey {
	return core.ResourceKey{Kind: core.ResourceDefinition, Name: "<module-source:" + name + ">"}
}

// RevalidateModuleSource refreshes the loader, including an in-flight read.
// Its reverse edges reach imports and definitions using the old source.
func (p *Program) RevalidateModuleSource(name string) {
	if p == nil || p.Engine == nil {
		return
	}
	name = path.Clean(name)
	if p.HasAuthoredSourceRequest(name) {
		p.Engine.Revalidate(p.Engine.Lookup(moduleSourceKey(name)))
	}
}

func (p *Program) moduleSource(c *Context, name string, loadBytes bool) Result {
	key := moduleSourceKey(name)
	node := p.Engine.Lookup(key)
	if node == nil {
		node = p.Engine.Add(key, runModuleSource, &moduleSourceLoad{program: p, path: name})
		node.Restartable = true
	}
	p.refreshDormantModuleNode(node)
	if !c.Engine.TryDependency(node.Key) {
		return Result{Waiting: true}
	}
	if d := c.Engine.DependencyDiagnostic(node.Key); d.Code != "" {
		return Result{Diagnostic: d.Clone(c.Run)}
	}
	value := c.Engine.Value(node.Key).Value
	if !loadBytes {
		return Result{Value: core.Value{Kind: value.Kind}}
	}
	return Result{Value: value.Clone(c.Run)}
}

func (p *Program) refreshDormantModuleNode(node *core.Node) {
	// Inactive includes are unwatched. Refresh their retained results when a
	// later declaration selects them again.
	if p.ModuleWatch && node.Interest == 0 && (node.Current || node.State == core.NodeFailed) {
		p.Engine.Invalidate(node)
	}
}

func runModuleSource(engine *core.EngineContext, _ int64) core.ProducerResult {
	load := engine.Context().(*moduleSourceLoad)
	c := &Context{Program: load.program, Engine: engine, Run: engine.Allocator(), Requests: load.program.Requests}
	c.Resume(engine)
	if c.Completion().RequestID == 0 {
		if c.submitSource(load.path) == 0 {
			engine.Fail(diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "source loading request was rejected"})
			return core.ProducerFailed
		}
		return core.ProducerSubmitted
	}
	completion := c.TakeCompletion()
	if completion.Diagnostic.Code != "" {
		completion.Value.Free(c.Run)
		c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: load.path}, core.ObservationContent, core.Signature{})
		engine.Fail(completion.Diagnostic)
		return core.ProducerFailed
	}
	signature := core.Signature{Mode: core.SignatureMissing}
	if completion.Value.Kind == core.Bytes {
		signature = core.ContentSignature(completion.Value.Bytes)
	} else if !completion.HasValue || completion.Value.Kind != core.Nil {
		completion.Value.Free(c.Run)
		engine.Fail(diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "source host returned an invalid completion"})
		return core.ProducerFailed
	}
	c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: load.path}, core.ObservationContent, signature)
	engine.PublishSigned(completion.Value, signature)
	return core.ProducerCompleted
}

func (p *Program) authoredModuleNode(name string) *core.Node {
	key := core.ResourceKey{Kind: core.ResourceDefinition, Name: "<module:" + name + ">"}
	if node := p.Engine.Lookup(key); node != nil {
		return node
	}
	node := p.Engine.Add(key, runAuthoredModule, &authoredModuleLoad{program: p, path: name})
	node.Restartable = true
	return node
}

func runAuthoredModule(engine *core.EngineContext, _ int64) core.ProducerResult {
	load := engine.Context().(*authoredModuleLoad)
	p := load.program
	c := &Context{Program: p, Engine: engine, Run: engine.Allocator(), Requests: p.Requests,
		Source: load.path, Environment: p.ModuleEnvironment}
	c.Resume(engine)
	r := p.loadModule(load.path, c, source.Span{})
	defer r.Free(c.Run)
	if r.Waiting {
		return core.ProducerWaiting
	}
	if r.Diagnostic.Code != "" {
		if r.Diagnostic.Source == "" {
			r.Diagnostic.Source = owned(c.Run, load.path)
		}
		engine.Fail(r.Diagnostic)
		r.Diagnostic = diagnostic.Diagnostic{}
		return core.ProducerFailed
	}
	engine.Publish(r.Value)
	r.Value = core.Value{}
	return core.ProducerCompleted
}

func (c *Context) importedValue(imp *authoredImport, member string, span source.Span) Result {
	if c.Engine == nil {
		if c.ResolveDeclarationImport == nil {
			return failure(c.Run, "PHASE_INVALID", span, "import lookup requires an execution engine")
		}
		r := c.ResolveDeclarationImport(c.ResolverState, imp.node)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		return c.importedModuleMember(imp.node.Latest.Module.Handle.(*kashModule), member, span)
	}
	if !c.Engine.TryDependency(imp.node.Key) {
		return Result{Waiting: true}
	}
	if d := c.Engine.DependencyDiagnostic(imp.node.Key); d.Code != "" {
		return Result{Diagnostic: d.Clone(c.Run)}
	}
	return c.importedModuleMember(imp.node.Latest.Module.Handle.(*kashModule), member, span)
}

type moduleBinding struct {
	module *kashModule
	member string
}

func (p *Program) moduleImport(origin int, name string) moduleBinding {
	if origin > 0 && origin <= len(p.SourceParts) {
		part := &p.SourceParts[origin-1]
		if part.moduleSource != nil {
			if namespace, member, qualified := strings.Cut(name, "/"); qualified {
				return moduleBinding{module: part.moduleSource.namespaces[namespace], member: member}
			}
			return part.moduleSource.imports[name]
		}
	}
	return moduleBinding{}
}

func (c *Context) importedModuleMember(module *kashModule, member string, span source.Span) Result {
	if selected := c.ModuleField(module.Value(), member, span); selected.Found {
		return selected.Result
	}
	return failure(c.Run, "REF_MISSING", span, "module does not export "+member)
}
