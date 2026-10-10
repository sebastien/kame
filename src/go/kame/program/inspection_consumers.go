package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Catalogue failures are reported only when their producer is affected. The
// graph owns these diagnostics, including across a host yield.
type inspectionConsumerFailure struct {
	Resource int
	Input bool
	Diagnostic diagnostic.Diagnostic
}

func (g *inspectionGraph) freeConsumerFailures() {
	for i := range g.ConsumerFailures { g.ConsumerFailures[i].Diagnostic.Free(g.Program.Alloc) }
	slices.Free(g.Program.Alloc, g.ConsumerFailures)
	g.ConsumerFailures = nil
}

// Consumer discovery needs a catalogue, not the prerequisite graph of the
// changed file. Seed concrete declarations, then instantiate patterns from
// known inputs (including computed inputs and not-yet-built artifacts).
func (g *inspectionGraph) discoverConsumers() diagnostic.Diagnostic {
	p := g.Program
	if !g.ConsumerSeeded { for i := range g.Resources {
		if !g.Resources[i].Root { continue }
		r := g.Resources[i]
		selected := p.selectRule(r.Request)
		invalid := selected.Ambiguous || selected.Diagnostic.Code != "" || (selected.Rule == nil && r.Key.Kind != core.ResourceFile && p.Eval.Definition(r.Request) == nil)
		freeCaptures(p.Alloc, selected.Captures); freeArguments(p.Alloc, selected.Arguments)
		mem.FreeString(p.Alloc, selected.Target); selected.Diagnostic.Free(p.Alloc)
		if invalid {
			result := p.Plan(r.Request)
			result.Plan.Free(p.Alloc)
			if result.Diagnostic.Code != "" { return g.contextDiagnostic(result.Diagnostic, i+1) }
		}
	} }
	if !g.ConsumerSeeded { for i := range p.Rules {
		r := p.Rules[i].Rule
		required := false
		for j := range r.Arguments { if !r.Arguments[j].Optional { required = true } }
		for j := range r.Outputs {
			output := r.Outputs[j]
			if output.Template && !inspectionInvertible(r, output.TargetForm) {
				key := core.NewResourceKey(p.Alloc, core.ResourceTarget, output.Text)
				id := g.resource(key, output.Text, 0)
				key.Free(p.Alloc)
				g.Resources[id-1].Potential = true
				g.deferred(0, id, "pattern-captures-unbound")
			}
			if output.Template || required { continue }
			key := p.inspectionKey(core.ResourceTarget, output.Text)
			g.resource(key, output.Text, 0)
			key.Free(p.Alloc)
		}
		if required {
			key := p.inspectionKey(core.ResourceTarget, r.Outputs[0].Text)
			id := g.resource(key, r.Outputs[0].Text, 0)
			key.Free(p.Alloc)
			g.Resources[id-1].Potential = true
			g.deferred(0, id, "target-arguments-required")
		}
	}; g.ConsumerSeeded = true }
	for ; g.ConsumerCursor < len(g.Resources); g.ConsumerCursor++ {
		cursor := g.ConsumerCursor
		r := g.Resources[cursor]
		if !r.Configuration && !(r.Potential && !r.Root) {
			if r.Root || g.QueryDepth == 0 {
				g.consumerDeclaration(cursor+1)
			} else if d := g.visit(cursor+1, -1); d.Code != "" {
				g.consumerDeclaration(cursor+1)
				g.consumerPlanningInputs(cursor+1)
				if producer := g.Resources[cursor].Producer; producer != 0 { g.observed(producer, 1) }
				g.ConsumerFailures = slices.Append(p.Alloc, g.ConsumerFailures, inspectionConsumerFailure{Resource: cursor+1, Diagnostic: d})
			}
			if g.Waiting {
				// visit has registered only this producer and its outputs before
				// yielding input resolution. Retry it without replaying the catalogue.
				producer := g.Resources[cursor].Producer
				entry := &g.Producers[producer-1]
				entry.Plan.Free(p.Alloc)
				slices.Free(p.Alloc, entry.Outputs)
				for i := range g.Resources { if g.Resources[i].Producer == producer { g.Resources[i].Producer = 0; g.Resources[i].Artifact = false } }
				g.Producers = g.Producers[:producer-1]
				return diagnostic.Diagnostic{}
			}
		}
		if r.Key.Kind != core.ResourceFile && r.Key.Kind != core.ResourceTarget && r.Key.Kind != core.ResourceTask && r.Key.Kind != core.ResourceService && r.Key.Kind != core.ResourceDefinition { continue }
		for i := range p.Rules {
			candidate := p.Rules[i].Rule
			for j := range candidate.Inputs {
				input := candidate.Inputs[j]
				if input.TargetForm == nil || input.OrderOnly { continue }
				// Use the same cwd-relative spelling as forward pattern selection.
				name := r.Display
				if path.IsAbs(input.Text) { name = r.Key.Name }
				match := input.TargetForm.MatchTarget(p.Alloc, name)
				if match == nil { continue }
				if match.Limited {
					match.Free(p.Alloc)
					g.ConsumerFailures = slices.Append(p.Alloc, g.ConsumerFailures, inspectionConsumerFailure{Resource: cursor+1, Input: true, Diagnostic: failure(p.Alloc, "PAT_LIMIT", "consumer pattern match exceeded its step budget")})
					continue
				}
				for k := range candidate.Outputs {
					output := candidate.Outputs[k]
					if !output.Template { continue }
					captures := inspectionReverseCaptures(p.Alloc, output.TargetForm, match.Captures)
					if captures == nil {
						key := core.NewResourceKey(p.Alloc, core.ResourceTarget, output.Text)
						id := g.resource(key, output.Text, 0)
						key.Free(p.Alloc)
						g.Resources[id-1].Potential = true
						g.deferred(0, id, "pattern-captures-unbound")
						continue
					}
					name := renderTarget(p.Alloc, output, captures)
					selected := p.selectRule(name)
					if selected.Diagnostic.Code != "" {
						freeCaptures(p.Alloc, selected.Captures); freeArguments(p.Alloc, selected.Arguments)
						mem.FreeString(p.Alloc, selected.Target)
						key := p.inspectionKey(core.ResourceTarget, name)
						id := g.resource(key, name, 0)
						key.Free(p.Alloc)
						g.ConsumerFailures = slices.Append(p.Alloc, g.ConsumerFailures, inspectionConsumerFailure{Resource: id, Diagnostic: selected.Diagnostic})
						g.consumerRuleDeclaration(id, candidate, captures)
						slices.Free(p.Alloc, captures)
						mem.FreeString(p.Alloc, name)
						continue
					}
					valid := selected.Rule == candidate && !selected.Ambiguous && selected.Diagnostic.Code == ""
					ambiguous := selected.Ambiguous
					freeCaptures(p.Alloc, selected.Captures); freeArguments(p.Alloc, selected.Arguments)
					mem.FreeString(p.Alloc, selected.Target); selected.Diagnostic.Free(p.Alloc)
					if ambiguous {
						key := p.inspectionKey(core.ResourceTarget, name)
						id := g.resource(key, name, 0)
						key.Free(p.Alloc)
						g.ConsumerFailures = slices.Append(p.Alloc, g.ConsumerFailures, inspectionConsumerFailure{Resource: id, Diagnostic: failure(p.Alloc, "TGT_AMBIG", "multiple rules match inferred consumer")})
						// Retain the inferred declared edge even when selection is
						// ambiguous, so an affected ambiguity remains an error.
						g.consumerRuleDeclaration(id, candidate, captures)
						slices.Free(p.Alloc, captures)
						mem.FreeString(p.Alloc, name)
						continue
					}
					if valid {
						key := p.inspectionKey(core.ResourceTarget, name)
						g.resource(key, name, 0)
						key.Free(p.Alloc)
					}
					mem.FreeString(p.Alloc, name)
					slices.Free(p.Alloc, captures)
				}
				match.Free(p.Alloc)
			}
		}
		// A recursive pattern can describe infinitely many consumer instances.
		// ponytail: bounded catalogue; replace with demand-indexed discovery if
		// real projects exceed this inspection-only ceiling.
		if len(g.Resources) > 16384 {
			g.deferred(0, cursor+1, "consumer-instance-limit")
			break
		}
	}
	// Globs observe membership even when a declared artifact does not exist yet.
	// Retain a potential computed edge, never fabricate the expression's value.
	count := len(g.Dependencies)
	for i := 0; i < count; i++ {
		edge := g.Dependencies[i]
		glob := g.Resources[edge.Resource-1]
		if glob.Key.Kind != core.ResourceGlob { continue }
		for j := range g.Resources {
			r := g.Resources[j]
			if r.Key.Kind == core.ResourceFile && inspectionGlobMatch(glob.Key.Name, r.Key.Name) {
				known := false
				for k := range g.Dependencies { if g.Dependencies[k].Producer == edge.Producer && g.Dependencies[k].Resource == j+1 && g.Dependencies[k].Origin != "configuration" && (!g.Dependencies[k].OrderOnly || edge.OrderOnly) { known = true; break } }
				if known { continue }
				g.dependency(edge.Producer, j+1, "computed", edge.OrderOnly, edge.Group)
				g.Dependencies[len(g.Dependencies)-1].Potential = true
			}
		}
	}
	depths := g.consumerDepths()
	defer slices.Free(p.Alloc, depths)
	for i := range g.ConsumerFailures {
		entry := g.ConsumerFailures[i]
		producer := g.Resources[entry.Resource-1].Producer
		if entry.Input {
			depth := -1
			if producer != 0 { depth = depths[producer-1] }
			if g.Resources[entry.Resource-1].Root { depth = 0 }
			if depth >= 0 && (g.QueryDepth == -1 || depth < g.QueryDepth) { return entry.Diagnostic.Clone(p.Alloc) }
			continue
		}
		if producer != 0 && depths[producer-1] >= 0 && (g.QueryDepth == -1 || depths[producer-1] <= g.QueryDepth) { return entry.Diagnostic.Clone(p.Alloc) }
		if producer == 0 { for j := range g.Dependencies {
			edge := g.Dependencies[j]
			if edge.Resource == entry.Resource && depths[edge.Producer-1] >= 0 && (g.QueryDepth == -1 || depths[edge.Producer-1] <= g.QueryDepth) { return entry.Diagnostic.Clone(p.Alloc) }
		} }
		if producer != 0 { g.deferred(0, entry.Resource, "consumer-inputs-unavailable") }
	}
	// Seeds were catalogued without resolving their inputs. Only a seed that
	// is itself downstream of another requested resource needs validation.
	for i := range g.Producers {
		entry := g.Producers[i]
		if !g.Resources[entry.Resource-1].Root || depths[i] < 0 || (g.QueryDepth != -1 && depths[i] > g.QueryDepth) { continue }
		result := p.expandReadOnlyPlan(g.Resources[entry.Resource-1].Request, p.Forwarding, true)
		if result.Waiting { g.Waiting = true; return diagnostic.Diagnostic{} }
		if result.Diagnostic.Code != "" {
			if !p.unavailableGeneratedRead(result.Diagnostic) { return g.contextDiagnostic(result.Diagnostic, entry.Resource) }
			result.Diagnostic.Free(p.Alloc)
			g.observed(i+1, 1)
			continue
		}
		g.Producers[i].Plan.Free(p.Alloc)
		g.Producers[i].Plan = result.Plan
		g.observed(i+1, 1)
		group := 1
		for j := range result.Plan.ResourceInputs {
			input := result.Plan.ResourceInputs[j]
			key := p.inspectionKey(input.Key.Kind, input.Display)
			id := g.resource(key, input.Display, 1)
			key.Free(p.Alloc)
			origin := "declared"
			if input.Computed { origin = "computed" }
			g.dependency(i+1, id, origin, input.OrderOnly, group)
			if input.SequenceEnd { group++ }
		}
		g.configuration(i+1, 1)
		if result.Plan.Rule != nil && len(result.Plan.Rule.Body) != 0 {
			g.deferred(i+1, 0, "runtime-discovery")
			g.deferred(i+1, 0, "opaque-process-io")
		}
	}
	if g.ConsumerCursor < len(g.Resources) && len(g.Resources) <= 16384 { return g.discoverConsumers() }
	return diagnostic.Diagnostic{}
}

// Invert renderInput's lookup: named references win over positional aliases;
// anonymous references use their input occurrence index. Values borrow the
// match, and names borrow the output template; only the slice is owned.
func inspectionReverseCaptures(a mem.Allocator, output *template.Target, inputs []template.CaptureValue) []template.CaptureValue {
	var captures []template.CaptureValue
	for i := range output.Parts { if output.Parts[i].Kind == template.Capture { captures = slices.Append(a, captures, template.CaptureValue{Name: output.Parts[i].Name}) } }
	bound := slices.Make[bool](a, len(captures))
	defer slices.Free(a, bound)
	for i := range inputs {
		index := -1
		if inputs[i].Name == "" { index = i } else {
			for j := range captures { if captures[j].Name == inputs[i].Name { index = j; break } }
			if index < 0 { if position, ok := positionalCapture(inputs[i].Name); ok { index = position } }
		}
		if index < 0 || index >= len(captures) { continue }
		for j := range captures {
			if j != index && (captures[index].Name == "" || captures[j].Name != captures[index].Name) { continue }
			if bound[j] && captures[j].Text != inputs[i].Text { slices.Free(a, captures); return nil }
			captures[j].Text, bound[j] = inputs[i].Text, true
		}
	}
	for i := range bound { if !bound[i] { slices.Free(a, captures); return nil } }
	return captures
}

func inspectionInvertible(r *rule.Rule, output *template.Target) bool {
	for i := range r.Inputs {
		input := r.Inputs[i].TargetForm
		if input == nil { continue }
		var captures []template.CaptureValue
		for j := range input.Parts { if input.Parts[j].Kind == template.Capture { captures = slices.Append(input.Alloc, captures, template.CaptureValue{Name: input.Parts[j].Name}) } }
		reversed := inspectionReverseCaptures(input.Alloc, output, captures)
		bound := reversed != nil
		slices.Free(input.Alloc, reversed)
		slices.Free(input.Alloc, captures)
		if bound { return true }
	}
	return false
}

// A failed resolver still has authored edges. Preserve those edges before
// deciding whether its diagnostic belongs to this downstream query.
func (g *inspectionGraph) consumerDeclaration(resource int) {
	p := g.Program
	selected := p.selectRule(g.Resources[resource-1].Request)
	if selected.Rule != nil {
		g.consumerRuleDeclaration(resource, selected.Rule, selected.Captures)
		producer := g.Resources[resource-1].Producer
		if len(g.Producers[producer-1].Plan.Arguments) == 0 { g.Producers[producer-1].Plan.Arguments = cloneArguments(p.Alloc, selected.Arguments) }
	}
	freeCaptures(p.Alloc, selected.Captures); freeArguments(p.Alloc, selected.Arguments)
	mem.FreeString(p.Alloc, selected.Target); selected.Diagnostic.Free(p.Alloc)
}

func (g *inspectionGraph) consumerRuleDeclaration(resource int, r *rule.Rule, captures []template.CaptureValue) {
	p := g.Program
	producer := g.Resources[resource-1].Producer
	if producer == 0 {
		plan := Plan{Rule: r, Target: cloneText(p.Alloc, g.Resources[resource-1].Request), Key: g.Resources[resource-1].Key.Clone(p.Alloc), Captures: cloneCaptures(p.Alloc, captures)}
		for i := range r.Outputs { plan.Outputs = slices.Append(p.Alloc, plan.Outputs, renderTarget(p.Alloc, r.Outputs[i], captures)) }
		g.Producers = slices.Append(p.Alloc, g.Producers, inspectionProducer{Plan: plan, Resource: resource})
		producer = len(g.Producers)
		g.Resources[resource-1].Producer = producer
		if r.Kind == rule.FileRule { for i := range plan.Outputs {
			key := p.inspectionKey(core.ResourceFile, plan.Outputs[i])
			id := g.resource(key, plan.Outputs[i], 0)
			key.Free(p.Alloc)
			g.Resources[id-1].Producer, g.Resources[id-1].Artifact = producer, true
			g.Producers[producer-1].Outputs = slices.Append(p.Alloc, g.Producers[producer-1].Outputs, id)
		} }
	}
	group := 1
	for i := range r.Inputs {
		input := r.Inputs[i]
		if input.Kind == rule.InputExpression || input.Kind == rule.InputString || input.Kind == rule.InputWildcard { if input.SequenceEnd { group++ }; continue }
		name := cloneText(p.Alloc, input.Text)
		if input.Kind == rule.InputTemplate { mem.FreeString(p.Alloc, name); name = renderInput(p.Alloc, input, captures) }
		key := p.inspectionKey(core.ResourceTarget, name)
		id := g.resource(key, name, 1)
		key.Free(p.Alloc); mem.FreeString(p.Alloc, name)
		g.dependency(producer, id, "declared", input.OrderOnly, group)
		if input.SequenceEnd { group++ }
	}
	g.observed(producer, 1)
}

// Plan discards its partially accumulated inputs on an error. Re-evaluate in
// the same pure planning phase to retain known computed edges on either side
// of that error. This never resolves deferred reads or relaxes capabilities.
func (g *inspectionGraph) consumerPlanningInputs(resource int) {
	p := g.Program
	producer := g.Resources[resource-1].Producer
	if producer == 0 || g.Producers[producer-1].Plan.Rule == nil { return }
	plan := clonePlan(p.Alloc, g.Producers[producer-1].Plan)
	defer plan.Free(p.Alloc)
	freeStrings(p.Alloc, plan.Inputs); plan.Inputs = nil
	freePlanInputs(p.Alloc, plan.ResourceInputs, true); plan.ResourceInputs = nil
	group := 1
	for i := range plan.Rule.Inputs {
		input := plan.Rule.Inputs[i]
		before := len(plan.ResourceInputs)
		computed := input.Kind == rule.InputExpression || input.Kind == rule.InputString || input.Kind == rule.InputWildcard
		if computed {
			d := p.planInputExpression(input, &plan)
			d.Free(p.Alloc)
		} else {
			name := cloneText(p.Alloc, input.Text)
			if input.Kind == rule.InputTemplate { mem.FreeString(p.Alloc, name); name = renderInput(p.Alloc, input, plan.Captures) }
			plan.Inputs = slices.Append(p.Alloc, plan.Inputs, name)
			plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, name), Key: core.NewResourceKey(p.Alloc, core.ResourceTarget, name)})
		}
		for j := before; j < len(plan.ResourceInputs); j++ {
			entry := plan.ResourceInputs[j]
			key := p.inspectionKey(entry.Key.Kind, entry.Display)
			id := g.resource(key, entry.Display, 1)
			key.Free(p.Alloc)
			origin := "declared"
			if computed { origin = "computed" }
			g.dependency(producer, id, origin, input.OrderOnly, group)
		}
		if input.SequenceEnd { group++ }
	}
}

func (g *inspectionGraph) consumerDepths() []int {
	a := g.Program.Alloc
	distances := slices.Make[int](a, len(g.Resources))
	defer slices.Free(a, distances)
	depths := slices.Make[int](a, len(g.Producers))
	for i := range distances { distances[i] = -1; if g.Resources[i].Root { distances[i] = 0 } }
	for i := range depths { depths[i] = -1 }
	for pass := 0; pass <= len(g.Producers); pass++ {
		changed := false
		for i := range g.Dependencies {
			edge := g.Dependencies[i]
			if edge.OrderOnly || distances[edge.Resource-1] < 0 { continue }
			depth := distances[edge.Resource-1]+1
			if depths[edge.Producer-1] >= 0 && depths[edge.Producer-1] <= depth { continue }
			depths[edge.Producer-1] = depth
			entry := g.Producers[edge.Producer-1]
			if distances[entry.Resource-1] < 0 || distances[entry.Resource-1] > depth { distances[entry.Resource-1] = depth }
			for j := range entry.Outputs { id := entry.Outputs[j]-1; if distances[id] < 0 || distances[id] > depth { distances[id] = depth } }
			changed = true
		}
		if !changed { break }
	}
	return depths
}

// Match glob path segments with the host's wildcard semantics: ** includes
// zero or more directories; ordinary * never crosses a directory separator.
func inspectionGlobMatch(pattern, name string) bool {
	end := strings.IndexByte(pattern, '/')
	if end < 0 {
		if pattern == "**" { return true }
		matched, err := path.Match(pattern, name)
		return err == nil && matched
	}
	segment := pattern[:end]
	cut := strings.IndexByte(name, '/')
	if segment == "**" {
		if inspectionGlobMatch(pattern[end+1:], name) { return true }
		return cut >= 0 && inspectionGlobMatch(pattern, name[cut+1:])
	}
	if cut < 0 { return false }
	matched, err := path.Match(segment, name[:cut])
	return err == nil && matched && inspectionGlobMatch(pattern[end+1:], name[cut+1:])
}

// Project the catalogue onto consumers reachable from the requested resources.
// Prerequisite identities remain as context, but their unrelated producers do
// not become affected work. Distances count consumer hops, not prerequisites.
func (g *inspectionGraph) selectConsumers(limit int) {
	p := g.Program
	distances := slices.Make[int](p.Alloc, len(g.Resources))
	producerDepths := slices.Make[int](p.Alloc, len(g.Producers))
	defer slices.Free(p.Alloc, distances)
	defer slices.Free(p.Alloc, producerDepths)
	for i := range distances { distances[i] = -1; if g.Resources[i].Root { distances[i] = 0 } }
	for i := range producerDepths { producerDepths[i] = -1 }
	for pass := 0; pass <= len(g.Producers); pass++ {
		changed := false
		for i := range g.Dependencies {
			edge := g.Dependencies[i]
			distance := distances[edge.Resource-1]
			if distance < 0 || edge.OrderOnly { continue }
			depth := distance+1
			if producerDepths[edge.Producer-1] >= 0 && producerDepths[edge.Producer-1] <= depth { continue }
			producerDepths[edge.Producer-1] = depth
			producer := g.Producers[edge.Producer-1]
			if distances[producer.Resource-1] < 0 || distances[producer.Resource-1] > depth { distances[producer.Resource-1] = depth }
			for j := range producer.Outputs {
				id := producer.Outputs[j]-1
				if distances[id] < 0 || distances[id] > depth { distances[id] = depth }
			}
			changed = true
		}
		if !changed { break }
	}
	if limit != -1 {
		for i := range producerDepths { if producerDepths[i] > limit { producerDepths[i] = -1; g.Truncated = true } }
		for i := range distances { if distances[i] > limit { distances[i] = -1 } }
	}
	filtered := inspectionGraph{Program: p, Downstream: true, Truncated: g.Truncated}
	filtered.QueryTargets, filtered.QueryKind, filtered.QueryDepth = g.QueryTargets, g.QueryKind, g.QueryDepth
	g.QueryTargets, g.QueryKind = nil, ""
	resources := slices.Make[int](p.Alloc, len(g.Resources))
	producers := slices.Make[int](p.Alloc, len(g.Producers))
	defer slices.Free(p.Alloc, resources)
	defer slices.Free(p.Alloc, producers)
	for i := range g.Resources {
		keep := distances[i] >= 0
		for j := range g.Dependencies { if g.Dependencies[j].Resource == i+1 && producerDepths[g.Dependencies[j].Producer-1] >= 0 { keep = true } }
		if limit != 0 { for j := range g.Deferred { if g.Deferred[j].Producer == 0 && g.Deferred[j].Resource == i+1 { keep = true } } }
		if !keep { continue }
		r := g.Resources[i]
		id := filtered.resource(r.Key, r.Request, distances[i])
		resources[i] = id
		filtered.Resources[id-1].Root = r.Root
		filtered.Resources[id-1].Configuration = r.Configuration
		filtered.Resources[id-1].Input = r.Root
		filtered.Resources[id-1].Potential = r.Potential || (r.Producer != 0 && producerDepths[r.Producer-1] < 0)
	}
	for i := range g.Producers {
		if producerDepths[i] < 0 { continue }
		entry := g.Producers[i]
		producer := inspectionProducer{Plan: clonePlan(p.Alloc, entry.Plan), Resource: resources[entry.Resource-1], Depth: producerDepths[i]}
		for j := range entry.Outputs { producer.Outputs = slices.Append(p.Alloc, producer.Outputs, resources[entry.Outputs[j]-1]) }
		filtered.Producers = slices.Append(p.Alloc, filtered.Producers, producer)
		id := len(filtered.Producers)
		producers[i] = id
		filtered.Resources[producer.Resource-1].Producer = id
		for j := range producer.Outputs { filtered.Resources[producer.Outputs[j]-1].Producer = id; filtered.Resources[producer.Outputs[j]-1].Artifact = true }
	}
	for i := range g.Dependencies {
		edge := g.Dependencies[i]
		if producers[edge.Producer-1] != 0 {
			filtered.dependency(producers[edge.Producer-1], resources[edge.Resource-1], edge.Origin, edge.OrderOnly, edge.Group)
			filtered.Dependencies[len(filtered.Dependencies)-1].Potential = edge.Potential
		}
	}
	for i := range g.Deferred {
		boundary := g.Deferred[i]
		producer, resource := 0, 0
		if boundary.Producer != 0 { producer = producers[boundary.Producer-1]; if producer == 0 { continue } }
		if boundary.Resource != 0 { resource = resources[boundary.Resource-1] }
		// Global discovery limitations must remain visible even if no affected
		// concrete instance could be inferred from that declaration.
		if boundary.Producer == 0 && resource == 0 { filtered.deferred(0, 0, boundary.Reason) } else { filtered.deferred(producer, resource, boundary.Reason) }
	}
	g.free()
	*g = filtered
}
