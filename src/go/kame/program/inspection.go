package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type inspectionResource struct {
	Key core.ResourceKey
	Display string
	Request string
	Depth int
	Producer int
	Root bool
	Input bool
	Artifact bool
	Intermediate bool
	Product bool
	Configuration bool
	Potential bool
	Status string
}

type inspectionProducer struct {
	Plan Plan
	Resource int
	Depth int
	Stage int
	State int
	Outputs []int
	ContextUnknown bool
}

type inspectionDependency struct {
	Producer int
	Resource int
	Origin string
	OrderOnly bool
	Group int
}

type inspectionDeferred struct {
	Producer int
	Resource int
	Reason string
}

type inspectionGraph struct {
	Program *Program
	Resources []inspectionResource
	Producers []inspectionProducer
	Dependencies []inspectionDependency
	Deferred []inspectionDeferred
	Truncated bool
	Waiting bool
}

func (g *inspectionGraph) free() {
	a := g.Program.Alloc
	for i := range g.Resources {
		g.Resources[i].Key.Free(a)
		mem.FreeString(a, g.Resources[i].Display)
		mem.FreeString(a, g.Resources[i].Request)
	}
	for i := range g.Producers {
		g.Producers[i].Plan.Free(a)
		slices.Free(a, g.Producers[i].Outputs)
	}
	slices.Free(a, g.Resources)
	slices.Free(a, g.Producers)
	slices.Free(a, g.Dependencies)
	slices.Free(a, g.Deferred)
}

// WriteGraph emits the schema-2 resource inventory for one target.
func (p *Program) WriteGraph(out io.Writer, target string, depth int, kind string) diagnostic.Diagnostic {
	targets := []string{target}
	return p.WriteInspection(out, targets, depth, kind)
}

// WriteInspection is the common read-only graph query for both CLI hosts.
// Host yields replay traversal, reusing completed resolvers. Never create a
// normal rule instance just to determine a resource's producer.
func (p *Program) WriteInspection(out io.Writer, targets []string, depth int, kind string) diagnostic.Diagnostic {
	p.InspectionWaiting = false
	if depth < -1 || (kind != "plan" && kind != "inputs" && kind != "outputs") || len(targets) == 0 {
		return failure(p.Alloc, "OPT_VALUE_INVALID", "invalid inspection query")
	}
	g := inspectionGraph{Program: p}
	defer g.free()
	for i := range targets {
		key := p.inspectionKey(core.ResourceTarget, targets[i])
		id := g.resource(key, targets[i], 0)
		key.Free(p.Alloc)
		g.Resources[id-1].Root = true
	}
	// Breadth-first traversal gives shared resources their shortest root distance.
	for cursor := 0; cursor < len(g.Resources); cursor++ {
		if g.Resources[cursor].Configuration { continue }
		if d := g.visit(cursor+1, depth); d.Code != "" { return d }
		if g.Waiting { p.InspectionWaiting = true; return diagnostic.Diagnostic{} }
	}
	for i := range g.Producers {
		if d := g.stage(i+1, nil); d.Code != "" { return d }
	}
	g.sequenceStages()
	for pass := 0; pass < len(g.Producers); pass++ {
		for i := range g.Dependencies {
			d := g.Dependencies[i]
			if d.Origin == "configuration" { continue }
			parent := g.Producers[d.Producer-1]
			child := g.Resources[d.Resource-1].Producer
			if child != 0 && (parent.ContextUnknown || (parent.Plan.Rule != nil && (len(parent.Plan.Rule.Environment) != 0 || parent.Plan.Rule.Metadata != nil))) { g.Producers[child-1].ContextUnknown = true }
		}
	}
	for i := range g.Producers {
		if len(g.Producers[i].Outputs) == 0 { continue }
		result := p.inspectArtifactStatus(g.Producers[i].Plan)
		if result.Waiting { p.InspectionWaiting = true; return diagnostic.Diagnostic{} }
		for j := range g.Producers[i].Outputs {
			status := result.State.Statuses[j]
			if g.Producers[i].ContextUnknown && status != "missing" { status = "unknown" }
			g.Resources[g.Producers[i].Outputs[j]-1].Status = status
		}
	}
	// A matching record cannot prove reuse before an unresolved prerequisite is
	// rebuilt. Inspection never executes that prerequisite to settle the question.
	for pass := 0; pass < len(g.Producers); pass++ {
		for i := range g.Dependencies {
			d := g.Dependencies[i]
			if d.Origin == "configuration" || d.OrderOnly { continue }
			r := g.Resources[d.Resource-1]
			if r.Producer == 0 { continue }
			if r.Artifact && r.Status == "built" { continue }
			for j := range g.Producers[d.Producer-1].Outputs {
				output := &g.Resources[g.Producers[d.Producer-1].Outputs[j]-1]
				if output.Status == "built" { output.Status = "unknown" }
			}
		}
	}
	for i := range g.Dependencies {
		edge := g.Dependencies[i]
		if edge.Origin == "configuration" { continue }
		if g.Producers[edge.Producer-1].Plan.Key.Kind == core.ResourceFile && g.Resources[edge.Resource-1].Artifact {
			g.Resources[edge.Resource-1].Intermediate = true
		}
	}
	for i := range g.Resources {
		g.Resources[i].Product = g.Resources[i].Artifact && (g.Resources[i].Root || !g.Resources[i].Intermediate)
	}
	g.write(out, targets, depth, kind)
	return diagnostic.Diagnostic{}
}

func (p *Program) inspectionKey(kind core.ResourceKind, name string) core.ResourceKey {
	if kind == core.ResourceTarget {
		if isFileName(name) { kind = core.ResourceFile } else {
			selected := p.selectRule(name)
			if selected.Rule != nil { kind = resourceKind(selected.Rule) } else if p.Eval.Definition(name) != nil { kind = core.ResourceDefinition }
			if selected.Rule != nil && selected.Rule.Kind != rule.FileRule && selected.Diagnostic.Code == "" {
				keyName := targetArgumentKey(p.Alloc, selected.Target, selected.Arguments)
				key := core.NewResourceKey(p.Alloc, kind, keyName)
				mem.FreeString(p.Alloc, keyName)
				freeCaptures(p.Alloc, selected.Captures)
				freeArguments(p.Alloc, selected.Arguments)
				mem.FreeString(p.Alloc, selected.Target)
				selected.Diagnostic.Free(p.Alloc)
				return key
			}
			freeCaptures(p.Alloc, selected.Captures)
			freeArguments(p.Alloc, selected.Arguments)
			mem.FreeString(p.Alloc, selected.Target)
			selected.Diagnostic.Free(p.Alloc)
		}
	}
	if kind == core.ResourceFile || kind == core.ResourceGlob {
		canonical := p.canonicalTarget(name, true)
		key := core.NewResourceKey(p.Alloc, kind, canonical)
		mem.FreeString(p.Alloc, canonical)
		return key
	}
	return core.NewResourceKey(p.Alloc, kind, name)
}

func (g *inspectionGraph) resource(key core.ResourceKey, request string, depth int) int {
	for i := range g.Resources {
		if g.Resources[i].Key.Kind == key.Kind && g.Resources[i].Key.Name == key.Name {
			if depth < g.Resources[i].Depth { g.Resources[i].Depth = depth }
			return i+1
		}
	}
	p := g.Program
	display := cloneText(p.Alloc, request)
	if key.Kind == core.ResourceFile || key.Kind == core.ResourceGlob {
		mem.FreeString(p.Alloc, display)
		display = p.relativePath(key.Name)
	}
	g.Resources = slices.Append(p.Alloc, g.Resources, inspectionResource{Key: key.Clone(p.Alloc), Display: display, Request: cloneText(p.Alloc, request), Depth: depth})
	return len(g.Resources)
}

func (g *inspectionGraph) dependency(producer, resource int, origin string, ordered bool, group int) {
	for i := range g.Dependencies {
		e := g.Dependencies[i]
		if e.Producer == producer && e.Resource == resource && e.Origin == origin && e.OrderOnly == ordered && e.Group == group { return }
	}
	g.Resources[resource-1].Input = true
	g.Dependencies = slices.Append(g.Program.Alloc, g.Dependencies, inspectionDependency{Producer: producer, Resource: resource, Origin: origin, OrderOnly: ordered, Group: group})
}

func (g *inspectionGraph) deferred(producer, resource int, reason string) {
	for i := range g.Deferred {
		if g.Deferred[i].Producer == producer && g.Deferred[i].Resource == resource && g.Deferred[i].Reason == reason { return }
	}
	g.Deferred = slices.Append(g.Program.Alloc, g.Deferred, inspectionDeferred{Producer: producer, Resource: resource, Reason: reason})
}

func (g *inspectionGraph) visit(resource, limit int) diagnostic.Diagnostic {
	p := g.Program
	r := g.Resources[resource-1]
	if r.Key.Kind != core.ResourceFile && r.Key.Kind != core.ResourceDefinition && r.Key.Kind != core.ResourceTarget && r.Key.Kind != core.ResourceTask && r.Key.Kind != core.ResourceService { return diagnostic.Diagnostic{} }
	if r.Producer != 0 { return diagnostic.Diagnostic{} }
	if limit != -1 && r.Depth >= limit && !r.Root {
		selected := p.selectRule(r.Request)
		g.Resources[resource-1].Potential = selected.Rule != nil
		if selected.Rule != nil || p.Eval.Definition(r.Request) != nil { g.Truncated = true }
		freeCaptures(p.Alloc, selected.Captures); freeArguments(p.Alloc, selected.Arguments)
		mem.FreeString(p.Alloc, selected.Target)
		d := selected.Diagnostic
		if selected.Ambiguous { d.Free(p.Alloc); d = failure(p.Alloc, "TGT_AMBIG", "multiple rules match target") }
		if d.Code == "" && !g.Resources[resource-1].Potential && r.Key.Kind != core.ResourceFile && p.Eval.Definition(r.Request) == nil { d = failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+r.Request) }
		if d.Code != "" { return g.contextDiagnostic(d, resource) }
		return d
	}
	base := p.Plan(r.Request)
	if base.Diagnostic.Code != "" {
		if base.Diagnostic.Code == "TGT_NO_RULE" && r.Key.Kind == core.ResourceFile && !r.Root { base.Diagnostic.Free(p.Alloc); return diagnostic.Diagnostic{} }
		return g.contextDiagnostic(base.Diagnostic, resource)
	}
	for i := range g.Producers {
		if sameInspectionPlan(g.Producers[i].Plan, base.Plan) {
			g.Resources[resource-1].Producer = i+1
			base.Plan.Free(p.Alloc)
			return diagnostic.Diagnostic{}
		}
	}
	plan := base.Plan
	producer := len(g.Producers)+1
	g.Producers = slices.Append(p.Alloc, g.Producers, inspectionProducer{Plan: plan, Resource: resource, Depth: r.Depth})
	g.Resources[resource-1].Producer = producer
	if limit == 0 { if len(plan.Inputs) != 0 || (plan.Rule != nil && hasExpressionInput(plan.Rule)) { g.Truncated = true }; return diagnostic.Diagnostic{} }
	if plan.Rule != nil && plan.Rule.Kind == rule.FileRule {
		for i := range plan.Outputs {
			key := p.inspectionKey(core.ResourceFile, plan.Outputs[i])
			id := g.resource(key, plan.Outputs[i], r.Depth)
			key.Free(p.Alloc)
			g.Resources[id-1].Producer, g.Resources[id-1].Artifact = producer, true
			g.Producers[producer-1].Outputs = slices.Append(p.Alloc, g.Producers[producer-1].Outputs, id)
		}
	}
	if plan.Rule == nil || hasExpressionInput(plan.Rule) {
		expanded := p.expandReadOnlyPlan(r.Request, p.Forwarding, true)
		if expanded.Waiting { g.Waiting = true; return diagnostic.Diagnostic{} }
		if expanded.Diagnostic.Code != "" {
			if !p.unavailableGeneratedRead(expanded.Diagnostic) { return g.contextDiagnostic(expanded.Diagnostic, resource) }
			expanded.Diagnostic.Free(p.Alloc)
			g.observed(producer, r.Depth+1)
		} else {
			g.Producers[producer-1].Plan.Free(p.Alloc)
			g.Producers[producer-1].Plan = expanded.Plan
			plan = expanded.Plan
			g.observed(producer, r.Depth+1)
		}
	}
	group := 1
	for i := range plan.ResourceInputs {
		input := plan.ResourceInputs[i]
		key := p.inspectionKey(input.Key.Kind, input.Display)
		id := g.resource(key, input.Display, r.Depth+1)
		key.Free(p.Alloc)
		origin := "declared"
		if input.Computed { origin = "computed" }
		g.dependency(producer, id, origin, input.OrderOnly, group)
		if input.SequenceEnd { group++ }
	}
	g.configuration(producer, r.Depth+1)
	if plan.Rule != nil {
		for i := range plan.Rule.Body {
			value := plan.Rule.Body[i].Template
			if value == nil { continue }
			for j := range value.Parts {
				part := value.Parts[j]
				if part.Kind != template.Tool { continue }
				key := p.inspectionKey(core.ResourceTool, part.Text)
				id := g.resource(key, part.Text, r.Depth+1)
				key.Free(p.Alloc)
				g.dependency(producer, id, "declared", false, 0)
			}
		}
	}
	for i := range plan.GeneratorDependencies {
		key := p.inspectionKey(core.ResourceDefinition, plan.GeneratorDependencies[i])
		id := g.resource(key, plan.GeneratorDependencies[i], r.Depth+1)
		key.Free(p.Alloc)
		g.Resources[id-1].Configuration = true
		g.dependency(producer, id, "configuration", false, 0)
	}
	if plan.Rule != nil && len(plan.Rule.Body) != 0 {
		g.deferred(producer, 0, "runtime-discovery")
		g.deferred(producer, 0, "opaque-process-io")
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) unavailableGeneratedRead(d diagnostic.Diagnostic) bool {
	if d.Code != "FS_ERR" { return false }
	for i := range d.Frames {
		frame := d.Frames[i]
		if frame.Kind != "resource" { continue }
		key := p.inspectionKey(core.ResourceFile, frame.Label)
		node := p.Engine.Lookup(key)
		key.Free(p.Alloc)
		return node != nil && node.Current && node.Signature.Mode == core.SignatureMissing && p.HasTarget(frame.Label)
	}
	return false
}

func (g *inspectionGraph) configuration(producer, depth int) {
	p := g.Program
	if len(p.Eval.SourceParts) == 0 { g.source(producer, p.Parsed.Source.Name, depth) }
	for i := range p.Eval.SourceParts { g.source(producer, p.Eval.SourceParts[i].Name, depth) }
}

func (g *inspectionGraph) source(producer int, name string, depth int) {
	if name == "" || name[0] == '<' { return }
	p := g.Program
	key := p.inspectionKey(core.ResourceFile, name)
	id := g.resource(key, name, depth)
	key.Free(p.Alloc)
	g.Resources[id-1].Configuration = true
	g.dependency(producer, id, "configuration", false, 0)
}

// Preserve the resource observations of lazy input definitions, without exposing
// their values or the evaluator's private scoped-node identities.
func (g *inspectionGraph) observed(producer int, depth int) {
	p := g.Program
	var pending []*core.Node
	for i := range p.Instances { if p.Instances[i].Inspection && p.Instances[i].InspectionStatus == nil && sameInspectionPlan(p.Instances[i].Plan, g.Producers[producer-1].Plan) { pending = slices.Append(p.Alloc, pending, p.Instances[i].Node); break } }
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		for i := range node.Dynamic { if !slices.Contains(pending, node.Dynamic[i]) { pending = slices.Append(p.Alloc, pending, node.Dynamic[i]) } }
		if cursor == 0 || node.Key.Kind == core.ResourceOperation { continue }
		name := node.Key.Name
		name = eval.AuthoredDefinitionName(name)
		if name == "" || name[0] == 0 { continue }
		if node.Key.Kind == core.ResourceDefinition && name == g.Producers[producer-1].Plan.Target { continue }
		key := p.inspectionKey(node.Key.Kind, name)
		id := g.resource(key, name, depth)
		key.Free(p.Alloc)
		g.dependency(producer, id, "computed", false, 0)
		if node.Key.Kind == core.ResourceFile && node.Current && node.Signature.Mode == core.SignatureMissing && p.HasTarget(node.Key.Name) {
			g.deferred(producer, id, "generated-input-unavailable")
		}
	}
	slices.Free(p.Alloc, pending)
}

func (g *inspectionGraph) contextDiagnostic(d diagnostic.Diagnostic, resource int) diagnostic.Diagnostic {
	if !d.Owned { owned := d.Clone(g.Program.Alloc); d.Free(g.Program.Alloc); d = owned }
	if d.Target == "" { d.Target = cloneText(g.Program.Alloc, g.Resources[resource-1].Request) }
	if len(d.TargetStack) == 0 {
		var reversed []string
		cursor := resource
		for {
			reversed = slices.Append(g.Program.Alloc, reversed, g.Resources[cursor-1].Display)
			parent := 0
			for i := range g.Dependencies {
				e := g.Dependencies[i]
				if e.Resource != cursor { continue }
				candidate := g.Producers[e.Producer-1].Resource
				if g.Resources[candidate-1].Depth < g.Resources[cursor-1].Depth { parent = candidate; break }
			}
			if parent == 0 { break }
			cursor = parent
		}
		for i := len(reversed)-1; i >= 0; i-- { d.TargetStack = slices.Append(g.Program.Alloc, d.TargetStack, cloneText(g.Program.Alloc, reversed[i])) }
		slices.Free(g.Program.Alloc, reversed)
	}
	return d
}

func (g *inspectionGraph) stage(producer int, stack []int) diagnostic.Diagnostic {
	entry := g.Producers[producer-1]
	if entry.State == 2 { return diagnostic.Diagnostic{} }
	if entry.State == 1 {
		d := failure(g.Program.Alloc, "DEP_CYCLE", "dependency cycle during inspection")
		start := 0
		for i := range stack { if stack[i] == producer { start = i; break } }
		for i := start; i < len(stack); i++ { d.TargetStack = slices.Append(g.Program.Alloc, d.TargetStack, cloneText(g.Program.Alloc, g.Producers[stack[i]-1].Plan.Target)) }
		d.TargetStack = slices.Append(g.Program.Alloc, d.TargetStack, cloneText(g.Program.Alloc, entry.Plan.Target))
		d.Target = cloneText(g.Program.Alloc, entry.Plan.Target)
		return d
	}
	g.Producers[producer-1].State = 1
	path := slices.Clone(g.Program.Alloc, stack)
	path = slices.Append(g.Program.Alloc, path, producer)
	defer slices.Free(g.Program.Alloc, path)
	level := 1
	for i := range g.Dependencies {
		edge := g.Dependencies[i]
		if edge.Producer != producer || edge.Origin == "configuration" { continue }
		child := g.Resources[edge.Resource-1].Producer
		if child == 0 { continue }
		if d := g.stage(child, path); d.Code != "" { return d }
		if g.Producers[child-1].Stage >= level { level = g.Producers[child-1].Stage+1 }
	}
	g.Producers[producer-1].Stage, g.Producers[producer-1].State = level, 2
	return diagnostic.Diagnostic{}
}

func (g *inspectionGraph) reaches(from, to int, seen []int) bool {
	if from == to { return true }
	if slices.Contains(seen, from) { return false }
	path := slices.Clone(g.Program.Alloc, seen)
	path = slices.Append(g.Program.Alloc, path, from)
	defer slices.Free(g.Program.Alloc, path)
	for i := range g.Dependencies {
		e := g.Dependencies[i]
		if e.Producer != from || e.Origin == "configuration" { continue }
		child := g.Resources[e.Resource-1].Producer
		if child != 0 && g.reaches(child, to, path) { return true }
	}
	return false
}

func (g *inspectionGraph) sequenceStages() {
	// ponytail: bounded relaxation for inspection-only ordering; index incoming
	// edges if profiling shows large sequenced graphs dominate inspection time.
	for pass := 0; pass < len(g.Producers); pass++ {
		changed := false
		for i := range g.Dependencies {
			e := g.Dependencies[i]
			if e.Origin == "configuration" { continue }
			child := g.Resources[e.Resource-1].Producer
			if child == 0 { continue }
			for j := range g.Dependencies {
				previous := g.Dependencies[j]
				if e.Group == 0 || previous.Group == 0 || previous.Producer != e.Producer || previous.Group >= e.Group { continue }
				prior := g.Resources[previous.Resource-1].Producer
				if prior == 0 || g.reaches(prior, child, nil) { continue }
				for candidate := 1; candidate <= len(g.Producers); candidate++ {
					if !g.reaches(child, candidate, nil) || g.neededEarlier(e.Producer, e.Group, child, candidate) { continue }
					if g.Producers[candidate-1].Stage <= g.Producers[prior-1].Stage { g.Producers[candidate-1].Stage = g.Producers[prior-1].Stage+1; changed = true }
				}
			}
			if g.Producers[e.Producer-1].Stage <= g.Producers[child-1].Stage { g.Producers[e.Producer-1].Stage = g.Producers[child-1].Stage+1; changed = true }
		}
		if !changed { return }
	}
}

func (g *inspectionGraph) neededEarlier(consumer, group, later, candidate int) bool {
	if g.Resources[g.Producers[candidate-1].Resource-1].Root { return true }
	for i := range g.Dependencies {
		e := g.Dependencies[i]
		if e.Group == 0 || e.Origin == "configuration" { continue }
		prior := g.Resources[e.Resource-1].Producer
		if prior == 0 { continue }
		if e.Producer == consumer {
			if e.Group < group && g.reaches(prior, candidate, nil) { return true }
		} else if e.Group == 1 && !g.reaches(later, e.Producer, nil) && !g.reaches(prior, consumer, nil) && g.reaches(prior, candidate, nil) {
			return true
		}
	}
	return false
}
