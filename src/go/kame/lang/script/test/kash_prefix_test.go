package scriptcheck

import (
	"kame/lang/script"
	"testing"
)

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
