// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

const defEscapeMessage = "function value escapes its scope; define it with (def name [params] body) instead"

// rejectStrandedCapture discards a value that would strand its capture and
// reports DEF_ESCAPE. The value is freed deeply so the reject path stays
// Tracker-clean by construction.
func rejectStrandedCapture(a mem.Allocator, value *core.Value, span source.Span) Result {
	freeCallables(a, value)
	value.Free(a)
	return failure(a, "DEF_ESCAPE", span, defEscapeMessage)
}

func (p *Program) body(scope *Scope, body []*expr.Expr, context *Context) Result {
	result := Result{Value: core.Value{Kind: core.Nil}}
	for i := range body {
		// Discard: intermediates never escape, so release callables as well
		// as storage. Core Value.Free is callable-blind by design.
		freeCallables(context.Run, &result.Value)
		result.Value.Free(context.Run)
		result = p.evaluate(context.Engine, scope, body[i], context)
		if result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
	}
	return result
}

func (p *Program) fallback(scope *Scope, values []*expr.Expr, context *Context) Result {
	for i := range values {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "REF_MISSING" {
			return r
		}
		r.Diagnostic.Free(context.Run)
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

func (p *Program) let(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) == 0 || values[0].Kind != expr.List || len(values[0].Items)%2 != 0 {
		return failure(context.Run, "EXPR_INVALID", span, "let needs name/value pairs")
	}
	child := newScope(context.Run, scope)
	defer child.Free()
	for i := 0; i < len(values[0].Items); i += 2 {
		name := values[0].Items[i]
		if name.Kind != expr.Name {
			return failure(context.Run, "DEF_INVALID", name.Span, "let binding needs a name")
		}
		r := p.evaluate(context.Engine, child, values[0].Items[i+1], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if !child.trySetValue(name.Text, r.Value) {
			return rejectStrandedCapture(context.Run, &r.Value, values[0].Items[i+1].Span)
		}
		r.Value.Free(context.Run)
	}
	return p.body(child, values[1:], context)
}

func (p *Program) def(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 2 || values[0].Kind != expr.Name {
		return failure(context.Run, "DEF_INVALID", span, "def needs a name and value")
	}
	if len(values) >= 3 && values[1].Kind == expr.List {
		parameters := slices.Make[expr.Parameter](context.Run, len(values[1].Items))
		for i := range values[1].Items {
			parameter := values[1].Items[i]
			if parameter.Kind != expr.Name || (parameter.Rest && i != len(values[1].Items)-1) {
				for j := 0; j < i; j++ {
					mem.FreeString(context.Run, parameters[j].Name)
				}
				slices.Free(context.Run, parameters)
				return failure(context.Run, "DEF_INVALID", parameter.Span, "invalid function parameter")
			}
			for j := 0; j < i; j++ {
				if parameters[j].Name == parameter.Text {
					for j := 0; j < i; j++ {
						mem.FreeString(context.Run, parameters[j].Name)
					}
					slices.Free(context.Run, parameters)
					return failure(context.Run, "DEF_INVALID", parameter.Span, "duplicate function parameter")
				}
			}
			parameters[i] = expr.Parameter{Name: owned(context.Run, parameter.Text), Span: parameter.Span, Rest: parameter.Rest}
		}
		function := mem.Alloc[Function](context.Run)
		for i := 2; i < len(values); i++ {
			function.Body = slices.Append(context.Run, function.Body, expr.Clone(context.Run, values[i]))
		}
		function.Kind, function.Parameters, function.Scope, function.ParametersOwned, function.ParameterNamesOwned, function.BodyOwned = FunctionDefinition, parameters, scope, true, true, true
		scope.setFunction(values[0].Text, function)
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	if len(values) != 2 {
		return failure(context.Run, "DEF_INVALID", span, "invalid def form")
	}
	r := p.evaluate(context.Engine, scope, values[1], context)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if !scope.trySetValue(values[0].Text, r.Value) {
		return rejectStrandedCapture(context.Run, &r.Value, values[1].Span)
	}
	// Transfer: the scope clones the value, so relinquish the original and
	// read the binding back. Returning r directly would share one callable
	// wrapper between the scope and the result (double free on Result.Free);
	// the fresh lookup owns an independent clone (or borrowed wrapper).
	r.Value.Free(context.Run)
	return name(scope, values[0].Text, values[0].Span, context)
}

func (p *Program) evalText(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) != 1 {
		return failure(context.Run, "EXPR_INVALID", span, "eval needs one string")
	}
	r := p.evaluate(context.Engine, scope, values[0], context)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.String {
		freeCallables(context.Run, &r.Value)
		r.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", span, "eval needs text")
	}
	parsed := expr.Parse(context.Run, "<eval>", r.Value.Text)
	r.Value.Free(context.Run)
	if len(parsed.Diagnostics) != 0 {
		parseDiagnostic := parsed.Diagnostics[0]
		parsed.Free()
		return Result{Diagnostic: diagnostic.Diagnostic{Code: parseDiagnostic.Code, Severity: diagnosticSeverity(parseDiagnostic.Severity), Message: parseDiagnostic.Message, Span: diagnostic.Span{Start: parseDiagnostic.Span.Start, End: parseDiagnostic.Span.End}}}
	}
	result := p.evaluate(context.Engine, scope, parsed.Expr, context)
	parsed.Free()
	return result
}

func truthValue(v core.Value) bool { return v.Kind != core.Nil && !(v.Kind == core.Bool && !v.Bool) }

func (p *Program) conditional(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 2 {
		return failure(context.Run, "EXPR_INVALID", span, "if needs a test and a body")
	}
	hasElse := len(values)%2 == 1
	pairs := len(values)
	if hasElse {
		pairs--
	}
	for i := 0; i < pairs; i += 2 {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		cond := truthValue(r.Value)
		freeCallables(context.Run, &r.Value)
		r.Value.Free(context.Run)
		if cond {
			return p.evaluate(context.Engine, scope, values[i+1], context)
		}
	}
	if hasElse {
		return p.evaluate(context.Engine, scope, values[len(values)-1], context)
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

func (p *Program) logicalAnd(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	_ = span
	if len(values) == 0 {
		return Result{Value: core.Value{Kind: core.Bool, Bool: true}}
	}
	for i := range values {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if !truthValue(r.Value) {
			return r
		}
		if i == len(values)-1 {
			return r
		}
		freeCallables(context.Run, &r.Value)
		r.Value.Free(context.Run)
	}
	return Result{Value: core.Value{Kind: core.Bool, Bool: true}}
}

func (p *Program) logicalOr(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	_ = span
	if len(values) == 0 {
		return Result{Value: core.Value{Kind: core.Bool, Bool: false}}
	}
	for i := range values {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if truthValue(r.Value) {
			return r
		}
		if i == len(values)-1 {
			return r
		}
		freeCallables(context.Run, &r.Value)
		r.Value.Free(context.Run)
	}
	return Result{Value: core.Value{Kind: core.Bool, Bool: false}}
}

func (p *Program) scopedWith(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 1 {
		return failure(context.Run, "EXPR_INVALID", span, "with needs a record")
	}
	r := p.evaluate(context.Engine, scope, values[0], context)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.Record {
		freeCallables(context.Run, &r.Value)
		r.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", values[0].Span, "with needs a record")
	}
	child := newScope(context.Run, scope)
	defer child.Free()
	for i := range r.Value.Record {
		key := r.Value.Record[i].Key
		if !child.trySetValue(key, r.Value.Record[i].Value) {
			reject := rejectStrandedCapture(context.Run, &r.Value, values[0].Span)
			return reject
		}
	}
	freeCallables(context.Run, &r.Value)
	r.Value.Free(context.Run)
	if len(values) == 1 {
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	return p.body(child, values[1:], context)
}

func (p *Program) patternMatch(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 1 {
		return failure(context.Run, "EXPR_INVALID", span, "match needs a subject")
	}
	sub := p.evaluate(context.Engine, scope, values[0], context)
	if sub.Waiting || sub.Diagnostic.Code != "" {
		return sub
	}
	var subjectText string
	switch sub.Value.Kind {
	case core.String, core.Pattern:
		subjectText = sub.Value.Text
	case core.Bytes:
		freeCallables(context.Run, &sub.Value)
		sub.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", values[0].Span, "match subject must be text; convert bytes with text first")
	default:
		freeCallables(context.Run, &sub.Value)
		sub.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", values[0].Span, "match subject must be text")
	}
	// Scan clauses without evaluating bodies. Only the selected body runs.
	var elseBody []*expr.Expr
	var elseFound bool
	for ci := 1; ci < len(values); ci++ {
		clause := values[ci]
		if clause.Kind != expr.List || len(clause.Items) == 0 {
			freeCallables(context.Run, &sub.Value)
			sub.Value.Free(context.Run)
			return failure(context.Run, "EXPR_INVALID", clause.Span, "match clause needs [pattern body...]")
		}
		head := clause.Items[0]
		if head.Kind == expr.Symbol && head.Text == "else" {
			if ci != len(values)-1 {
				freeCallables(context.Run, &sub.Value)
				sub.Value.Free(context.Run)
				return failure(context.Run, "EXPR_INVALID", clause.Span, ":else must be the last match clause")
			}
			elseBody = clause.Items[1:]
			elseFound = true
			continue
		}
		if elseFound {
			freeCallables(context.Run, &sub.Value)
			sub.Value.Free(context.Run)
			return failure(context.Run, "EXPR_INVALID", clause.Span, ":else must be the last match clause")
		}
		pr := p.evaluate(context.Engine, scope, head, context)
		if pr.Waiting || pr.Diagnostic.Code != "" {
			freeCallables(context.Run, &sub.Value)
			sub.Value.Free(context.Run)
			return pr
		}
		mout := matchPattern(context.Run, subjectText, &pr.Value, head.Span)
		matched, names, captures := mout.Matched, mout.Names, mout.Captures
		diag := mout.Diag
		freeCallables(context.Run, &pr.Value)
		pr.Value.Free(context.Run)
		if diag.Code != "" {
			for i := range names {
				mem.FreeString(context.Run, names[i])
			}
			slices.Free(context.Run, names)
			for i := range captures {
				mem.FreeString(context.Run, captures[i])
			}
			slices.Free(context.Run, captures)
			freeCallables(context.Run, &sub.Value)
			sub.Value.Free(context.Run)
			return Result{Diagnostic: diag}
		}
		if !matched {
			for i := range captures {
				mem.FreeString(context.Run, captures[i])
			}
			slices.Free(context.Run, captures)
			for i := range names {
				mem.FreeString(context.Run, names[i])
			}
			slices.Free(context.Run, names)
			continue
		}
		armScope := newScope(context.Run, scope)
		for i := range names {
			if names[i] != "" {
				v := core.NewString(context.Run, captures[i])
				if !armScope.trySetValue(names[i], v) {
					v.Free(context.Run)
					for j := range names {
						mem.FreeString(context.Run, names[j])
					}
					slices.Free(context.Run, names)
					for j := range captures {
						mem.FreeString(context.Run, captures[j])
					}
					slices.Free(context.Run, captures)
					freeCallables(context.Run, &sub.Value)
					sub.Value.Free(context.Run)
					armScope.Free()
					return failure(context.Run, "DEF_INVALID", clause.Span, "match capture escapes its scope")
				}
				v.Free(context.Run)
			}
		}
		for i := range names {
			mem.FreeString(context.Run, names[i])
		}
		slices.Free(context.Run, names)
		for i := range captures {
			mem.FreeString(context.Run, captures[i])
		}
		slices.Free(context.Run, captures)
		freeCallables(context.Run, &sub.Value)
		sub.Value.Free(context.Run)
		if len(clause.Items) == 1 {
			armScope.Free()
			return Result{Value: core.Value{Kind: core.Nil}}
		}
		armResult := p.body(armScope, clause.Items[1:], context)
		armScope.Free()
		return armResult
	}
	freeCallables(context.Run, &sub.Value)
	sub.Value.Free(context.Run)
	if elseFound {
		if len(elseBody) == 0 {
			return Result{Value: core.Value{Kind: core.Nil}}
		}
		elseScope := newScope(context.Run, scope)
		elseResult := p.body(elseScope, elseBody, context)
		elseScope.Free()
		return elseResult
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

// matchOutcome carries a pattern match plus owned capture names and texts.
type matchOutcome struct {
	Matched  bool
	Names    []string
	Captures []string
	Diag     diagnostic.Diagnostic
}

// matchPattern tests subject against a pattern value. Misuse is reported in
// Diag with PAT_INVALID / EXPR_INVALID codes.
func matchPattern(a mem.Allocator, subject string, pattern *core.Value, span source.Span) matchOutcome {
	switch pattern.Kind {
	case core.String:
		if pattern.Text == subject {
			return matchOutcome{Matched: true}
		}
		return matchOutcome{}
	case core.Pattern:
		parsed := expr.ParsePatternText(a, pattern.Text, 0)
		if len(parsed.Diagnostics) != 0 {
			parsed.Pattern.Free(a)
			slices.Free(a, parsed.Diagnostics)
			return matchOutcome{Diag: diagnostic.Diagnostic{Code: cloneFailureText(a, "PAT_INVALID"), Severity: diagnostic.Error, Message: cloneFailureText(a, "invalid match pattern"), Span: diagnostic.Span{Start: span.Start, End: span.End}, Owned: true}}
		}
		slices.Free(a, parsed.Diagnostics)
		if parsed.Pattern.References != 0 {
			parsed.Pattern.Free(a)
			return matchOutcome{Diag: diagnostic.Diagnostic{Code: cloneFailureText(a, "PAT_INVALID"), Severity: diagnostic.Error, Message: cloneFailureText(a, "match pattern must not be an expansion pattern"), Span: diagnostic.Span{Start: span.Start, End: span.End}, Owned: true}}
		}
		if parsed.Pattern.Matchers == 0 {
			// No groups: exact text match on canonical form.
			matched := parsed.Pattern.Parts != nil && len(parsed.Pattern.Parts) == 1 && parsed.Pattern.Parts[0].Kind == expr.PatternLiteral && parsed.Pattern.Parts[0].Text == subject
			if len(parsed.Pattern.Parts) == 0 && subject == "" {
				matched = true
			}
			parsed.Pattern.Free(a)
			if matched {
				return matchOutcome{Matched: true}
			}
			return matchOutcome{}
		}
		res := parsed.Pattern.MatchText(a, subject)
		var names []string
		for i := range parsed.Pattern.Names {
			names = slices.Append(a, names, owned(a, parsed.Pattern.Names[i]))
		}
		parsed.Pattern.Free(a)
		if !res.Matched {
			slices.Free(a, res.Captures)
			for i := range names {
				mem.FreeString(a, names[i])
			}
			slices.Free(a, names)
			return matchOutcome{}
		}
		var captures []string
		for i := range res.Captures {
			captures = slices.Append(a, captures, owned(a, res.Captures[i]))
		}
		slices.Free(a, res.Captures)
		// Normalize lengths: anonymous matchers still occupy slots.
		for len(captures) < len(names) {
			captures = slices.Append(a, captures, owned(a, ""))
		}
		return matchOutcome{Matched: true, Names: names, Captures: captures}
	default:
		return matchOutcome{Diag: diagnostic.Diagnostic{Code: cloneFailureText(a, "EXPR_INVALID"), Severity: diagnostic.Error, Message: cloneFailureText(a, "match pattern must be text"), Span: diagnostic.Span{Start: span.Start, End: span.End}, Owned: true}}
	}
}
