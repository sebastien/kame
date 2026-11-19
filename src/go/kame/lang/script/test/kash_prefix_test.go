package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/script"
	"strings"
	"testing"
)

func TestKashCommandWordAtSigns(t *testing.T) {
	const text = "printf @build --mtime=@0 name@host @@ @(str 42) '@{quoted}' \"./@{quoted}\"\n"
	s := script.ParseKash(nil, "words.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 {
		t.Fatalf("valid command words failed to parse: %v", s.Diagnostics)
	}
	words := s.Items[0].Expression.Items[0].Items
	want := []string{"printf", "@build", "--mtime=@0", "name@host", "@@", "@", "@{quoted}", "./@{quoted}"}
	if len(words) != len(want) {
		t.Fatalf("got %d words, want %d", len(words), len(want))
	}
	for i := range want {
		if len(words[i].Parts) == 0 || words[i].Parts[0].Expr != nil || words[i].Parts[0].Text != want[i] {
			t.Errorf("word %d lost literal text %q", i, want[i])
		}
	}
	parts := words[5].Parts
	if len(parts) != 2 || parts[1].Form != "(" || expr.Ungroup(parts[1].Expr).Kind != expr.Application {
		t.Error("parenthesized value after a literal @ was lost")
	}
	if formatted := script.Format(nil, s); formatted != text {
		t.Errorf("formatted words as %q", formatted)
	}
	for _, text := range []string{
		"printf @{name}", "printf prefix@{name}", "printf ./dir/@{name}",
		"printf > @{name}/file", ":cwd ./@{name} printf value",
		"printf @\\\n{name}", "printf \\@{name}",
	} {
		invalid := script.ParseKash(nil, "invalid.kash", text)
		start, end := strings.IndexByte(text, '@'), strings.IndexByte(text, '{')+1
		if len(invalid.Diagnostics) == 0 || invalid.Diagnostics[0].Code != "PARSE_ERR" ||
			invalid.Diagnostics[0].Span.Start != start || invalid.Diagnostics[0].Span.End != end ||
			!strings.Contains(invalid.Diagnostics[0].Message, "$name") {
			t.Errorf("%q reported %v", text, invalid.Diagnostics)
		}
		invalid.Free()
	}
}

func TestKashActionAndValueContextsSelectDifferentTrees(t *testing.T) {
	const line = "emit \"hello\""
	value := expr.Parse(nil, "value.km", line)
	defer value.Free()
	if len(value.Diagnostics) != 0 || value.Expr == nil || value.Expr.Kind != expr.Application {
		t.Fatalf("value context did not parse an application: %v", value.Diagnostics)
	}
	for _, prefix := range []string{"", "def emit word = word\n"} {
		action := script.ParseKash(nil, "action.kash", prefix+line+"\n")
		if len(action.Diagnostics) != 0 || len(action.Items) == 0 {
			t.Errorf("action context failed to parse: %v", action.Diagnostics)
		} else {
			item := action.Items[len(action.Items)-1]
			if item.Kind != script.Command || item.Expression.Kind != expr.CommandGraph ||
				!strings.HasSuffix(script.Format(nil, action), line+"\n") {
				t.Error("a binding changed the command grammar or spelling")
			}
		}
		action.Free()
	}
}

func TestKashBlocksUseExactLocalPrefixes(t *testing.T) {
	const text = "if true\n  while true\n       break\n  printf first\nelif false\n    printf second\nelse\n\tprintf third\nprintf done\n"
	const want = "if true\n\twhile true\n\t\tbreak\n\tprintf first\nelif false\n\tprintf second\nelse\n\tprintf third\nprintf done\n"
	s := script.ParseKash(nil, "prefixes.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 {
		t.Fatalf("valid blocks failed to parse: %v", s.Diagnostics)
	}
	if formatted := script.Format(nil, s); formatted != want {
		t.Errorf("block format = %q", formatted)
	}
}

func TestKashBlocksRejectIndentationThatMatchesNoActivePrefix(t *testing.T) {
	for _, text := range []string{
		"if true\n  while true\n      break\n   printf invalid\n",
		"if true\n  printf first\n\tprintf invalid\n",
		"if true\n \tprintf mixed\n",
		"  printf orphaned\n",
	} {
		s := script.ParseKash(nil, "invalid.kash", text)
		if len(s.Diagnostics) == 0 || s.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("accepted invalid indentation in %q: %v", text, s.Diagnostics)
		}
		s.Free()
	}
}

func TestKashStatementMatchUsesArrowArms(t *testing.T) {
	const text = "match value + 1\n\t2 =>\n\t\t# Before two\n\t\tprintf two\n\t3 => out! \"three\"\n\t# Before else\n\telse => out! \"other\"\n"
	s := script.ParseKash(nil, "match.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 {
		t.Fatalf("statement match failed to parse: %v", s.Diagnostics)
	}
	if formatted := script.Format(nil, s); formatted != text {
		t.Errorf("statement match formatted as %q", formatted)
	}
	for _, text := range []string{
		"match value\n\tcase 1\n\t\tprintf bad\n",
		"match value\n\t1\n\t\tprintf bad\n",
		"match value\n\telse => nil\n\t2 => nil\n",
	} {
		invalid := script.ParseKash(nil, "invalid.kash", text)
		if len(invalid.Diagnostics) == 0 || invalid.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("accepted invalid match in %q: %v", text, invalid.Diagnostics)
		}
		invalid.Free()
	}
}
