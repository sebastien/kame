package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/script"
	"testing"
)

func TestKashAssignmentRequiresAnUncapturedLocalLet(t *testing.T) {
	for _, text := range []string{
		"set missing = 1\n",
		"set count = 2\nlet count = 1\n",
		"def count = 1\nset count = 2\n",
		"let count = 1\ndef update ()\n  set count = 2\n",
		"let count = 1\nset count = 2\nlet read-count = (ignored => count)\n",
		"let count = 1\ndef read-count ()\n  printf %s $count\nset count = 2\n",
		"let command = (x => x)\ndef run ()\n  command input\nset command = nil\n",
		"let count = 1\nfor count in [1]\n  set count = 2\n",
		"def local ()\n\tlet prior = 4\n\tlet count =\n  1\n",
		"let count = 1\nset count =\nlet other = 2\n",
	} {
		parsed := script.ParseKash(nil, "assignment.kash", text)
		if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected assignment parse diagnostic, got %v", text, parsed.Diagnostics)
		}
		parsed.Free()
	}
	for _, text := range []string{
		"let count = 1\nif true\n  set count = 2\n",
		"let count = 1\nlet identity = (count => count)\nset count = 2\n",
		"let count = 1\ndef local ()\n  let count = 2\n  set count = 3\n  return count\nset count = 4\n",
		"let count =\r\n\t1\r\nset count =\r\n\t2\r\n",
		"let count = \\\n1\nset count = \\\n\\\n2\n",
	} {
		parsed := script.ParseKash(nil, "assignment.kash", text)
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", text, parsed.Diagnostics)
		}
		parsed.Free()
	}
}

func TestKashComputedEnvironmentTargetFormat(t *testing.T) {
	parsed := script.ParseKash(nil, "assignment.kash", `set env/${ if (eq? key "=") then "MODE" else key }="dev"`+"\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		t.Fatal(parsed.Diagnostics)
	}
	got := expr.FormatKashStatement(nil, parsed.Items[0].Expression, "\t")
	want := `set env/${if (eq? key "=") then "MODE" else key} = "dev"`
	if got != want {
		t.Fatalf("formatted target: %q, want %q", got, want)
	}
}

func TestKashBindingWrapsWholeArguments(t *testing.T) {
	const input = `let links = concat (flatmap opencode-skill-links (sdk-list OPENCODE_SKILLS)) (flatmap opencode-agent-links (sdk-list OPENCODE_AGENTS)) (flatmap opencode-rule-links (sdk-list OPENCODE_RULES))`
	const want = "let links =\n\tconcat \\\n\t\t(flatmap opencode-skill-links (sdk-list OPENCODE_SKILLS)) \\\n\t\t(flatmap opencode-agent-links (sdk-list OPENCODE_AGENTS)) \\\n\t\t(flatmap opencode-rule-links (sdk-list OPENCODE_RULES))\n"
	var call string
	for _, text := range []string{input, want} {
		parsed := script.ParseKash(nil, "links.kash", text)
		defer parsed.Free()
		if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 || parsed.Items[0].Definition == nil {
			t.Fatalf("expected one binding: %v", parsed.Diagnostics)
		}
		initializer := expr.Compact(nil, parsed.Items[0].Definition.Expression)
		if call != "" && initializer != call {
			t.Fatalf("binding changed from %q to %q", call, initializer)
		}
		call = initializer
		if got := script.FormatWithIndent(nil, parsed, "\t"); got != want {
			t.Fatalf("formatted as:\n%s\nwant:\n%s", got, want)
		}
	}
}
