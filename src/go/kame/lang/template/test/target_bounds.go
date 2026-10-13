package template_test

import (
	"kame/lang/template"
	"solod.dev/so/testing"
)

func TestRemediation3TargetBudgets(t *testing.T) {
	a := t.Allocator()
	patterns := []string{"plain", "{x:*a*a*a*b}", "{*}{*}{*}z", "{x:*}{x:*}z", "{~a+}"}
	for i := range patterns {
		parsed := template.ParseTarget(a, "target", patterns[i])
		budget := 0
		m := parsed.MatchTargetBudget(a, "aaaaaaaaaaaa", &budget)
		if m == nil || !m.Limited || len(m.Captures) != 0 { t.Error("zero target budget did not fail closed") }
		m.Free(a)
		if i != 0 {
			budget = 20
			m = parsed.MatchTargetBudget(a, "aaaaaaaaaaaa", &budget)
			if m == nil || !m.Limited || len(m.Captures) != 0 { t.Error("target candidate/backtracking bound escaped") }
			m.Free(a)
		}
		parsed.Free()
	}
	parsed := template.ParseTarget(a, "target", "{x:*}/{x:*}")
	m := parsed.MatchTarget(a, "a/a")
	if m == nil || m.Limited || len(m.Captures) != 2 { t.Error("ordinary repeated target capture changed") }
	m.Free(a)
	parsed.Free()
}
