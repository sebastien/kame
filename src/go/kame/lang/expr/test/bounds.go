package exprcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/source"
)

func TestCommandSetupValuePrefixAtEOF(t *testing.T) {
	a := t.Allocator()
	keys := []string{"env", "accept-exit"}
	texts := []string{"", "setup "}
	for i := range keys {
		for j := range texts {
			s := source.New(a, "setup.kash", texts[j])
			end := len(s.Text)
			parsed := expr.ParseCommandSetupValuePrefix(a, s, end, keys[i])
			if parsed.Expr != nil || parsed.End != end || len(parsed.Diagnostics) != 1 {
				t.Error("missing setup value did not return a diagnostic at EOF")
			} else if parsed.Diagnostics[0].Message != "expected Kash stage setup value" || parsed.Diagnostics[0].Span.Start != end || parsed.Diagnostics[0].Span.End != end {
				t.Error("missing setup value diagnostic changed")
			}
			expr.Free(a, parsed.Expr)
			mem.FreeSlice(nil, parsed.Diagnostics)
			s.Free(a)
		}
	}
}

func TestRemediation3PatternBudgets(t *testing.T) {
	a := t.Allocator()
	patterns := []string{"plain", "{x:*a*a*a*b}", "{*}{*}{*}z", "{x:*}{x:*}z", "{~a+}"}
	for i := range patterns {
		parsed := expr.ParsePatternText(a, patterns[i], 0)
		budget := 0
		result := parsed.Pattern.MatchTextBudget(a, "aaaaaaaaaaaa", &budget)
		if result.Matched || !result.Limited || len(result.Captures) != 0 {
			t.Error("zero budget did not fail closed")
		}
		mem.FreeSlice(nil, result.Captures)
		if i != 0 {
			budget = 20
			result = parsed.Pattern.MatchTextBudget(a, "aaaaaaaaaaaa", &budget)
			if result.Matched || !result.Limited || len(result.Captures) != 0 {
				t.Error("candidate/backtracking work escaped the shared bound")
			}
			mem.FreeSlice(nil, result.Captures)
		}
		parsed.Pattern.Free(a)
		mem.FreeSlice(nil, parsed.Diagnostics)
	}
	budget := 20
	matched, limited := expr.MatchGlobBudget("*", "a", &budget)
	if !matched || limited {
		t.Error("ordinary nonempty glob changed")
	}
	matched, limited = expr.MatchGlobBudget("*", "", &budget)
	if matched || limited {
		t.Error("empty star semantics changed")
	}
	budget = 1
	matched, limited = expr.MatchGlobBudget("", "", &budget)
	if !matched || limited || budget != 0 {
		t.Error("last allowed step failed")
	}
	matched, limited = expr.MatchGlobBudget("", "", &budget)
	if matched || !limited {
		t.Error("exhausted shared budget was reused")
	}
}

func TestRemediation3SectionAndPatternPositions(t *testing.T) {
	a := t.Allocator()
	texts := []string{"((f _4096))", "((f _1000000000))", "((f _999999999999999999999999999999))"}
	for i := range texts {
		r := expr.Parse(a, "arity.km", texts[i])
		if len(r.Diagnostics) == 0 || r.Expr == nil || len(r.Expr.Parameters) != 0 {
			t.Error("excessive section arity materialized parameters")
		}
		r.Free()
	}
	r := expr.Parse(a, "arity.km", "((f _3))")
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Section || len(r.Expr.Parameters) != 4 {
		t.Error("ordinary placeholder section changed")
	}
	r.Free()
	r = expr.Parse(a, "arity.km", "((f _4095))")
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Section || len(r.Expr.Parameters) != expr.MaxSectionArity {
		t.Error("maximum supported placeholder section was rejected")
	}
	r.Free()
	parsed := expr.ParsePatternText(a, "{_18446744073709551616}", 0)
	if len(parsed.Diagnostics) == 0 {
		t.Error("overflowing pattern position accepted")
	}
	parsed.Pattern.Free(a)
	mem.FreeSlice(nil, parsed.Diagnostics)
}
