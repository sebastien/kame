package script

import "kame/lang/source"

// ValidateKashLibrary runs after loading, before any execution. Included source
// may define functions with arbitrary bodies, but cannot run top-level work.
func ValidateKashLibrary(s *Script) {
	for i := range s.Items {
		item := s.Items[i]
		if (item.Kind != Comment && item.Kind != Definition && item.Kind != Include) || (item.Definition != nil && item.Definition.Eager) {
			s.Diagnostics = append(s.Diagnostics, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: item.Span, Message: "Kash libraries allow only definitions, comments, and literal top-level includes"})
		}
	}
}
