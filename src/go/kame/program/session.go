package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

type Fragment struct {
	Name string
	Text string
	Lang string
	Entries []string
	Inline bool
	SkipStatements bool
	Offset int
}

type SessionWork struct {
	Expression *expr.Expr
	Target string
	Selected bool
	Value bool
	ImplicitDefault bool
}

// Session owns one compiled program; work is sequenced by its host driver.
type Session struct {
	Program *Program
	Work []SessionWork
	JSON bool
	reported bool
	active int
	JoinExpression expr.Expr
}

// JSON sessions share the build event encoder, including binary-safe streams.
func (s *Session) SetJSON(enabled bool) {
	s.JSON = enabled
	if enabled { s.Program.Eval.SetDefinitionEffectSink(sessionEventEffect, s.Program) }
}

func sessionEventEffect(state any, effect eval.Effect) {
	p := state.(*Program)
	kind := Stdout
	if effect.Kind == eval.EffectErr { kind = Stderr } else if effect.Kind != eval.EffectOut && effect.Kind != eval.EffectYield { return }
	p.emit(Event{Kind: kind, Data: slices.Clone(p.Alloc, effect.Data)})
}

// Observe reports value/statement roots once. Recipe roots already emit their
// own lifecycle through observeInstances and must not be duplicated here.
func (s *Session) Observe(h *Handle) {
	if !s.JSON || s.reported || h == nil || !h.Definition { return }
	n := h.Node
	if !n.Current && n.State != core.NodeFailed && n.State != core.NodeCancelled { return }
	s.reported = true
	p := s.Program
	if s.active == len(s.Work) && n.State != core.NodeFailed && n.State != core.NodeCancelled { return }
	e := Event{Target: h.Target, Key: n.Key, NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, Kind: TargetCompleted}
	if n.State == core.NodeFailed || n.State == core.NodeCancelled {
		e.Kind = TargetFailed
		if n.State == core.NodeCancelled { e.Kind = TargetCancelled }
		e.Diagnostic = n.Diagnostic.Clone(p.Alloc)
	} else {
		if s.active < len(s.Work) && s.Work[s.active].Value && !p.Options.DryRun {
			value := e
			value.Kind, value.Value = TargetValue, n.Latest.Clone(p.Alloc)
			p.emit(value)
		}
	}
	p.emit(e)
}

type SessionCompile struct {
	Session *Session
	Diagnostics []diagnostic.Diagnostic
}

func (s *SessionCompile) Free(a mem.Allocator) {
	for i := range s.Diagnostics { s.Diagnostics[i].Free(a) }
	slices.Free(a, s.Diagnostics)
}

func (s *Session) Free() {
	if s == nil { return }
	a := s.Program.Alloc
	freeSessionWork(a, s.Work)
	s.Program.Free()
	mem.Free(a, s)
}

// CompileSession parses every fragment independently before registering their
// declarations in one scope. The shared text is only span/borrowed-text storage.
func CompileSession(a mem.Allocator, fragments []Fragment, registry *eval.Registry, options Options) SessionCompile {
	b := strings.NewBuilder(a)
	var offsets []int
	for i := range fragments {
		offsets = slices.Append(a, offsets, b.Len())
		b.WriteString(fragments[i].Text)
		b.WriteByte('\n')
	}
	parsed := mem.Alloc[script.Script](a)
	parsed.Alloc, parsed.Source = a, source.New(a, "<session>", b.String())
	parsed.Source.Expanded = true
	b.Free()
	var work []SessionWork
	valueDefinitions, ruleFragments, kashFragments := false, false, false
	for i := range fragments {
		f := fragments[i]
		part := script.ParseFragment(a, parsed.Source, f.Lang, offsets[i], offsets[i]+len(f.Text))
		for j := range part.Diagnostics { parsed.Diagnostics = slices.Append(a, parsed.Diagnostics, part.Diagnostics[j]) }
		slices.Free(a, part.Diagnostics)
		part.Diagnostics = nil
		for j := range part.Items {
			item := part.Items[j]
			parsed.Items = slices.Append(a, parsed.Items, item)
			if item.Kind == script.Include { parsed.Diagnostics = slices.Append(a, parsed.Diagnostics, source.Diagnostic{Code: "FEATURE_UNSUP", Severity: source.Error, Span: item.Span, Message: "includes in runner sessions are not yet supported"}) }
			if item.Kind == script.Definition && f.Lang == "km" { valueDefinitions = true }
			if len(f.Entries) == 0 && !f.SkipStatements && f.Lang != "kmk" && (item.Kind == script.Expression || item.Kind == script.Command) {
				work = slices.Append(a, work, SessionWork{Expression: item.Expression, Value: f.Lang != "kash"})
			}
		}
		// Transfer child ownership while retaining the original source storage.
		slices.Free(a, part.Items)
		part.Items = nil
		part.Free()
		if f.Lang == "kash" { kashFragments = true }
		if f.Lang == "kmk" { ruleFragments = true }
		for j := range f.Entries { work = slices.Append(a, work, SessionWork{Target: cloneText(a, f.Entries[j]), Selected: f.Lang == "km", Value: f.Lang == "km"}) }
		if f.Lang == "kmk" && !f.Inline && len(f.Entries) == 0 { work = slices.Append(a, work, SessionWork{Target: cloneText(a, "default"), ImplicitDefault: true}) }
	}
	if len(work) == 0 && (ruleFragments || (valueDefinitions && !kashFragments)) { work = slices.Append(a, work, SessionWork{Target: cloneText(a, "default"), Value: !ruleFragments, ImplicitDefault: true}) }
	compiled := Compile(a, parsed, registry, options)
	p := compiled.Program
	result := SessionCompile{}
	for i := range compiled.Diagnostics { result.Diagnostics = slices.Append(a, result.Diagnostics, compiled.Diagnostics[i].Clone(a)) }
	compiled.Free(a)
	if p == nil {
		parsed.Free()
		freeSessionWork(a, work)
	} else {
		p.ParsedOwned = true
		p.SessionPolicy = true
		if len(fragments) == 1 {
			mem.FreeString(a, parsed.Source.Name)
			parsed.Source.Name = cloneText(a, fragments[0].Name)
			parsed.Source.Expanded = fragments[0].Offset != 0
		}
		for i := range fragments { p.Eval.AddSourcePart(fragments[i].Name, offsets[i], offsets[i]+len(fragments[i].Text), fragments[i].Offset) }
		// Reject unknown static value entries before any prior statement executes.
		for i := range work {
			if work[i].Target != "" && work[i].Value && p.Eval.Definition(work[i].Target) == nil {
				function := false
				for j := range parsed.Items {
					item := parsed.Items[j]
					if item.Definition == nil || !item.Definition.Function || item.Definition.Name != work[i].Target { continue }
					e := mem.Alloc[expr.Expr](a)
					e.Kind, e.Text, e.Span = expr.Name, work[i].Target, item.Definition.NameSpan
					work[i].Expression = e
					parsed.Items = slices.Append(a, parsed.Items, script.ScriptItem{Kind: script.Expression, Span: e.Span, Expression: e})
					function = true
					break
				}
				if !function {
					code := "TGT_NO_RULE"
					if work[i].Target == "default" { code = "TGT_NO_DEFAULT" }
					result.Diagnostics = slices.Append(a, result.Diagnostics, failure(a, code, "unknown value entry: "+work[i].Target))
				}
			}
			if work[i].Target != "" && !work[i].Value {
				if work[i].ImplicitDefault && !p.HasTarget("default") {
					d := failure(a, "TGT_NO_DEFAULT", "no target was requested and no default target is defined")
					names := p.NamedTargets()
					joined := strings.Join(a, names, ", ")
					if len(names) == 0 { d.Notes = slices.Append(a, d.Notes, cloneText(a, "available targets: (none)")) } else { d.Notes = slices.Append(a, d.Notes, cloneText(a, "available targets: "+joined)) }
					mem.FreeString(a, joined)
					freeStrings(a, names)
					result.Diagnostics = slices.Append(a, result.Diagnostics, d)
					continue
				}
				// Preflight only authored target selection, not dynamic inputs:
				// those may depend on args or earlier session effects.
				selected := p.selectRule(work[i].Target)
				if selected.Ambiguous { result.Diagnostics = slices.Append(a, result.Diagnostics, failure(a, "TGT_AMBIG", "multiple rules match target")) } else if selected.Rule == nil && p.Eval.Definition(work[i].Target) == nil { result.Diagnostics = slices.Append(a, result.Diagnostics, failure(a, "TGT_NO_RULE", "no rule for target: "+work[i].Target)) }
				freeCaptures(a, selected.Captures)
			}
		}
		if len(result.Diagnostics) == 0 {
			s := mem.Alloc[Session](a)
			s.Program, s.Work = p, work
			result.Session = s
		} else { p.Free(); freeSessionWork(a, work) }
	}
	for i := range result.Diagnostics {
		position := result.Diagnostics[i].Span.Start
		if result.Diagnostics[i].Source != "<session>" { continue }
		owner := 0
		for j := range offsets { if offsets[j] <= position { owner = j } }
		copy := result.Diagnostics[i].Clone(a)
		result.Diagnostics[i].Free(a)
		mem.FreeString(a, copy.Source)
		copy.Source = cloneText(a, fragments[owner].Name)
		copy.Span.Start += fragments[owner].Offset-offsets[owner]
		copy.Span.End += fragments[owner].Offset-offsets[owner]
		result.Diagnostics[i] = copy
	}
	slices.Free(a, offsets)
	return result
}

type statementState struct { Program *Program; Expression *expr.Expr }

func freeSessionWork(a mem.Allocator, work []SessionWork) {
	for i := range work { mem.FreeString(a, work[i].Target) }
	slices.Free(a, work)
}

func freeStatement(a mem.Allocator, value any) { mem.Free(a, value.(*statementState)) }

func runStatement(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	state := c.Context().(*statementState)
	p := state.Program
	context := eval.Context{Program: p.Eval, Engine: c, Run: c.Allocator(), Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Args: p.Eval.DefinitionArgs, HasArgs: true, DependencyObserver: p.Eval.DefinitionDependencyObserver, ResolverState: p.Eval.DefinitionDependencyState}
	r := p.Eval.EvaluateWith(state.Expression, &context)
	if !r.Waiting && r.Diagnostic.Code == "" && p.Eval.DefinitionEffectSink != nil {
		for i := range context.Effects { p.Eval.DefinitionEffectSink(p.Eval.DefinitionEffectState, context.Effects[i]) }
	}
	eval.FreeEffects(c.Allocator(), context.Effects)
	if r.Waiting { r.Free(c.Allocator()); if c.Submitted() { return core.ProducerSubmitted }; return core.ProducerWaiting }
	if r.Diagnostic.Code != "" { c.Fail(r.Diagnostic); r.Diagnostic = diagnostic.Diagnostic{}; r.Free(c.Allocator()); return core.ProducerFailed }
	c.Publish(r.Value)
	r.Value = core.Value{}
	r.Free(c.Allocator())
	return core.ProducerCompleted
}

func (s *Session) Start(index int) HandleStart {
	s.reported = false
	s.active = index
	w := SessionWork{}
	if index < len(s.Work) { w = s.Work[index] }
	if index == len(s.Work) { s.JoinExpression.Kind = expr.InvocationJoin; w.Expression = &s.JoinExpression }
	if w.Expression == nil { return s.Program.Start(w.Target) }
	p := s.Program
	state := mem.Alloc[statementState](p.Alloc)
	state.Program, state.Expression = p, w.Expression
	var buffer [strconv.MaxIntBase10Len]byte
	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<statement:"+strconv.Itoa(buffer[:], index)+">")
	node := p.Engine.AddOwned(key, runStatement, state, freeStatement)
	key.Free(p.Alloc)
	if node == nil { mem.Free(p.Alloc, state); return HandleStart{Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot schedule statement")} }
	h := mem.Alloc[Handle](p.Alloc)
	h.Program, h.Node, h.Root, h.Target = p, node, p.Engine.RequestRoot(node), cloneText(p.Alloc, "<statement>")
	h.Definition = true // Value-producing root, not a recipe instance.
	return HandleStart{Handle: h}
}
