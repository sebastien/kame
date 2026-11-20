package script

import "kame/lang/source"

// ValidateKashModule checks the declarations allowed in imported and included modules.
func ValidateKashModule(s *Script) {
	for i := range s.Items {
		item := s.Items[i]
		if (item.Kind != Comment && item.Kind != Definition && item.Kind != Include && item.Module == nil) || (item.Definition != nil && item.Definition.Eager) {
			s.Diagnostics = append(s.Diagnostics, source.Diagnostic{Code: "DEF_INVALID", Severity: source.Error, Span: item.Span, Message: "Kash modules allow only definitions, comments, and top-level source declarations"})
		}
	}
}
