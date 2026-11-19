package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/script"
	"testing"
)

func TestKashRuleBodyReturnUsesValueGrammar(t *testing.T) {
	const text = "def inner ()\n\treturn 0\nif true\n\treturn \"one\" + \"two\"\n"
	parsed := script.ParseKashRuleBody(nil, "recipe", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
		t.Fatalf("valid rule body was rejected: %v", parsed.Diagnostics)
	}
	functionReturn := parsed.Items[0].Definition.Expression.Body[0]
	ruleReturn := parsed.Items[1].Expression.Items[0].Body[0]
	if functionReturn.Kind != expr.KashReturn || ruleReturn.Kind != expr.ValueReturn || ruleReturn.Items[0].Infix != "+" {
		t.Fatalf("wrong return contexts: %v and %v", functionReturn.Kind, ruleReturn.Kind)
	}
	if actual := script.Format(nil, parsed); actual != text {
		t.Fatalf("rule body formatted as %q", actual)
	}
	for _, invalid := range []string{"return 0 ?", "yield 1", "include ./source.kash", "import ./source.kash as source", "alias greeting = (out! 42)"} {
		parsed := script.ParseKashRuleBody(nil, "recipe", invalid)
		if len(parsed.Diagnostics) == 0 {
			t.Errorf("rule body accepted %q", invalid)
		}
		parsed.Free()
	}
}
