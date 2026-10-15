package program_test

import (
 "kame/program"
 "solod.dev/so/testing"
)

func TestDeclarationPredicateUsesLiteralOverridesAndLazyDefinitions(t *testing.T) {
 a := t.Allocator()
 text := "mode ?= \"debug\"\nmode ?= (sh! \"never executed\")\n(selected value) = (eq mode value)\nunused = (read \"never read\")\n"
 result := program.DeclarationPredicate(a, "conditions.kmk", text, "(selected \"release\")", []string{"mode=first", "future=later", "mode=release"}, []string{"KAME_mode=environment"})
 if result.Diagnostic.Code != "" || !result.Selected { t.Error("override predicate: "+result.Diagnostic.Code+" "+result.Diagnostic.Message) }
 result.Diagnostic.Free(a)
 result = program.DeclarationPredicate(a, "conditions.kmk", text, "(selected \"environment\")", nil, []string{"KAME_mode=environment"})
 if result.Diagnostic.Code != "" || !result.Selected { t.Error("environment predicate: "+result.Diagnostic.Code+" "+result.Diagnostic.Message) }
 result.Diagnostic.Free(a)
 result = program.DeclarationPredicate(a, "conditions.kmk", text, "(selected \"release\")", nil, nil)
 if result.Diagnostic.Code != "" || result.Selected { t.Error("predicate ignored authored default") }
 result.Diagnostic.Free(a)
}

func TestDeclarationPredicateRejectsCyclesEffectsAndNonBooleanValues(t *testing.T) {
 a := t.Allocator()
 prefixes := []string{"a = b\nb = a\n", "", "", "", "", ""}
 predicates := []string{"a", "42", "(sh! \"never execute\")", "(out! \"never publish\")", "(read \"never read\")", "(eq missing 1)"}
 codes := []string{"DEP_CYCLE", "EXPR_INVALID", "CAP_DENIED", "PHASE_INVALID", "CAP_DENIED", "REF_MISSING"}
  for i := range prefixes {
  result := program.DeclarationPredicate(a, "conditions.kmk", prefixes[i], predicates[i], nil, nil)
   if result.Diagnostic.Code != codes[i] { t.Error("predicate "+predicates[i]+" expected "+codes[i]+" got "+result.Diagnostic.Code+" "+result.Diagnostic.Message) }
   if predicates[i] == "42" && (result.Diagnostic.Message != "configuration predicate must return bool; got int" || len(result.Diagnostic.Tips) != 1 || result.Diagnostic.Source != "conditions.kmk" || result.Diagnostic.Span.End != 2 || !result.Diagnostic.Owned) { t.Error("strict predicate lost kind, owned tip, or source span") }
  result.Diagnostic.Free(a)
 }
}
