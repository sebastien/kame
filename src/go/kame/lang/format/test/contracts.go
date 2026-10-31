package format_test

import (
	"kame/lang/format"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestKashGraphFormattingCanonicalizesOperatorStages(t *testing.T) {
	a := t.Allocator()
	// An implicit-stage command follows its operator directly: the `|` an author
	// may write there is not structural, so both spellings format identically.
	piped := format.Source(a, "kash", "each.kash", "printf a |(frame :lines) |(each) | cat\n", "tabs", 4)
	defer mem.FreeString(a, piped.Text)
	defer mem.FreeString(a, piped.Code)
	defer mem.FreeString(a, piped.Message)
	direct := format.Source(a, "kash", "each.kash", "printf a |(frame :lines) |(each) cat\n", "tabs", 4)
	defer mem.FreeString(a, direct.Text)
	defer mem.FreeString(a, direct.Code)
	defer mem.FreeString(a, direct.Message)
	if !piped.OK || !direct.OK || piped.Text != direct.Text || direct.Text != "printf a |(frame :lines) |(each) cat\n" {
		t.Error("implicit-stage command did not canonicalize to the space form")
	}

	// A nested group expands regardless of line width, and stays stable.
	inline := format.Source(a, "kash", "group.kash", "printf a |(broadcast) (cat > ./l, cat > ./r)\n", "tabs", 4)
	defer mem.FreeString(a, inline.Text)
	defer mem.FreeString(a, inline.Code)
	defer mem.FreeString(a, inline.Message)
	expanded := format.Source(a, "kash", "group.kash", inline.Text, "tabs", 4)
	defer mem.FreeString(a, expanded.Text)
	defer mem.FreeString(a, expanded.Code)
	defer mem.FreeString(a, expanded.Message)
	if !inline.OK || !expanded.OK || expanded.Text != inline.Text {
		t.Error("expanded Kash graph formatting is not idempotent")
	}
	if !strings.Contains(inline.Text, "\n\t|(broadcast) (\n\t\tcat > ./l,\n") {
		t.Error("nested Kash group did not expand structurally")
	}
}

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
