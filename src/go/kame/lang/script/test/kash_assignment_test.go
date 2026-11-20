package scriptcheck

import (
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
