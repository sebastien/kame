package script_test

import (
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestConditionalDeclarationsKeepPredicatesRulesAndIncludes(t *testing.T) {
	a := t.Allocator()
	text := "when (eq mode \"debug\")\ninclude \"debug.kmk\"\nbuild :\n\techo debug\notherwise\nwhen :false\ninclude? absent.kmk\nend\nend\n"
	parsed := script.Parse(a, "conditions.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 8 {
		t.Fatal("conditional declarations did not parse")
		return
	}
	if parsed.Items[0].Kind != script.When || parsed.Items[0].Expression == nil || parsed.Items[3].Kind != script.Otherwise || parsed.Items[5].Kind != script.Include || !parsed.Items[5].OptionalInclude || parsed.Items[7].Kind != script.EndWhen {
		t.Error("conditional marker metadata was lost")
	}
	formatted := script.Format(a, parsed)
	defer mem.FreeString(a, formatted)
	again := script.Parse(a, "formatted.kmk", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || len(again.Items) != len(parsed.Items) {
		t.Error("formatted conditionals changed structure")
	}
}

func TestConditionalDeclarationsRequireBalancedMarkers(t *testing.T) {
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

func TestValueFragmentsPreserveConditionalDeclarationMarkers(t *testing.T) {
	a := t.Allocator()
	authored := source.New(a, "conditions.km", "when :true\nvalue = 42\notherwise\nvalue = 0\nend\n")
	defer authored.Free(a)
	parsed := script.ParseFragment(a, authored, "km", 0, len(authored.Text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 5 || parsed.Items[0].Kind != script.When || parsed.Items[2].Kind != script.Otherwise || parsed.Items[4].Kind != script.EndWhen {
		t.Error("value fragment conditionals were not preserved")
	}
}
