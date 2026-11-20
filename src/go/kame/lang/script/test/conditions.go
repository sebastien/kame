package scriptcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/script"
	"kame/lang/source"
)

func TestConditionalDeclarationsKeepPredicatesRulesAndIncludes(t *testing.T) {
	a := t.Allocator()
	text := "if mode == \"debug\"\n\tinclude ./debug.km\nelse\n\tif false\n\t\tinclude? ./absent.km\ntask build\n\techo debug\n"
	parsed := script.ParseKameFile(a, "conditions.km", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
		t.Fatal("conditional declarations did not parse")
		return
	}
	if parsed.Items[0].Kind != script.Composition || len(parsed.Items[0].Branches) != 2 || parsed.Items[1].Kind != script.Rule {
		t.Error("conditional composition or rule metadata was lost")
	}
	leaf := parsed.Items[0].Branches[1].Items[0].Branches[0].Items[0]
	if leaf.Kind != script.Include || !leaf.OptionalInclude || leaf.Include != "./absent.km" {
		t.Error("nested optional include metadata was lost")
	}
	formatted := script.Format(a, parsed)
	defer mem.FreeString(a, formatted)
	again := script.ParseKameFile(a, "formatted.km", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || len(again.Items) != len(parsed.Items) {
		t.Error("formatted conditionals changed structure")
	}
}

func TestConditionalDeclarationsRejectLegacyMarkers(t *testing.T) {
	a := t.Allocator()
	cases := []string{"when\nend\n", " when true\nend\n", "when true\nx = 1\n", "otherwise\n", "end\n", "when true\notherwise\notherwise\nend\n", "when true false\nend\n"}
	for i := range cases {
		parsed := script.ParseKameFile(a, "bad.km", cases[i])
		if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Error("invalid conditional structure did not report a parse error")
		}
		parsed.Free()
	}
	parsed := script.ParseKameFile(a, "definitions.km", "when = true\notherwise = false\nend = nil\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 {
		t.Error("ordinary definitions with these names did not parse")
	}
}

func TestValueFragmentsPreserveConditionalComposition(t *testing.T) {
	a := t.Allocator()
	authored := source.New(a, "conditions.km", "if true\n  include ./answer.km\nelse\n  include ./zero.km\n")
	defer authored.Free(a)
	parsed := script.ParseFragment(a, authored, "km", 0, len(authored.Text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 || parsed.Items[0].Kind != script.Composition {
		t.Error("value fragment conditionals were not preserved")
	}
}
