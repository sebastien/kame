package script

import "solod.dev/so/strings"

// Legacy markers are rejected only in directive context; named definitions
// such as when = :true continue to use the ordinary definition grammar.
func rejectLegacyDirective(s *Script, start int, end int) int {
	text := s.Source.Text[start:end]
	if topLevel(text, '=') >= 0 {
		return 0
	}
	if text != "otherwise" && text != "end" && text != "when" && !strings.HasPrefix(text, "when ") && !strings.HasPrefix(text, "when\t") {
		return 0
	}
	s.error(start, end, "legacy source directives are unsupported; use (if TEST (include PATH) :nil)")
	return nextLine(s.Source.Text, end)
}
