package script_test

import (
	"kame/lang/definition"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/testing"
)

func TestValueFragmentCompactHeadersMatchDefinitionParser(t *testing.T) {
	a := t.Allocator()
	texts := []string{"value=42", "value= 42", "value =42", "(identity x)=x", "(f)=42", "items+=[1]", "value?=42", "value??=42", "ready!=:true", "ready? =:true", "(collect rest...)=[rest]"}
	for _, text := range texts {
		prefix := "prior : dependency\n# before\n"
		authored := source.New(a, "fragments.km", prefix+text+"\n# after\nfollowing : dependency\n")
		end := len(prefix) + len(text)
		direct := definition.ParseRange(a, authored, len(prefix), end)
		parsed := script.ParseFragment(a, authored, "km", len("prior : dependency\n"), end+len("\n# after\n"))
		if len(direct.Diagnostics) != 0 || len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 {
			t.Error("compact header rejected: " + text)
		} else if parsed.Items[0].Kind != script.Comment || parsed.Items[1].Kind != script.Definition || parsed.Items[2].Kind != script.Comment {
			t.Error("compact header changed comment/definition order: " + text)
		} else {
			d := parsed.Items[1].Definition
			if d.Name != direct.Definition.Name || d.Function != direct.Definition.Function || d.Default != direct.Definition.Default || d.Contribution != direct.Definition.Contribution || d.Span.Start != len(prefix) || d.Span.End != end || d.Expression.Span.Start != direct.Definition.Expression.Span.Start {
				t.Error("fragment differs from definition grammar/spans: " + text)
			}
		}
		parsed.Free()
		direct.Free(a)
		authored.Free(a)
	}
}

func TestValueFragmentCompactHeadersRetainDiagnostics(t *testing.T) {
	a := t.Allocator()
	invalidHeads := []string{"1value=42", "value.name=42", "value/name=42", "value???=42", "value ? =42", "(f x.y)=42", "(f rest... x)=42", "build : dep=42"}
	for _, text := range invalidHeads {
		authored := source.New(a, "invalid.km", text)
		parsed := script.ParseFragment(a, authored, "km", 0, len(text))
		if definition.ValueHeaderEnd(a, authored, 0) != 0 || len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Error("invalid complete head became a value header: " + text)
		}
		if text == "value ? =42" && (len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Message != "expected statement separator" || parsed.Diagnostics[0].Span.Start != 6 || parsed.Diagnostics[0].Span.End != 7) {
			t.Error("noncontiguous default changed its statement diagnostic")
		}
		parsed.Free()
		authored.Free(a)
	}
	invalidValues := []string{"value=", "value=42 43", "(f x)?=42", "(f x)+=[x]"}
	for _, text := range invalidValues {
		authored := source.New(a, "invalid.km", text)
		direct := definition.ParseRange(a, authored, 0, len(text))
		parsed := script.ParseFragment(a, authored, "km", 0, len(text))
		if len(direct.Diagnostics) == 0 || len(parsed.Diagnostics) == 0 {
			t.Error("compact malformed definition was accepted: " + text)
		} else if parsed.Diagnostics[0].Code != direct.Diagnostics[0].Code || parsed.Diagnostics[0].Message != direct.Diagnostics[0].Message || parsed.Diagnostics[0].Span.Start != direct.Diagnostics[0].Span.Start || parsed.Diagnostics[0].Span.End != direct.Diagnostics[0].Span.End {
			t.Error("compact definition lost its authored diagnostic: " + text)
		}
		parsed.Free()
		direct.Free(a)
		authored.Free(a)
	}
	// Compact shell words must not become Kash definitions.
	authored := source.New(a, "command.kash", "value=42")
	if definition.KashHeaderEnd(a, authored, 0) != 0 {
		t.Error("value header fix weakened Kash operator spacing")
	}
	authored.Free(a)
}
