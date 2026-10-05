package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestRuntimeRegistersPluginOperationsBeforeEvaluation(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil { t.Fatal("runtime did not compile"); return }
	runtime := started.Runtime
	defer runtime.Free()
	declarations := []byte(`[{"name":"example","version":"1","operations":[{"name":"example-run","version":"2","minArity":1,"maxArity":1}]}]`)
	if result := runtime.RegisterPluginsJSON(declarations); result.Code != "" {
		t.Fatal("valid plugin declarations were rejected")
		result.Free(a)
		return
	}
	if result := runtime.RequestExpression(`(example-run "input")`); result.Code != "" {
		t.Fatal("plugin expression was rejected")
		result.Free(a)
		return
	}
	next := runtime.Step()
	if !next.OK || next.Request.Kind != host.RequestPlugin {
		t.Fatal("plugin operation did not yield the registered host request")
		return
	}
	if host.PayloadInt(next.Request.Payload, "generation") != next.Request.Generation || host.PayloadInt(next.Request.Payload, "attempt") != next.Request.Attempt {
		t.Error("plugin request omitted its evaluation generation or attempt")
	}
	argsJSON := host.PayloadList(next.Request.Payload, "argsJSON")
	if len(argsJSON) != 1 || argsJSON[0].Kind != core.String || argsJSON[0].Text != `{"kind":"string","data":"input"}` {
		t.Error("plugin request omitted the canonical JSON argument")
	}
	if host.PayloadText(next.Request.Payload, host.FieldData) == "" {
		t.Error("plugin request omitted its serialized host envelope")
	}
	response := core.NewString(a, `{"kind":"int","data":42}`)
	runtime.Complete(next.Request, response, diagnostic.Diagnostic{})
	next.Request.Free(a)
	_ = runtime.Step()
	_ = runtime.Step()
	result := runtime.Result()
	if !result.Done || result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("plugin JSON result did not decode into an owned Kame value")
	}
	result.Free(a)
}
