package scriptcheck

import (
	"kame/lang/script"
	"testing"
)

func TestKashInteractiveBuiltinsRequireASession(t *testing.T) {
	for _, input := range []string{
		"jobs", "fg %1", "bg %1", "alias shortcut = [\"echo\"]", "unalias shortcut",
		"history", "option", "explain", "echo a | jobs", "let value = $(jobs)",
	} {
		for _, parse := range []func() *script.Script{
			func() *script.Script { return script.ParseKash(nil, "commands.kash", input) },
			func() *script.Script { return script.ParseKashRuleBody(nil, "rule.km", input) },
		} {
			parsed := parse()
			if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
				t.Errorf("%q: expected parse error, got %v", input, parsed.Diagnostics)
			}
			parsed.Free()
		}
	}
	for _, input := range []string{"x/jobs", "def jobs ()\n  return nil\njobs"} {
		parsed := script.ParseKash(nil, "commands.kash", input)
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", input, parsed.Diagnostics)
		}
		parsed.Free()
	}
}
