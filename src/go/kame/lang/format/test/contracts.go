package format_test

import (
	"kame/lang/format"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestValueFormatterSharesFragmentGrammarAndComments(t *testing.T) {
	a := t.Allocator()
	text := "# before\n(f) = 1\n// between\n  (f)\n# after\n"
	authored := source.New(a, "values.km", text)
	defer authored.Free(a)
	parsed := script.ParseFragment(a, authored, "km", 0, len(text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 5 || parsed.Items[2].Kind != script.Comment || parsed.Items[2].Span.Start != len("# before\n(f) = 1\n") {
		t.Fatal("value fragment lost comment order or authored spans")
		return
	}
	formatted := format.Source(a, "km", "values.km", text, "tabs", 4)
	defer mem.FreeString(a, formatted.Text)
	defer mem.FreeString(a, formatted.Code)
	defer mem.FreeString(a, formatted.Message)
	if !formatted.OK || formatted.Text != "# before\n(f) = 1\n// between\n(f)\n# after\n" {
		t.Error("value formatter differs from fragment grammar or drops comments")
	}
	languages := []string{"km", "kmk", "kash"}
	for _, lang := range languages {
		result := format.Source(a, lang, "independent", "build : dep\n\techo ok\n", "tabs", 4)
		if (lang == "km" && result.OK) || (lang == "kmk" && !result.OK) {
			t.Error("value/rule parser boundary changed")
		}
		mem.FreeString(a, result.Text)
		mem.FreeString(a, result.Code)
		mem.FreeString(a, result.Message)
	}
}
