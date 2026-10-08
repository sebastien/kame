package program

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

func inspectionLifetime(key core.ResourceKey) string {
	switch key.Kind {
	case core.ResourceFile:
		if strings.HasPrefix(key.Name, "mem:") { return "host" }
		return "persistent"
	case core.ResourceService: return "interest"
	case core.ResourceTarget, core.ResourceEnvironment: return "invocation"
	case core.ResourceDefinition, core.ResourceOperation, core.ResourceTask, core.ResourceGlob: return "generation"
	case core.ResourceTool: return "host"
	}
	return "unknown"
}

func (g *inspectionGraph) write(out io.Writer, targets []string, depth int, kind string) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema"); e.Int(2)
	e.Str("type"); e.Str(kind)
	e.Str("targets"); encodeStringArray(&e, targets)
	e.Str("depth"); e.Int(int64(depth))
	e.Str("scope"); e.Str("declared-and-read-only")
	e.Str("truncated"); e.Bool(g.Truncated)
	e.Str("resources"); e.BeginArray()
	for i := range g.Resources {
		r := g.Resources[i]
		e.BeginObject()
		e.Str("id"); e.Int(int64(i+1))
		e.Str("key"); encodeResourceKey(&e, r.Key)
		e.Str("display"); e.Str(r.Display)
		e.Str("roles"); e.BeginArray()
		if r.Input { e.Str("input") }
		if r.Artifact { e.Str("artifact") }
		if r.Intermediate { e.Str("intermediate") }
		if r.Product { e.Str("product") }
		if r.Configuration { e.Str("configuration") }
		e.EndArray()
		e.Str("lifetime"); e.Str(inspectionLifetime(r.Key))
		if r.Producer != 0 { e.Str("producer"); e.Int(int64(r.Producer)) }
		if r.Potential { e.Str("unvisitedProducer"); e.Bool(true) }
		if r.Artifact { e.Str("status"); e.Str(r.Status) }
		e.EndObject()
	}
	e.EndArray()
	e.Str("producers"); e.BeginArray()
	for i := range g.Producers {
		entry := g.Producers[i]
		plan := entry.Plan
		e.BeginObject()
		e.Str("id"); e.Int(int64(i+1))
		e.Str("resource"); e.Int(int64(entry.Resource))
		e.Str("target"); e.Str(plan.Target)
		e.Str("kind"); e.Str(eventResourceKind(plan.Key.Kind))
		e.Str("outputs"); e.BeginArray()
		for j := range entry.Outputs { e.Int(int64(entry.Outputs[j])) }
		e.EndArray()
		// Retain established single-producer plan details, not its old untyped
		// input/output arrays or a claim of established freshness.
		writeInspectionDetails(g.Program, &e, &plan)
		e.EndObject()
	}
	e.EndArray()
	e.Str("dependencies"); e.BeginArray()
	for i := range g.Dependencies {
		d := g.Dependencies[i]
		e.BeginObject()
		e.Str("producer"); e.Int(int64(d.Producer))
		e.Str("resource"); e.Int(int64(d.Resource))
		e.Str("origin"); e.Str(d.Origin)
		e.Str("orderOnly"); e.Bool(d.OrderOnly)
		if d.Group != 0 { e.Str("group"); e.Int(int64(d.Group)) }
		e.EndObject()
	}
	e.EndArray()
	e.Str("stages"); e.BeginArray()
	max := 0
	for i := range g.Producers { if g.Producers[i].Stage > max { max = g.Producers[i].Stage } }
	for stage := 1; stage <= max; stage++ {
		e.BeginObject()
		e.Str("number"); e.Int(int64(stage))
		e.Str("producers"); e.BeginArray()
		for i := range g.Producers { if g.Producers[i].Stage == stage { e.Int(int64(i+1)) } }
		e.EndArray(); e.EndObject()
	}
	e.EndArray()
	e.Str("deferred"); e.BeginArray()
	for i := range g.Deferred {
		d := g.Deferred[i]
		e.BeginObject()
		e.Str("reason"); e.Str(d.Reason)
		if d.Producer != 0 { e.Str("producer"); e.Int(int64(d.Producer)) }
		if d.Resource != 0 { e.Str("resource"); e.Int(int64(d.Resource)) }
		if d.Producer != 0 && g.Producers[d.Producer-1].Plan.Rule != nil {
			span := g.Producers[d.Producer-1].Plan.RuleSpan
			location := g.Program.Eval.LocateSource(g.Program.Parsed.Source.Name, source.Span{Start: span.Start, End: span.End})
			e.Str("source"); e.Str(location.Source)
			e.Str("span"); e.BeginObject(); e.Str("start"); e.Int(int64(location.Span.Start)); e.Str("end"); e.Int(int64(location.Span.End)); e.EndObject()
		}
		e.EndObject()
	}
	e.EndArray()
	if kind != "plan" {
		e.Str("items"); e.BeginArray()
		if depth != 0 { for i := range g.Resources { if (kind == "inputs" && g.Resources[i].Input) || (kind == "outputs" && g.Resources[i].Artifact) { e.Int(int64(i+1)) } } }
		e.EndArray()
	}
	e.EndObject(); e.Flush()
	io.WriteString(out, "\n")
}

func writeInspectionDetails(p *Program, e *json.Encoder, plan *Plan) {
	e.Str("freshness"); e.Str("unknown")
	e.Str("captures"); e.BeginArray()
	for i := range plan.Captures { e.BeginObject(); e.Str("name"); e.Str(plan.Captures[i].Name); e.Str("value"); e.Str(plan.Captures[i].Text); e.EndObject() }
	e.EndArray()
	if len(plan.Arguments) != 0 {
		e.Str("arguments"); e.BeginObject()
		for i := range plan.Arguments { e.Str(plan.Arguments[i].Name); e.Str(plan.Arguments[i].Value) }
		e.EndObject()
	}
	if len(plan.Configuration) != 0 {
		e.Str("configuration"); e.BeginObject()
		for i := range plan.Configuration { equal := strings.IndexByte(plan.Configuration[i], '='); e.Str(plan.Configuration[i][:equal]); e.Str(plan.Configuration[i][equal+1:]) }
		e.EndObject()
	}
	if plan.Generator != "" {
		e.Str("generator"); e.BeginObject(); e.Str("name"); e.Str(plan.Generator)
		e.Str("dependencies"); encodeStringArray(e, plan.GeneratorDependencies); e.EndObject()
	}
	if plan.Rule == nil {
		for i := range p.Eval.Definitions {
			definition := p.Eval.Definitions[i]
			if definition.Name != plan.Target { continue }
			location := p.Eval.LocateSource(p.Parsed.Source.Name, definition.Span)
			e.Str("source"); e.Str(location.Source)
			e.Str("span"); e.BeginObject(); e.Str("start"); e.Int(int64(location.Span.Start)); e.Str("end"); e.Int(int64(location.Span.End)); e.EndObject()
			break
		}
		return
	}
	location := p.Eval.LocateSource(p.Parsed.Source.Name, source.Span{Start: plan.RuleSpan.Start, End: plan.RuleSpan.End})
	e.Str("source"); e.Str(location.Source)
	e.Str("rule"); e.BeginObject()
	e.Str("start"); e.Int(int64(location.Span.Start))
	e.Str("end"); e.Int(int64(location.Span.End))
	start, end := plan.Rule.Header.Start, plan.Rule.Header.End
	if start >= 0 && end >= start && end <= len(p.Parsed.Source.Text) {
		e.Str("text"); e.Str(p.Parsed.Source.Text[start:end])
	}
	e.EndObject()
	if plan.Rule.Metadata != nil {
		metadata := expr.Compact(p.Alloc, plan.Rule.Metadata)
		e.Str("metadata"); e.Str(metadata)
		mem.FreeString(p.Alloc, metadata)
	}
	if len(plan.Rule.Environment) != 0 {
		e.Str("environment"); e.BeginArray()
		for i := range plan.Rule.Environment { e.Str(plan.Rule.Environment[i].Value) }
		e.EndArray()
	}
	if plan.Rule.Always { e.Str("always"); e.Bool(true) }
	e.Str("reusePolicy")
	switch plan.Key.Kind {
	case core.ResourceFile:
		if plan.Rule.Always { e.Str("always") } else { e.Str("content-signatures") }
	case core.ResourceTask: e.Str("fingerprint")
	case core.ResourceService: e.Str("while-required")
	default: e.Str("always")
	}
	// Retain established tool mappings without rendering recipe expressions.
	e.Str("tools"); e.BeginObject()
	var names []string
	for i := range plan.Tools {
		graphAppend(p.Alloc, &names, plan.Tools[i].Name)
		e.Str(plan.Tools[i].Name); e.Str(plan.Tools[i].Path)
	}
	for i := range plan.Rule.Body {
		t := plan.Rule.Body[i].Template
		if t == nil { continue }
		for j := range t.Parts {
			part := t.Parts[j]
			if part.Kind != template.Tool || graphContains(names, part.Text) { continue }
			graphAppend(p.Alloc, &names, part.Text)
			e.Str(part.Text); e.Str(p.ResolveTool(part.Text))
		}
	}
	FreeStrings(p.Alloc, names)
	e.EndObject()
}
