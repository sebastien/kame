package wasmcheck

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"kame/internal/testing"
	"strings"
)

func TestRuntimeFinallyWaitsForCancelledRequestAndDrainsCleanup(t *testing.T) {
	for mode := 0; mode < 2; mode++ {
		a := t.Allocator()
		started := wasm.NewRuntime(a, "")
		r := started.Runtime
		if r == nil {
			t.Fatal("runtime creation failed")
			return
		}
		r.FinalizationTick(1, false)
		expression := `(finally! ([] (read "resource")) ([] (out! "cleanup")))`
		if mode == 1 {
			expression = `(finally! [timeout-ms: 1] ([] 42) ([] (read "resource")))`
		}
		query := r.RequestExpression(expression)
		if query.Code != "" {
			t.Error("expression request rejected")
		}
		query.Free(a)
		request := host.NextResult{}
		for i := 0; i < 64 && !request.OK; i++ {
			request = r.Step()
		}
		if !request.OK {
			t.Fatal("callback did not request host work")
			return
		}
		if mode == 0 {
			r.Cancel()
		} else {
			r.FinalizationTick(1000002, false)
		}
		cancel := host.NextResult{}
		for i := 0; i < 64 && !cancel.OK; i++ {
			cancel = r.Step()
		}
		if !cancel.OK || cancel.Request.Kind != host.RequestProcessCancel {
			t.Error("withdrawn request was not forwarded for cancellation")
		}
		if cancel.OK {
			r.Complete(cancel.Request, core.Value{}, diagnostic.Diagnostic{})
			cancel.Request.Free(a)
		}
		result := r.Result()
		if result.Done || r.ExpressionEffectKind() != 0 {
			t.Error("cleanup or publication preceded host quiescence")
		}
		result.Free(a)
		// Acknowledgement is mandatory even though its evaluation result is stale.
		r.Complete(request.Request, core.Value{}, diagnostic.Diagnostic{Code: "EXEC_CANCELLED"})
		request.Request.Free(a)
		for i := 0; i < 64; i++ {
			next := r.Step()
			if next.OK {
				next.Request.Free(a)
				t.Error("unexpected extra cleanup request")
			}
		}
		result = r.Result()
		code := "EXEC_CANCELLED"
		if mode == 1 {
			code = "FINALIZE_TIMEOUT"
		}
		if !result.Done || result.Diagnostic.Code != code {
			t.Error("wrong finalization outcome: " + result.Diagnostic.Code)
		}
		if mode == 0 {
			var output [7]byte
			if r.ExpressionEffectKind() != 1 || !r.CopyExpressionEffect(output[:]) || string(output[:]) != "cleanup" {
				t.Error("cancellation lost cleanup output")
			}
		}
		result.Free(a)
		r.Free()
		started.Result.Free(a)
	}
}

func TestRuntimeResultDiagnosticQueryPreservesSecondaryAndRoot(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	r := started.Runtime
	if r == nil {
		t.Fatal("runtime creation failed")
		return
	}
	r.FinalizationTick(1, false)
	query := r.RequestExpression(`(finally! ([] (fail! "body-primary")) ([] (fail! "cleanup-secondary")))`)
	query.Free(a)
	for i := 0; i < 64; i++ {
		next := r.Step()
		if next.OK {
			next.Request.Free(a)
			t.Error("unexpected host request")
		}
	}
	interest := r.Node.Interest
	for i := 0; i < 16; i++ {
		document := r.ResultDiagnosticJSON()
		if document.Code != "" || !strings.Contains(document.Text, "body-primary") || !strings.Contains(document.Text, "cleanup-secondary") {
			t.Error("result diagnostic lost a failure")
		}
		document.Free(a)
	}
	if r.Node.Interest != interest {
		t.Error("diagnostic query released root interest")
	}
	result := r.Result()
	if !result.Done || result.Diagnostic.Code != "USER_ERROR" || result.Diagnostic.Message != "body-primary" {
		t.Error("diagnostic query consumed or changed the result")
	}
	result.Free(a)
	r.Free()
	started.Result.Free(a)
}
