package wasm_test

import (
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestAuthoredSourceFieldValidationAcrossDescriptors(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	invalid := []string{
		`{"name":1,"text":""}`,
		`{"name":"a","text":false}`,
		`{"name":"a","text":"","offset":"1"}`,
		`{"name":"a","text":"","offset":-1}`,
		`{"name":"a","text":"","offset":2147483648}`,
		`{"name":"a","text":"x","offset":2147483647}`,
		`{"name":"a","text":"","includes":false}`,
	}
	for i := range invalid {
		fragment := invalid[i]
		condition := wasm.RunCompositionCondition(a, []byte(`{"sources":[],"condition":`+fragment+`}`))
		if condition.Code != "EXPR_INVALID" {
			t.Error("invalid condition source accepted: " + fragment)
		}
		condition.Free(a)
		build := r.SetBuildSources([]byte(`{"sources":[` + fragment + `]}`))
		if build.Code != "PARSE_ERR" || len(r.BuildSources) != 0 {
			t.Error("invalid build source accepted: " + fragment)
		}
		build.Free(a)
		session := r.PrepareSession([]byte(`{"fragments":[{"lang":"km",` + fragment[1:] + `]}`))
		if session.Code != "PARSE_ERR" || r.Session != nil {
			t.Error("invalid session source accepted: " + fragment)
		}
		session.Free(a)
	}
}
