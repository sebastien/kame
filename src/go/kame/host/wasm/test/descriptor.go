package wasmcheck

import (
	"kame/core"
	"kame/host/wasm"
	"kame/internal/testing"
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
		`{"name":"a","text":"","sourceDirectory":false}`,
		`{"name":"a","text":"","sourceDirectory":"relative"}`,
		`{"name":"a","text":"","sourceSignature":null}`,
		`{"name":"a","text":"","sourceSignature":42}`,
		`{"name":"a","text":"","sourceSignature":false}`,
		`{"name":"a","text":"","sourceSignature":[]}`,
		`{"name":"a","text":"","sourceSignature":{}}`,
		`{"name":"a","text":"","sourceSignature":""}`,
		`{"name":"a","text":"","sourceSignature":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde"}`,
		`{"name":"a","text":"","sourceSignature":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"}`,
		`{"name":"a","text":"","sourceSignature":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg"}`,
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

func TestSourceSignatureOptionalAndValidAcrossDescriptors(t *testing.T) {
	a := t.Allocator()
	fields := []string{
		``,
		`,"sourceSignature":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`,
		`,"sourceSignature":"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"`,
	}
	for i := range fields {
		start := wasm.NewRuntime(a, "")
		if start.Runtime == nil {
			start.Result.Free(a)
			t.Fatal("create runtime")
			return
		}
		r := start.Runtime
		fragment := `{"name":"a","lang":"expr","text":"42"` + fields[i] + `}`
		build := r.SetBuildSources([]byte(`{"sources":[` + fragment + `]}`))
		if build.Code != "" || len(r.BuildSources) != 1 {
			t.Error("valid build source signature rejected")
		} else {
			signature := r.BuildSources[0].Signature
			if i == 0 {
				if signature.Mode != core.SignatureUnavailable {
					t.Error("omitted source signature should remain unavailable")
				}
			} else if signature.Mode != core.SignatureContent || signature.Digest[0] != 0x01 || signature.Digest[31] != 0xef {
				t.Error("source signature was not decoded")
			}
		}
		build.Free(a)
		session := r.PrepareSession([]byte(`{"fragments":[` + fragment + `]}`))
		if session.Code != "" || r.Session == nil {
			t.Error("valid session source signature rejected")
		}
		session.Free(a)
		r.Free()
		condition := wasm.RunCompositionCondition(a, []byte(`{"sources":[],"condition":`+fragment+`}`))
		if i == 0 {
			if condition.Code != "" || condition.Text != "true" {
				t.Error("composition condition without signature rejected")
			}
		} else if condition.Code != "EXPR_INVALID" {
			t.Error("composition condition accepted disallowed sourceSignature field")
		}
		condition.Free(a)
	}
}

func TestInvocationInputDescriptorRequiresAnExplicitBooleanInteger(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	invalid := []string{"true", "\"stdin\"", "-1", "2", "[]"}
	for i := range invalid {
		result := r.PrepareSession([]byte(`{"fragments":[],"dataInput":` + invalid[i] + `}`))
		if result.Code != "PARSE_ERR" {
			t.Error("invalid data input policy accepted")
		}
		result.Free(a)
	}
}
