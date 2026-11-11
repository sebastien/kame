package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/source"
	"kame/lang/template"

	"strings"
)

type outputResolution struct {
	Rule       *rule.Rule
	Diagnostic diagnostic.Diagnostic
}

// Resolved rules own only their expanded outputs; all other syntax is borrowed.
func freeResolvedRule(a mem.Allocator, r *rule.Rule) {
	for i := range r.Outputs {
		mem.FreeString(a, r.Outputs[i].Text)
		r.Outputs[i].TargetForm.Free()
	}
	mem.FreeSlice(nil, r.Outputs)
	mem.Free(a, r)
}

func (p *Program) outputDiagnostic(span source.Span, code string, message string) diagnostic.Diagnostic {
	d := failureAt(p.Alloc, code, diagnostic.Span{Start: span.Start, End: span.End}, message)
	d.Source = cloneText(p.Alloc, p.Parsed.Source.Name)
	return d
}

func (p *Program) appendOutput(value core.Value, span source.Span, outputs *[]rule.Target) diagnostic.Diagnostic {
	if value.Kind == core.Nil {
		return diagnostic.Diagnostic{}
	}
	if value.Kind == core.List {
		for i := range value.List {
			if d := p.appendOutput(value.List[i], span, outputs); d.Code != "" {
				return d
			}
		}
		return diagnostic.Diagnostic{}
	}
	if (value.Kind != core.String && value.Kind != core.Pattern) || value.Text == "" {
		return p.outputDiagnostic(span, "EXPR_INVALID", "rule outputs require nonempty strings or patterns")
	}
	*outputs = append(*outputs, rule.Target{Text: cloneText(p.Alloc, value.Text), Span: span, Decoded: value.Kind == core.String})
	return diagnostic.Diagnostic{}
}

// Output registration must retain pattern provenance that ordinary text rendering
// intentionally discards. Lists keep the same space-separated rendering as Render.
func appendOutputText(a mem.Allocator, b *strings.Builder, value core.Value, patterns *[]source.Span) bool {
	if value.Kind == core.List {
		for i := range value.List {
			if i != 0 {
				b.WriteByte(' ')
			}
			if !appendOutputText(a, b, value.List[i], patterns) {
				return false
			}
		}
		return true
	}
	text, ok := eval.Stringify(a, value)
	if !ok {
		return false
	}
	if value.Kind == core.Pattern {
		*patterns = append(*patterns, source.Span{Start: b.Len(), End: b.Len() + len(text)})
	}
	b.WriteString(text)
	mem.FreeString(a, text)
	return true
}

func (p *Program) renderOutput(output rule.Target, context *eval.Context, patterns *[]source.Span) eval.Result {
	b := strings.Builder{}
	defer b.Reset()
	for i := range output.Expansion.Parts {
		part := &output.Expansion.Parts[i]
		if part.Kind == template.Literal {
			b.WriteString(part.Text)
			continue
		}
		var result eval.Result
		if part.Expr != nil {
			result = p.Eval.EvaluateWith(part.Expr, context)
		} else {
			// Keep the ordinary selector/tool diagnostics and planning restrictions.
			one := template.String{Parts: output.Expansion.Parts[i : i+1], Span: part.Span}
			result = p.Eval.Render(p.Alloc, &one, p.Eval.Scope, context)
		}
		if result.Waiting || result.Completed || result.Stream != nil || result.Diagnostic.Code != "" || context.PhaseInvalid() || len(context.Effects) != 0 {
			return result
		}
		ok := appendOutputText(p.Alloc, &b, result.Value, patterns)
		kind := result.Value.Kind
		result.Free(p.Alloc)
		if !ok {
			return eval.Result{Diagnostic: p.outputDiagnostic(part.Span, "EXPR_INVALID", "cannot render "+core.KindName(kind)+" as text; use an explicit conversion")}
		}
	}
	return eval.Result{Value: core.NewString(p.Alloc, b.String())}
}

func (p *Program) resolveOutputs(authored *rule.Rule) outputResolution {
	computed := false
	for i := range authored.Outputs {
		if authored.Outputs[i].Expansion != nil {
			computed = true
		}
	}
	if !computed {
		return outputResolution{Rule: authored}
	}
	state := generatedEvaluation{Program: p}
	context := eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Phase: eval.PlanningPhase, ResolverState: &state, ResolveDefinition: resolveGeneratedDefinition}
	var outputs []rule.Target
	var d diagnostic.Diagnostic
	for i := range authored.Outputs {
		output := authored.Outputs[i]
		if output.Expansion == nil {
			outputs = append(outputs, rule.Target{Text: cloneText(p.Alloc, output.Text), Span: output.Span})
			continue
		}
		var result eval.Result
		var patterns []source.Span
		if output.Kind == rule.TargetExpression {
			result = p.Eval.EvaluateWith(output.Expansion.Parts[0].Expr, &context)
		} else {
			result = p.renderOutput(output, &context, &patterns)
		}
		if result.Waiting || result.Completed || result.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 {
			d = p.outputDiagnostic(output.Span, "PHASE_INVALID", "rule outputs must resolve without host requests or effects")
		} else if result.Diagnostic.Code != "" {
			d = p.outputDiagnostic(output.Span, result.Diagnostic.Code, result.Diagnostic.Message)
		} else {
			d = p.appendOutput(result.Value, output.Span, &outputs)
			if d.Code == "" && len(patterns) != 0 {
				last := &outputs[len(outputs)-1]
				last.TargetForm = template.ParseMixedTarget(p.Alloc, p.Parsed.Source.Name, last.Text, patterns)
			}
		}
		mem.FreeSlice(nil, patterns)
		result.Free(p.Alloc)
		if d.Code == "PHASE_INVALID" || d.Code == "CAP_DENIED" {
			p.registrationTip(&d)
		}
		if d.Code != "" {
			break
		}
	}
	eval.FreeEffects(p.Alloc, context.Effects)
	freeStrings(p.Alloc, state.Stack)
	freeStrings(p.Alloc, state.Dependencies)
	if d.Code == "" && len(outputs) == 0 {
		d = p.outputDiagnostic(authored.Header, "EXPR_INVALID", "rule output set is empty")
	}
	if d.Code != "" {
		for i := range outputs {
			mem.FreeString(p.Alloc, outputs[i].Text)
			outputs[i].TargetForm.Free()
		}
		mem.FreeSlice(nil, outputs)
		return outputResolution{Diagnostic: d}
	}
	part := rule.ResolveOutputs(p.Alloc, p.Parsed.Source, authored, outputs)
	resolved := part.Rule
	mem.FreeSlice(nil, outputs)
	p.ResolvedRules = append(p.ResolvedRules, resolved)
	if len(part.Diagnostics) != 0 {
		first := part.Diagnostics[0]
		d = p.outputDiagnostic(first.Span, "PARSE_ERR", first.Message)
	}
	mem.FreeSlice(nil, part.Diagnostics)
	if d.Code != "" {
		return outputResolution{Diagnostic: d}
	}
	return outputResolution{Rule: resolved}
}
