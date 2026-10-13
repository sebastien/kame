package script_test

import (
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestConditionalDeclarationsKeepPredicatesRulesAndIncludes(t *testing.T) {
	a := t.Allocator()
	text := "(if (eq mode \"debug\") (include \"debug.kmk\") (if :false (include? ./absent.kmk) :nil))\nbuild :\n\techo debug\n"
	parsed := script.Parse(a, "conditions.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
		t.Fatal("conditional declarations did not parse")
		return
	}
	if parsed.Items[0].Kind != script.Composition || parsed.Items[0].Expression == nil || parsed.Items[1].Kind != script.Rule {
		t.Error("conditional composition or rule metadata was lost")
	}
	leaf := script.CompositionInclude(parsed.Items[0].Expression.Items[3].Items[2])
	if !leaf.OK || !leaf.Optional || leaf.Path != "./absent.kmk" {
		t.Error("nested optional include metadata was lost")
	}
	formatted := script.Format(a, parsed)
	defer mem.FreeString(a, formatted)
	again := script.Parse(a, "formatted.kmk", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || len(again.Items) != len(parsed.Items) {
		t.Error("formatted conditionals changed structure")
	}
}

func TestConditionalDeclarationsRejectLegacyMarkers(t *testing.T) {
	a := t.Allocator()
	cases := []string{"when\nend\n", " when :true\nend\n", "when :true\nx = 1\n", "otherwise\n", "end\n", "when :true\notherwise\notherwise\nend\n", "when :true :false\nend\n"}
	for i := range cases {
		parsed := script.Parse(a, "bad.kmk", cases[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid conditional structure was accepted")
		}
		parsed.Free()
	}
}

func TestValueFragmentsPreserveConditionalComposition(t *testing.T) {
	a := t.Allocator()
	authored := source.New(a, "conditions.km", "(if :true (include ./answer.km) (include ./zero.km))\n")
	defer authored.Free(a)
	parsed := script.ParseFragment(a, authored, "km", 0, len(authored.Text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 || parsed.Items[0].Kind != script.Composition {
		t.Error("value fragment conditionals were not preserved")
	}
}
