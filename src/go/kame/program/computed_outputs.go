package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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
	slices.Free(a, r.Outputs)
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
	*outputs = slices.Append(p.Alloc, *outputs, rule.Target{Text: cloneText(p.Alloc, value.Text), Span: span})
	return diagnostic.Diagnostic{}
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
			outputs = slices.Append(p.Alloc, outputs, rule.Target{Text: cloneText(p.Alloc, output.Text), Span: output.Span})
			continue
		}
		var result eval.Result
		if output.Kind == rule.TargetExpression {
			result = p.Eval.EvaluateWith(output.Expansion.Parts[0].Expr, &context)
		} else {
			result = p.Eval.Render(p.Alloc, output.Expansion, p.Eval.Scope, &context)
		}
		if result.Waiting || result.Completed || result.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 {
			d = p.outputDiagnostic(output.Span, "PHASE_INVALID", "rule outputs must resolve without host requests or effects")
		} else if result.Diagnostic.Code != "" {
			d = p.outputDiagnostic(output.Span, result.Diagnostic.Code, result.Diagnostic.Message)
		} else {
			d = p.appendOutput(result.Value, output.Span, &outputs)
		}
		result.Free(p.Alloc)
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
		}
		slices.Free(p.Alloc, outputs)
		return outputResolution{Diagnostic: d}
	}
	part := rule.ResolveOutputs(p.Alloc, p.Parsed.Source, authored, outputs)
	resolved := part.Rule
	slices.Free(p.Alloc, outputs)
	p.ResolvedRules = slices.Append(p.Alloc, p.ResolvedRules, resolved)
	if len(part.Diagnostics) != 0 {
		first := part.Diagnostics[0]
		d = p.outputDiagnostic(first.Span, "PARSE_ERR", first.Message)
	}
	slices.Free(p.Alloc, part.Diagnostics)
	if d.Code != "" {
		return outputResolution{Diagnostic: d}
	}
	return outputResolution{Rule: resolved}
}
