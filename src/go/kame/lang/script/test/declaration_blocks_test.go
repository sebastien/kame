package scriptcheck

import (
	"bytes"
	"kame/lang/ast"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"strings"
	"testing"
)

func TestDeclarationLoopBindingsAndGenerationRoundTrip(t *testing.T) {
	const input = "for variant in [\"debug\",\"release\"] # variants\n  let flags = if variant == \"debug\" then [\"-g\"] else [] # choose\n  if variant == \"debug\"\n    generate (make-rules variant flags)\n  task check\ngenerate rules # register\n"
	const want = "for variant in [\"debug\", \"release\"] # variants\n\tlet flags = if variant == \"debug\" then [\"-g\"] else [] # choose\n\tif variant == \"debug\"\n\t\tgenerate (make-rules variant flags)\n\ttask check\ngenerate rules # register\n"
	for _, text := range []string{input, want} {
		authored := source.New(nil, "program.km", text)
		defer authored.Free(nil)
		parsed := script.ParseFragment(nil, authored, script.KameFileLanguage, 0, len(text))
		defer parsed.Free()
		if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
			t.Fatalf("declaration block failed to parse: %v", parsed.Diagnostics)
		}
		loop := parsed.Items[0]
		if loop.Kind != script.DeclarationFor || loop.Expression.Kind != expr.List || loop.Loop.Name != "variant" || len(loop.Loop.Items) != 3 {
			t.Fatalf("incorrect declaration loop: %+v", loop)
		}
		binding := loop.Loop.Items[0]
		generate := loop.Loop.Items[1].Branches[0].Items[0]
		if binding.Kind != script.DeclarationLet || binding.Definition.Name != "flags" || binding.Definition.Expression.Kind != expr.Conditional ||
			generate.Kind != script.Generate || expr.Ungroup(generate.Expression).Kind != expr.Application || parsed.Items[1].Kind != script.Generate {
			t.Fatalf("incorrect binding or generation: %+v %+v", binding, generate)
		}
		if formatted := script.Format(nil, parsed); formatted != want {
			t.Fatalf("formatted as %q, want %q", formatted, want)
		}
	}
	var encoded bytes.Buffer
	if ast.WriteAST(&encoded, script.KameFileLanguage, "program.km", input) != 0 ||
		!strings.Contains(encoded.String(), `"binding":"variant"`) || !strings.Contains(encoded.String(), `"kind":"let"`) ||
		!strings.Contains(encoded.String(), `"kind":"generate"`) {
		t.Fatalf("declaration AST lost structure: %s", encoded.String())
	}
}

func TestDeclarationBlocksRejectActionStatementsAndMalformedHeaders(t *testing.T) {
	for _, text := range []string{
		"for value from values\n  task check\n",
		"for true in values\n  task check\n",
		"for value in\n  task check\n",
		"for value in values\n",
		"for value in values\n  echo forbidden\n",
		"for value in values\n  set value = 1\n",
		"for value in values\n  let option ?= 1\n",
		"for value in values\n  let option =\n  task check\n",
		"  let value = 1\n",
		"generate\n",
		"generate named = rules\n",
	} {
		authored := source.New(nil, "invalid.km", text)
		parsed := script.ParseFragment(nil, authored, script.KameFileLanguage, 0, len(text))
		if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("accepted invalid declaration syntax %q: %v", text, parsed.Diagnostics)
		}
		parsed.Free()
		authored.Free(nil)
	}
}
