package wasm_test

import (
 "kame/host/wasm"
 "solod.dev/so/testing"
)

func TestDeclarationPredicateDescriptorIsTypedAndHostFree(t *testing.T) {
 a := t.Allocator()
 result := wasm.DeclarationPredicate(a, []byte(`{"prefix":"mode ?= \"debug\"\n","predicate":"(eq mode \"release\")","defines":["mode=release"],"environment":["KAME_mode=debug"]}`))
 if result.Code != "" || result.Text != "true" { t.Error("declaration predicate descriptor lost literal configuration") }
 result.Free(a)
 result = wasm.DeclarationPredicate(a, []byte(`{"predicate":":false"}`))
 if result.Code != "" || result.Text != "false" { t.Error("false declaration predicate failed") }
 result.Free(a)
 inputs := []string{`[]`, `{}`, `{"predicate":true}`, `{"predicate":":true","prefix":42}`, `{"predicate":":true","name":[]}`, `{"predicate":":true","defines":"mode=release"}`, `{"predicate":":true","environment":[42]}`, `{"predicate":":true","unexpected":0}`}
 for i := range inputs {
  result = wasm.DeclarationPredicate(a, []byte(inputs[i]))
  if result.Code != "EXPR_INVALID" { t.Error("malformed declaration descriptor was accepted") }
  result.Free(a)
 }
 result = wasm.DeclarationPredicate(a, []byte(`{"predicate":"(read \"never read\")"}`))
 if result.Code != "CAP_DENIED" || result.HostNeeded { t.Error("declaration predicate exposed a host request") }
 result.Free(a)
}
