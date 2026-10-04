package rule

import (
	"kame/lang/expr"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *parser) ruleMetadata(r *Rule, start int, end int) {
	start, end = trim(p.s.Text, start, end)
	if start == end || p.s.Text[start] != '[' { p.ruleEnvironment(r, start, end); return }
	parsed := expr.ParsePrefix(p.a, p.s, start)
	p.takeDiagnostics(parsed.Diagnostics)
	slices.Free(p.a, parsed.Diagnostics)
	r.Metadata = parsed.Expr
	if parsed.End != end || r.Metadata == nil || r.Metadata.Kind != expr.Record { p.error(start, end, "rule metadata must be a record"); return }
	for i := range r.Metadata.Fields {
		field := r.Metadata.Fields[i]
		if field.Key != "shell" && field.Key != "env" && !(r.Kind == ServiceRule && serviceMetadataField(field.Key)) { p.error(field.Span.Start, field.Span.End, "unknown rule metadata field") }
		for j := 0; j < i; j++ { if r.Metadata.Fields[j].Key == field.Key { p.error(field.Span.Start, field.Span.End, "duplicate rule metadata field") } }
	}
}

func serviceMetadataField(name string) bool {
	switch name {
	case "ready", "health", "restart", "stop", "log-bytes": return true
	default: return false
	}
}

// ruleEnvironment accepts a metadata suffix: ; env "NAME=value" ... .
// Literal assignments keep registration free of evaluation and host effects.
func (p *parser) ruleEnvironment(r *Rule, start int, end int) {
	items := ranges(p.a, p.s.Text, start, end)
	defer slices.Free(p.a, items)
	if len(items) < 2 || p.s.Text[items[0].Start:items[0].End] != "env" {
		p.error(start, end, "expected env followed by quoted NAME=value assignments")
		return
	}
	for i := 1; i < len(items); i++ {
		span := items[i]
		parsed := expr.ParsePrefix(p.a, p.s, span.Start)
		p.takeDiagnostics(parsed.Diagnostics)
		slices.Free(p.a, parsed.Diagnostics)
		literal := parsed.Expr != nil && parsed.Expr.Kind == expr.String && parsed.End == span.End
		b := strings.NewBuilder(p.a)
		if literal {
			for j := range parsed.Expr.Parts {
				if parsed.Expr.Parts[j].Expr != nil {
					literal = false
					break
				}
				b.WriteString(parsed.Expr.Parts[j].Text)
			}
		}
		value := b.String()
		equal := strings.IndexByte(value, '=')
		if !literal || equal < 1 || !environmentName(value[:equal]) || strings.IndexByte(value, 0) >= 0 {
			p.error(span.Start, span.End, "environment assignment must be a literal quoted NAME=value with a valid name and no NUL")
		} else {
			r.Environment = slices.Append(p.a, r.Environment, EnvironmentAssignment{Text: p.s.Text[span.Start:span.End], Value: owned(p.a, value), Span: span})
		}
		b.Free()
		expr.Free(p.a, parsed.Expr)
	}
}

func environmentName(name string) bool {
	for i := range name {
		c := name[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return len(name) > 0
}
