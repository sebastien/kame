package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"path"
)

// SourcePart maps one include-expanded segment back to its authored file.
type SourcePart struct {
	Name         string
	Start        int
	End          int
	Offset       int
	Directory    string
	Includes     []diagnostic.Frame
	Signature    core.Signature // Original bytes of a file-backed source.
	Module       bool           // Module spans are outside the combined source.
	Included     *source.Source // Independently parsed declaration include.
	moduleSource *moduleSource
	// observedCwd and observedName memoize the canonical file key recorded by
	// observeSourcePart, which runs on every call of a source-defined function.
	observedCwd  string
	observedName string
}

type Location struct {
	Source string
	Span   source.Span
}

// IndependentSource returns the file backing spans outside the combined script.
func (p *Program) IndependentSource(origin int) *source.Source {
	if origin <= 0 || origin > len(p.SourceParts) {
		return nil
	}
	part := &p.SourceParts[origin-1]
	if part.moduleSource != nil {
		return part.moduleSource.Parsed.Source
	}
	return part.Included
}

func (c *Context) observeDefinitionSource(e *definition.Definition) {
	if e == nil {
		return
	}
	c.observeExpressionSource(e.Expression)
}

func (c *Context) observeExpressionSource(e *expr.Expr) {
	// A separately parsed recipe is already signed by its authored body.
	if e == nil || e.EntrySource != nil || c.Program == nil {
		return
	}
	c.observeSourcePart(e.SourceOrigin)
}

func (c *Context) observeSourcePart(i int) {
	if c.Program == nil {
		return
	}
	if i <= 0 || i > len(c.Program.SourceParts) {
		return
	}
	part := &c.Program.SourceParts[i-1]
	if part.Signature.Mode != core.SignatureContent {
		return
	}
	// Rendered values already fingerprint their implementation and consumed data.
	if !part.Module && c.Phase != EvaluatePhase {
		return
	}
	if part.observedName == "" || part.observedCwd != c.Cwd {
		name := part.Name
		if part.Directory != "" {
			name = path.Join(part.Directory, path.Base(part.Name))
		}
		mem.FreeString(c.Program.Alloc, part.observedCwd)
		mem.FreeString(c.Program.Alloc, part.observedName)
		part.observedCwd, part.observedName = owned(c.Program.Alloc, c.Cwd), canonicalPath(c.Program.Alloc, c.Cwd, name)
	}
	if c.Engine != nil {
		c.Engine.ObserveAspect(core.ResourceKey{Kind: core.ResourceFile, Name: part.observedName}, core.ObservationContent, part.Signature)
	}
}

func (p *Program) AddSourcePart(name string, start int, end int, offset int) {
	p.SourceParts = append(p.SourceParts, SourcePart{Name: owned(p.Alloc, name), Start: start, End: end, Offset: offset})
}

func (p *Program) SetSourcePartIncludes(includes []diagnostic.Frame) {
	if len(p.SourceParts) == 0 {
		return
	}
	part := &p.SourceParts[len(p.SourceParts)-1]
	for i := range includes {
		frame := includes[i]
		part.Includes = append(part.Includes, diagnostic.Frame{Kind: owned(p.Alloc, frame.Kind), Label: owned(p.Alloc, frame.Label), Source: owned(p.Alloc, frame.Source), Span: frame.Span})
	}
}

// SetSourcePartDirectory attaches trusted loading metadata to the newest part.
// Keeping this separate preserves source-label-only embedding APIs.
func (p *Program) SetSourcePartDirectory(directory string) {
	if len(p.SourceParts) != 0 && path.IsAbs(directory) {
		part := &p.SourceParts[len(p.SourceParts)-1]
		mem.FreeString(p.Alloc, part.Directory)
		part.Directory = path.Clean(directory)
	}
}

// SetScriptSourceOrigin records a compiler-assigned origin on authored ASTs.
// Cloning retains it; parsing generated text deliberately starts without it.
func SetScriptSourceOrigin(parsed *script.Script, start int, end int, origin int) {
	for i := range parsed.Items {
		item := &parsed.Items[i]
		if item.Span.Start < start || item.Span.End > end {
			continue
		}
		setItemOrigin(item, origin)
	}
}

func setItemOrigin(item *script.ScriptItem, origin int) {
	item.SourceOrigin = origin
	if item.Module != nil {
		item.Module.SourceOrigin = origin
	}
	setExpressionOrigin(item.Expression, origin, nil)
	if item.Definition != nil {
		setExpressionOrigin(item.Definition.Expression, origin, nil)
	}
	setRuleOrigin(item.Rule, origin)
	for _, branch := range item.Branches {
		setExpressionOrigin(branch.Condition, origin, nil)
		for i := range branch.Items {
			setItemOrigin(&branch.Items[i], origin)
		}
	}
	if item.Loop != nil {
		for i := range item.Loop.Items {
			setItemOrigin(&item.Loop.Items[i], origin)
		}
	}
}

func setRuleOrigin(r *rule.Rule, origin int) {
	if r != nil {
		r.SourceOrigin = origin
		setExpressionOrigin(r.Metadata, origin, nil)
		setExpressionOrigin(r.BodyExpression, origin, nil)
		for i := range r.Outputs {
			r.Outputs[i].SourceOrigin = origin
			setTemplateSourceOrigin(r.Outputs[i].Expansion, origin)
		}
		for _, input := range r.Inputs {
			setTemplateSourceOrigin(input.Template, origin)
		}
		if r.BodyDoc != nil {
			setExpressionOrigin(r.BodyDoc.Root, origin, nil)
		}
		for _, line := range r.Body {
			setTemplateSourceOrigin(line.Template, origin)
		}
	}
}

// SetExpressionSourceOrigin gives a separately parsed expression the lexical
// scope of an authored source while retaining its own diagnostic spans.
func SetExpressionSourceOrigin(e *expr.Expr, origin int, entry *source.Source) {
	setExpressionOrigin(e, origin, entry)
}

func setTemplateSourceOrigin(value *template.String, origin int) {
	if value != nil {
		for i := range value.Parts {
			setExpressionOrigin(value.Parts[i].Expr, origin, nil)
		}
	}
}

// Interactive ASTs share immutable provenance without pinning completed entries
// in SourceParts. The view survives the parser and is collected with its users.
func SetInteractiveSource(parsed *script.Script) {
	entry := &source.Source{Name: parsed.Source.Name, Text: parsed.Source.Text}
	for _, item := range parsed.Items {
		setExpressionOrigin(item.Expression, 0, entry)
		if item.Definition != nil {
			setExpressionOrigin(item.Definition.Expression, 0, entry)
		}
	}
}

func setExpressionOrigin(e *expr.Expr, origin int, entry *source.Source) {
	if e == nil {
		return
	}
	e.SourceOrigin = origin
	e.EntrySource = entry
	if e.RuleSyntax != nil {
		setRuleOrigin(e.RuleSyntax.Declaration.(*rule.Rule), origin)
	}
	for i := range e.Items {
		setExpressionOrigin(e.Items[i], origin, entry)
	}
	for i := range e.Body {
		setExpressionOrigin(e.Body[i], origin, entry)
	}
	for i := range e.Fields {
		setExpressionOrigin(e.Fields[i].Value, origin, entry)
	}
	for i := range e.Parts {
		setExpressionOrigin(e.Parts[i].Expr, origin, entry)
	}
	setExpressionOrigin(e.CapturePolicy, origin, entry)
}

func (c *Context) SourceDirectory() Result {
	if part := c.authoringSource(); part != nil {
		return Result{Value: core.NewString(c.Run, part.Directory)}
	}
	return failure(c.Run, "SEL_NO_CONTEXT", c.Span, "source-dir requires file-backed source context")
}

func (c *Context) modulePath(member string, span source.Span) Result {
	if member != "dir" && member != "file" {
		return failure(c.Run, "REF_MISSING", span, "unknown module member: "+member)
	}
	if part := c.authoringSource(); part != nil {
		if member == "dir" {
			return Result{Value: core.NewPath(c.Run, part.Directory)}
		}
		return Result{Value: core.NewPath(c.Run, path.Join(part.Directory, path.Base(part.Name)))}
	}
	return failure(c.Run, "SEL_NO_CONTEXT", span, "module path requires file-backed source context")
}

func (c *Context) authoringSource() *SourcePart {
	if c.Program != nil && c.SourceOrigin > 0 && c.SourceOrigin <= len(c.Program.SourceParts) {
		part := &c.Program.SourceParts[c.SourceOrigin-1]
		if part.Directory != "" {
			return part
		}
	}
	return nil
}

// SetSourceOrigin hides a driver's synthetic wrapper around authored input.
func (p *Program) SetSourceOrigin(name string, start int, end int) {
	p.freeSourceParts()
	p.SourceParts = nil
	p.AddSourcePart(name, start, end, 0)
}

// LocateSource leaves external template sources unchanged. Returned names are
// borrowed from the program; retained diagnostics must copy them.
func (p *Program) LocateSource(name string, span source.Span) Location {
	location := Location{Source: name, Span: span}
	if p == nil || p.Script == nil || name != p.Script.Source.Name {
		return location
	}
	if len(p.SourceParts) != 0 && span.Start < p.SourceParts[0].Start {
		return Location{}
	}
	for i := len(p.SourceParts) - 1; i >= 0; i-- {
		part := p.SourceParts[i]
		if part.Module || part.Included != nil {
			// Independent spans belong to separate sources; only SourceOrigin
			// may select them, never a combined-script offset lookup.
			continue
		}
		if span.Start < part.Start {
			continue
		}
		start := span.Start
		if start > part.End {
			start = part.End
		}
		end := span.End
		if end > part.End {
			end = part.End
		}
		if end < start {
			end = start
		}
		location.Source = part.Name
		location.Span = source.Span{Start: start - part.Start + part.Offset, End: end - part.Start + part.Offset}
		return location
	}
	return location
}

func (p *Program) freeSourceParts() {
	for i := range p.SourceParts {
		mem.FreeString(p.Alloc, p.SourceParts[i].Name)
		mem.FreeString(p.Alloc, p.SourceParts[i].Directory)
		mem.FreeString(p.Alloc, p.SourceParts[i].observedCwd)
		mem.FreeString(p.Alloc, p.SourceParts[i].observedName)
		for j := range p.SourceParts[i].Includes {
			frame := p.SourceParts[i].Includes[j]
			mem.FreeString(p.Alloc, frame.Kind)
			mem.FreeString(p.Alloc, frame.Label)
			mem.FreeString(p.Alloc, frame.Source)
		}
		mem.FreeSlice(nil, p.SourceParts[i].Includes)
	}
	mem.FreeSlice(nil, p.SourceParts)
}
