package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/script"
	"kame/lang/source"
	"path"
)

type moduleSource struct {
	Path      string
	Parsed    *script.Script
	Frames    []diagnostic.Frame
	Signature core.Signature
	Origin    int
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
	Index     int
	RequestID int64
	IncludeAt source.Span
}

type moduleLoad struct {
	Module      *kashModule
	Completed   *kashModule // Program owns this after a successful load.
	Stack       []moduleFrame
	Definitions []*definition.Definition
}

func freeModuleLoad(a mem.Allocator, value any) {
	state := value.(*moduleLoad)
	if state.Module != nil {
		state.Module.Free()
	}
	for i := range state.Stack {
		mem.FreeString(a, state.Stack[i].Path)
	}
	mem.FreeSlice(nil, state.Stack)
	mem.FreeSlice(nil, state.Definitions)
	mem.Free(a, state)
}

func restoreModuleOperation(c *Context, start int, end int) {
	c.operationStart, c.operationEnd = start, end
}

func (c *Context) clearFailedModuleLoad(state *moduleLoad) {
	// Failed loads must keep their containing files observable for watch recovery.
	for i := range state.Module.Sources {
		s := state.Module.Sources[i]
		c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: s.Path}, core.ObservationContent, s.Signature)
	}
	c.ClearOperationState()
}

func (p *Program) loadKashModule(name string, c *Context, span source.Span) Result {
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
		state.Module.Program, state.Module.Scope = p, newScope(p.Alloc, c.Scope)
		state.Stack = append(state.Stack, moduleFrame{Path: owned(p.Alloc, name)})
		c.SetOperationState(state, freeModuleLoad)
	}
	module := state.Module
	if state.Completed != nil {
		module = state.Completed
	}
	// Resumption recreates temporary caller scopes; retain the current lexical parent.
	if module.Scope.Parent != c.Scope {
		c.Scope.Retain()
		module.Scope.Parent.Free()
		module.Scope.Parent = c.Scope
	}
	if state.Completed != nil {
		return Result{Value: state.Completed.Value()}
	}
	for len(state.Stack) != 0 {
		frame := &state.Stack[len(state.Stack)-1]
		if frame.Origin == 0 {
			if frame.RequestID == 0 {
				frame.RequestID = c.submitSource(frame.Path)
				if frame.RequestID == 0 {
					c.clearFailedModuleLoad(state)
					return failure(c.Run, "FEATURE_UNSUP", span, "source-loading host is unavailable")
				}
				return Result{Waiting: true}
			}
			if c.Completion().RequestID != frame.RequestID {
				return Result{Waiting: true}
			}
			completion := c.TakeCompletion()
			if completion.Diagnostic.Code != "" {
				d := completion.Diagnostic.Clone(c.Run)
				c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: frame.Path}, core.ObservationContent, core.Signature{Mode: core.SignatureMissing})
				c.clearFailedModuleLoad(state)
				return Result{Diagnostic: d}
			}
			if completion.Value.Kind != core.Bytes {
				completion.Value.Free(c.Run)
				c.clearFailedModuleLoad(state)
				return failure(c.Run, "HOST_FAIL", span, "source host did not return bytes")
			}
			parsed := script.ParseKash(p.Alloc, frame.Path, string(completion.Value.Bytes))
			bytesSignature := core.ContentSignature(completion.Value.Bytes)
			completion.Value.Free(c.Run)
			script.ValidateKashLibrary(parsed)
			if len(parsed.Diagnostics) != 0 {
				d := parsed.Diagnostics[0]
				result := failure(c.Run, d.Code, d.Span, d.Message)
				result.Diagnostic.Source = owned(c.Run, frame.Path)
				for i := len(state.Stack) - 1; i > 0; i-- {
					parent := state.Stack[i-1]
					result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: owned(c.Run, "include"), Label: owned(c.Run, state.Stack[i].Path), Source: owned(c.Run, parent.Path), Span: diagnostic.Span{Start: state.Stack[i].IncludeAt.Start, End: state.Stack[i].IncludeAt.End}})
				}
				parsed.Free()
				c.Observe(core.ResourceKey{Kind: core.ResourceFile, Name: frame.Path}, core.ObservationContent, bytesSignature)
				c.clearFailedModuleLoad(state)
				return result
			}
			module := state.Module
			loaded := moduleSource{Path: owned(p.Alloc, frame.Path), Parsed: parsed, Signature: bytesSignature}
			for i := len(state.Stack) - 1; i > 0; i-- {
				entry := state.Stack[i]
				parent := state.Stack[i-1]
				loaded.Frames = append(loaded.Frames, diagnostic.Frame{Kind: "include", Label: owned(p.Alloc, entry.Path), Source: owned(p.Alloc, parent.Path), Span: diagnostic.Span{Start: entry.IncludeAt.Start, End: entry.IncludeAt.End}})
			}
			module.Sources = append(module.Sources, loaded)
			frame.Origin = len(module.Sources)
			continue
		}
		item := state.Module.Sources[frame.Origin-1].Parsed.Items
		if frame.Index == len(item) {
			mem.FreeString(p.Alloc, frame.Path)
			state.Stack = state.Stack[:len(state.Stack)-1]
			continue
		}
		current := item[frame.Index]
		frame.Index++
		if current.Kind == script.Definition && current.Definition != nil {
			state.Definitions = append(state.Definitions, current.Definition)
			if len(state.Stack) == 1 {
				state.Module.Fields = append(state.Module.Fields, owned(p.Alloc, current.Definition.Name))
			}
		}
		if current.Kind == script.Include {
			base := path.Dir(frame.Path)
			child := path.Join(base, current.Include)
			mem.FreeString(p.Alloc, base)
			for i := range state.Stack {
				if state.Stack[i].Path == child {
					mem.FreeString(p.Alloc, child)
					result := failure(c.Run, "DEP_CYCLE", current.Span, "Kash library include cycle")
					result.Diagnostic.Source = owned(c.Run, frame.Path)
					for j := range state.Module.Sources[frame.Origin-1].Frames {
						f := state.Module.Sources[frame.Origin-1].Frames[j]
						result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: owned(c.Run, f.Kind), Label: owned(c.Run, f.Label), Source: owned(c.Run, f.Source), Span: f.Span})
					}
					c.clearFailedModuleLoad(state)
					return result
				}
			}
			state.Stack = append(state.Stack, moduleFrame{Path: child, IncludeAt: current.Span})
		}
	}
	m := state.Module
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
			c.clearFailedModuleLoad(state)
			return result
		}
		if d.Function {
			f := mem.Alloc[Function](p.Alloc)
			f.Kind, f.Parameters, f.Expression, f.Scope, f.ParametersOwned, f.Definition = FunctionDefinition, convertParameters(p.Alloc, d.Parameters), d.Expression, m.Scope, true, d
			m.Scope.setFunction(d.Name, f)
		} else {
			local := mem.Alloc[localDefinition](p.Alloc)
			local.Expression, local.Scope = d.Expression, m.Scope
			m.Scope.appendBinding(binding{Kind: bindingLocalDefinition, Name: owned(p.Alloc, d.Name), Local: local})
		}
	}
	for i := range m.Sources {
		s := &m.Sources[i]
		p.AddSourcePart(s.Path, 0, len(s.Parsed.Source.Text), 0)
		parent := path.Dir(s.Path)
		p.SetSourcePartDirectory(parent)
		mem.FreeString(p.Alloc, parent)
		p.SourceParts[len(p.SourceParts)-1].Signature = s.Signature
		p.SourceParts[len(p.SourceParts)-1].Module = true
		p.SetSourcePartIncludes(s.Frames)
		s.Origin = len(p.SourceParts)
		SetScriptSourceOrigin(s.Parsed, 0, len(s.Parsed.Source.Text), s.Origin)
	}
	digest := core.NewDigest()
	digest.Text("kash-module-v1")
	// Helpers are observed only when reached; their bytes cannot define the
	// module's identity or an unused nested edit would rebuild its callers.
	if len(m.Sources) != 0 {
		digest.Text(m.Sources[0].Path)
		digest.Write(m.Sources[0].Signature.Digest[:])
	}
	if m.Scope.Parent == p.Scope {
		m.Signature.Mode = core.SignatureContent
		digest.Sum(m.Signature.Digest[:])
	}
	p.Modules = append(p.Modules, m)
	state.Module, state.Completed = nil, m
	return Result{Value: m.Value()}
}

func (m *kashModule) Value() core.Value {
	return core.Value{Kind: core.Module, Module: m, ModuleOwner: m.Program, ModuleSignature: &m.Signature}
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
	found := false
	for i := range m.Fields {
		if m.Fields[i] == key {
			found = true
			break
		}
	}
	if !found {
		return ModuleFieldResult{}
	}
	previous := c.Source
	c.Source = c.Program.Script.Source.Name
	r := name(m.Scope, key, span, c)
	c.Source = previous
	return ModuleFieldResult{Result: r, Found: true}
}

// RevalidateModuleSource reloads retained module definitions when watch reports
// a changed nested library. A recipe observation alone would re-run old ASTs.
func (p *Program) RevalidateModuleSource(name string) {
	if p == nil || p.Engine == nil {
		return
	}
	for i := range p.Nodes {
		n := p.Nodes[i].Node
		if n.Latest.Kind != core.Module || n.Latest.ModuleOwner != p {
			continue
		}
		module := n.Latest.Module.(*kashModule)
		for j := range module.Sources {
			if module.Sources[j].Path == name {
				p.Engine.Revalidate(n)
				break
			}
		}
	}
}
