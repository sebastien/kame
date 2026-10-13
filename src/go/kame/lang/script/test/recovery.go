package script_test

import (
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestMalformedNestedKashValueRetainsDiagnostics(t *testing.T) {
	inputs := []string{"if true\n  ()\n", "if true\n  (\n", "match (value)\n  case 1\n    ()\n", "match (value)\n  case 1\n    (\n"}
	for i := range inputs {
		s := script.ParseKash(t.Allocator(), "invalid.kash", inputs[i])
		if len(s.Diagnostics) == 0 { t.Error("malformed nested Kash value was accepted") }
		for j := range s.Diagnostics {
			span := s.Diagnostics[j].Span
			if span.Start < 0 || span.End < span.Start || span.End > len(inputs[i]) { t.Error("nested Kash diagnostic lost its authored span") }
		}
		s.Free()
	}
}
