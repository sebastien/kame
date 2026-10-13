package wasm_test

import (
	"kame/core"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestDeclarationPredicateDescriptorIsTypedAndHostFree(t *testing.T) {
	a := t.Allocator()
	result := wasm.DeclarationPredicate(a, []byte(`{"prefix":"mode ?= \"debug\"\n","predicate":"(eq mode \"release\")","defines":["mode=release"],"environment":["KAME_mode=debug"]}`))
	if result.Code != "" || result.Text != "true" {
		t.Error("declaration predicate descriptor lost literal configuration")
	}
	result.Free(a)
	result = wasm.DeclarationPredicate(a, []byte(`{"predicate":":false"}`))
	if result.Code != "" || result.Text != "false" {
		t.Error("false declaration predicate failed")
	}
	result.Free(a)
	inputs := []string{`[]`, `{}`, `{"predicate":true}`, `{"predicate":":true","prefix":42}`, `{"predicate":":true","name":[]}`, `{"predicate":":true","defines":"mode=release"}`, `{"predicate":":true","environment":[42]}`, `{"predicate":":true","unexpected":0}`}
	for i := range inputs {
		result = wasm.DeclarationPredicate(a, []byte(inputs[i]))
		if result.Code != "EXPR_INVALID" {
			t.Error("malformed declaration descriptor was accepted")
		}
		result.Free(a)
	}
	result = wasm.DeclarationPredicate(a, []byte(`{"predicate":"(read \"never read\")"}`))
	if result.Code != "CAP_DENIED" || result.HostNeeded {
		t.Error("declaration predicate exposed a host request")
	}
	result.Free(a)
}

func TestCompositionConditionDescriptorSharesValidationAndDiagnosticJSON(t *testing.T) {
	a := t.Allocator()
	inputs := []string{`{"predicate":"0"}`, `{"predicate":"[]"}`, `{"predicate":":nil"}`, `{"predicate":":false"}`, `{"prefix":"mode ?= \"debug\"\nunused = (read \"never read\")\n","predicate":"(eq mode \"release\")","defines":["future=later","mode=release"],"environment":["KAME_mode=debug"]}`}
	for i := range inputs {
		result := wasm.RunCompositionCondition(a, []byte(inputs[i]))
		expected := "true"
		if i == 2 || i == 3 {
			expected = "false"
		}
		if result.Code != "" || result.Text != expected || result.HostNeeded {
			t.Error("composition condition lost ordinary truth or pure configuration")
		}
		result.Free(a)
	}
	invalid := []string{`[]`, `{}`, `{"predicate":true}`, `{"predicate":":true","prefix":42}`, `{"predicate":":true","name":[]}`, `{"predicate":":true","defines":"mode=release"}`, `{"predicate":":true","environment":[42]}`, `{"predicate":":true","unexpected":0}`}
	for i := range invalid {
		result := wasm.RunCompositionCondition(a, []byte(invalid[i]))
		if result.Code != "EXPR_INVALID" || result.HostNeeded {
			t.Error("composition descriptor validation was weakened")
		}
		result.Free(a)
	}
	result := wasm.RunCompositionCondition(a, []byte(`{"name":"condition.km","predicate":"missing"}`))
	if result.Code != "REF_MISSING" || result.HostNeeded {
		t.Error("composition condition exposed host work")
	}
	var value core.Value
	if !core.ParseJSON(a, []byte(result.Text), &value) {
		t.Error("composition diagnostic is not portable JSON")
	} else {
		code, message, source := "", "", ""
		for i := range value.Record {
			if value.Record[i].Key != "diagnostic" {
				continue
			}
			for j := range value.Record[i].Value.Record {
				field := value.Record[i].Value.Record[j]
				if field.Key == "code" {
					code = field.Value.Text
				}
				if field.Key == "message" {
					message = field.Value.Text
				}
				if field.Key == "source" {
					source = field.Value.Text
				}
			}
		}
		if code != result.Code || message != result.Message || source != "condition.km" {
			t.Error("composition diagnostic JSON lost code/message/source")
		}
	}
	value.Free(a)
	result.Free(a)
	result = wasm.DeclarationPredicate(a, []byte(`{"predicate":"42"}`))
	if result.Code != "EXPR_INVALID" || result.Message != "configuration predicate must return bool; got int" {
		t.Error("existing predicate stopped requiring bool")
	}
	result.Free(a)
}
