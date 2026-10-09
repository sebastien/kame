package definition_test

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestDefinitionClassifiesExpressionAndSpans(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "build = [name: \"app\"]")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition == nil || result.Definition.Expression.Kind != expr.Record {
		t.Error("definition did not classify record RHS as an expression")
		return
	}
	if !sameSpan(result.Definition.NameSpan, 0, 5) || !sameSpan(result.Definition.Expression.Span, 8, 21) {
		t.Error("definition name or expression span was not retained")
	}
}

func TestFunctionDefinitionRetainsRestParameter(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "(copy source rest...) = output")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition == nil || result.Definition.Name != "copy" || len(result.Definition.Parameters) != 2 || !result.Definition.Parameters[1].Rest {
		t.Error("function definition did not retain rest parameter")
	}
}

func TestRangeKeepsCallerSourceAndInterpolatedString(t *testing.T) {
	s := source.New(t.Allocator(), "test.km", "prefix\nvalue = \"hello @(name)\"")
	defer s.Free(t.Allocator())
	part := definition.ParseRange(t.Allocator(), s, 7, len(s.Text))
	defer part.Free(t.Allocator())
	d := part.Definition
	if len(part.Diagnostics) != 0 || d == nil || d.Expression.Kind != expr.String || len(d.Expression.Parts) != 2 || d.Expression.Parts[1].Expr == nil || !sameSpan(d.Expression.Parts[1].Expr.Span, 24, 28) {
		t.Error("quoted RHS did not retain a positioned expression string")
	}
}

func TestFormatDefinitionCanonicalizesWhitespace(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "value   =   [\"words\"  name]")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition.Expression.Kind != expr.List || result.Definition.Expression.Items[1].Kind != expr.Name {
		t.Error("RHS did not parse list expressions")
		return
	}
	formatted := definition.Format(t.Allocator(), result.Definition)
	if formatted != "value = [\"words\" name]" {
		t.Errorf("Format() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
}

func TestDefinitionAtomsUseExpressionGrammar(t *testing.T) {
	texts := []string{"alias = other", "number = 42", "flag = :false", "nothing = :nil", "path = ./file", "shell = kash", "(identity value) = value"}
	kinds := []expr.Kind{expr.Name, expr.Integer, expr.Boolean, expr.Nil, expr.Path, expr.Name, expr.Name}
	for i := range texts {
		result := definition.Parse(t.Allocator(), "test.km", texts[i])
		if len(result.Diagnostics) != 0 || result.Definition == nil || result.Definition.Expression == nil || result.Definition.Expression.Kind != kinds[i] {
			t.Error("definition did not parse expression atom: " + texts[i])
		}
		result.Free()
	}
}

func TestDefinitionRejectsTextAndMultipleExpressions(t *testing.T) {
	texts := []string{"value = one two", "value = \"one\" \"two\"", "value = -Wall -Wextra", "value = [", "value =", "value = (broken"}
	for i := range texts {
		result := definition.Parse(t.Allocator(), "test.km", texts[i])
		if len(result.Diagnostics) == 0 {
			t.Error("invalid RHS fell back to text: " + texts[i])
		}
		result.Free()
	}
}

func TestDefinitionRangeBoundsExpressionAndAllowsTrailingContinuation(t *testing.T) {
	a := t.Allocator()
	s := source.New(a, "test.km", "value = 42ignored")
	part := definition.ParseRange(a, s, 0, 10)
	if len(part.Diagnostics) != 0 || part.Definition == nil || part.Definition.Expression.Kind != expr.Integer {
		t.Error("definition expression crossed its source range")
	}
	part.Free(a)
	s.Free(a)
	result := definition.Parse(a, "test.km", "value = 42 \\\n  ")
	if len(result.Diagnostics) != 0 {
		t.Error("trailing continuation became extra expression data")
	}
	result.Free()
}

func TestDefaultDefinitionPreservesSpansAndFormatting(t *testing.T) {
	a := t.Allocator()
	result := definition.Parse(a, "test.km", "value ?= words")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition == nil || !result.Definition.Default || !sameSpan(result.Definition.NameSpan, 0, 5) {
		t.Error("default definition lost name/span")
		return
	}
	formatted := definition.Format(a, result.Definition)
	if formatted != "value ?= words" {
		t.Error("default definition formatting lost operator")
	}
	mem.FreeString(a, formatted)
}

func TestQuestionMarkNameIsNotDefaultOperator(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "name? = \"name?\"")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition == nil || result.Definition.Default || result.Definition.Name != "name?" {
		t.Error("question-mark name became a default")
	}
}

func TestContributionDefinitionPreservesSpansAndFormatting(t *testing.T) {
	a := t.Allocator()
	result := definition.Parse(a, "test.km", "items += [1 2]")
	defer result.Free()
	d := result.Definition
	if len(result.Diagnostics) != 0 || d == nil || !d.Contribution || d.Default || !sameSpan(d.NameSpan, 0, 5) || !sameSpan(d.Expression.Span, 9, 14) {
		t.Fatal("contribution lost operator or spans")
	}
	formatted := definition.Format(a, d)
	if formatted != "items += [1 2]" {
		t.Error("contribution formatting lost operator")
	}
	mem.FreeString(a, formatted)
	invalid := definition.Parse(a, "test.km", "(items x) += [x]")
	if len(invalid.Diagnostics) == 0 || invalid.Diagnostics[0].Code != "PARSE_ERR" {
		t.Error("function contribution must fail during parsing")
	}
	invalid.Free()
}
