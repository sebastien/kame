package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/script"
	"testing"
)

func TestDefinitionRHSUsesValueExpressionGrammar(t *testing.T) {
	const input = "value = f 1 2 +\n    3\n" +
		"selected = match value\n    3 => true\n    else => false\n" +
		"next = 4\n"
	parsed := script.Parse(nil, "definitions.km", input)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 {
		t.Fatalf("value expressions consumed the wrong definition range: %v", parsed.Diagnostics)
	}
	value := parsed.Items[0].Definition.Expression
	if value.Infix != "+" || value.Items[1].Kind != expr.Application || !value.Items[1].Juxtaposed {
		t.Fatal("definition did not keep juxtaposition and operator precedence")
	}
	if match := parsed.Items[1].Definition.Expression; match.Kind != expr.Match || len(match.Items) != 2 {
		t.Fatal("definition did not keep its match arms")
	}
	if next := parsed.Items[2].Definition; next.Name != "next" || next.Expression.Int != 4 {
		t.Fatal("match consumed a following definition")
	}
}
