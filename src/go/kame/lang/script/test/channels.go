package script_test

import (
	"kame/lang/expr"
	"kame/lang/format"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestChannelDeclarationsAreLexicalSyntax(t *testing.T) {
	s := script.ParseKash(t.Allocator(), "channels.kash", "stream reports\n(printf a >&reports, &reports | cat)\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 {
		t.Error("named channel group did not parse")
		return
	}
	if len(s.Items) != 2 || s.Items[0].Expression.Kind != expr.CommandStream {
		t.Error("stream declaration became an executable")
		return
	}
	e := s.Items[1].Expression
	if len(e.Channels) != 1 || e.Channels[0] != "reports" {
		t.Error("graph lost its lexical declarations")
	}
	clone := expr.Clone(t.Allocator(), e)
	expr.Free(t.Allocator(), clone)
	formatted := format.Source(t.Allocator(), "kash", "channels.kash", s.Source.Text, "tabs", 4)
	if !formatted.OK {
		t.Error("channel script failed formatting")
	} else {
		copy := script.ParseKash(t.Allocator(), "formatted.kash", formatted.Text)
		if len(copy.Diagnostics) != 0 || len(copy.Items) != 2 {
			t.Error("formatted channel script did not reparse")
		}
		copy.Free()
	}
	mem.FreeString(t.Allocator(), formatted.Text)
	mem.FreeString(t.Allocator(), formatted.Code)
	mem.FreeString(t.Allocator(), formatted.Message)
}

func TestMalformedChannelDeclarationsFailParsing(t *testing.T) {
	texts := []string{"stream", "stream 123", "stream in", "stream reports extra", "stream reports\nstream reports"}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Errorf("invalid stream declaration accepted: %s", texts[i])
		}
		s.Free()
	}
}

func TestStreamingGraphFormatting(t *testing.T) {
	a := t.Allocator()
	inputs := []string{
		"printf a |(broadcast) (cat > ./left, cat > ./right)\n",
		"(printf a, printf b) |(merge) cat\n",
		"printf a |(frame :lines) |(balance) (|(each) cat, |(each) cat)\n",
		"printf a |(frame :lines) |(filter :true | cat > ./selected) | cat\n",
		"if true\n\tprintf a |(broadcast) (cat, cat)\n",
		"stream reports\n(printf a >&reports 2>&1, &reports | cat)\n",
		"printf a |(broadcast) (cat, cat) ?\n",
		"printf a |(broadcast) (cat, cat) ? printf fallback\n",
		"printf a |(broadcast) (cat, cat) &\n",
		"(printf a |(broadcast) (cat > ./audit, |(frame :lines) |(filter :true | cat >&errors) | cat > ./normal), &errors | cat)\n",
	}
	for i := range inputs {
		s := script.ParseKash(a, "stream.kash", inputs[i])
		if len(s.Diagnostics) != 0 {
			t.Errorf("input failed to parse: %s", inputs[i])
			s.Free()
			continue
		}
		formatted := script.FormatWithIndent(a, s, "  ")
		if i == 0 && formatted != "printf a\n  |(broadcast) (\n    cat > ./left,\n    cat > ./right\n  )\n" {
			t.Errorf("unexpected streaming layout: %s", formatted)
		}
		copy := script.ParseKash(a, "formatted.kash", formatted)
		if len(copy.Diagnostics) != 0 {
			t.Errorf("formatted graph did not parse: %s", formatted)
		} else {
			for j := range s.Items {
				if s.Items[j].Expression != nil && s.Items[j].Expression.Kind == expr.CommandGraph {
					before := expr.Compact(a, s.Items[j].Expression)
					after := expr.Compact(a, copy.Items[j].Expression)
					if before != after {
						t.Errorf("formatting changed graph: %s -> %s", before, after)
					}
					mem.FreeString(a, before)
					mem.FreeString(a, after)
				}
			}
			again := script.FormatWithIndent(a, copy, "  ")
			if formatted != again {
				t.Errorf("graph formatting is not idempotent: %s -> %s", formatted, again)
			}
			mem.FreeString(a, again)
		}
		mem.FreeString(a, formatted)
		copy.Free()
		s.Free()
	}
}
