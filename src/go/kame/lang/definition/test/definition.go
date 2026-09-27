package definition_test

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestDefinitionClassifiesExpressionAndSpans(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "build = [name: \"app\"]")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Definition == nil || result.Definition.ValueKind != definition.ValueExpression || result.Definition.Expression.Kind != expr.Record {
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

func TestRangeKeepsCallerSourceAndQuotedTemplate(t *testing.T) {
	s := source.New(t.Allocator(), "test.km", "prefix\nvalue = \"hello @(name)\"")
	defer s.Free(t.Allocator())
	part := definition.ParseRange(t.Allocator(), s, 7, len(s.Text))
	defer part.Free(t.Allocator())
	d := part.Definition
	if len(part.Diagnostics) != 0 || d == nil || d.ValueKind != definition.ValueTemplate || len(d.Template.Parts) != 2 || d.Template.Parts[1].Kind != template.Expression || !sameSpan(d.Template.Parts[1].Span, 22, 29) {
		t.Error("quoted RHS did not retain a positioned template")
	}
}

func TestFormatDefinitionCanonicalizesWhitespace(t *testing.T) {
	result := definition.Parse(t.Allocator(), "test.km", "value   =   words  @(name)")
	defer result.Free()
	if result.Definition.ValueKind != definition.ValueWords || result.Definition.Words[1].Template.Parts[0].Kind != template.Expression {
		t.Error("unquoted RHS did not parse template words")
		return
	}
	formatted := definition.Format(t.Allocator(), result.Definition)
	if formatted != "value = words @(name)" {
		t.Errorf("Format() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
}
