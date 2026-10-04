package host_test

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/testing"
)

func TestRecipePayloadPreservesSelectedShellArguments(t *testing.T) {
	a := t.Allocator()
	payload := host.RecipeExecutionPayload(a, "printf 'quoted\\n'", []string{"./out"}, []string{"MODE=scoped"}, []string{"/tool path/sh", "-c", ""})
	defer payload.Free(a)
	var decoded core.Value
	if !core.ParseJSON(a, []byte(host.PayloadText(payload, host.FieldData)), &decoded) {
		t.Fatal("recipe descriptor is not JSON")
		return
	}
	defer decoded.Free(a)
	shell := host.PayloadList(decoded, "shell")
	environment := host.PayloadList(decoded, host.FieldEnvironment)
	if len(shell) != 3 || shell[0].Text != "/tool path/sh" || shell[1].Text != "-c" || shell[2].Text != "" || len(environment) != 1 || environment[0].Text != "MODE=scoped" {
		t.Error("selected recipe settings were not preserved")
	}
	if host.PayloadText(decoded, host.FieldScript) != "printf 'quoted\\n'" {
		t.Error("recipe text changed")
	}
	legacy := host.RecipeEnvironmentPayload(a, "true", nil, nil)
	defer legacy.Free(a)
	var original core.Value
	if !core.ParseJSON(a, []byte(host.PayloadText(legacy, host.FieldData)), &original) {
		t.Fatal("legacy recipe descriptor is not JSON")
		return
	}
	defer original.Free(a)
	for i := range original.Record {
		if original.Record[i].Key == "shell" || original.Record[i].Key == host.FieldEnvironment {
			t.Error("legacy descriptor unexpectedly acquired settings")
		}
	}
}

func TestCollectedShellPayloadRetainsExplicitEmptyEnvironment(t *testing.T) {
    a := t.Allocator()
    payload := host.ScopedProcessPayload(a, "true", nil)
    var decoded core.Value
    if !core.ParseJSON(a, []byte(host.PayloadText(payload, host.FieldData)), &decoded) {
        t.Fatal("invalid scoped process descriptor")
        payload.Free(a)
        return
    }
    foundWire, foundNative := false, false
    for i := range decoded.Record {
        if decoded.Record[i].Key == host.FieldEnvironment && decoded.Record[i].Value.Kind == core.List && len(decoded.Record[i].Value.List) == 0 { foundWire = true }
    }
    for i := range payload.Record {
        if payload.Record[i].Key == host.FieldEnvironment && payload.Record[i].Value.Kind == core.List && len(payload.Record[i].Value.List) == 0 { foundNative = true }
    }
    if !foundWire || !foundNative || len(host.PayloadList(decoded, "outputs")) != 0 { t.Error("empty process environment was omitted or outputs introduced") }
    decoded.Free(a)
    payload.Free(a)
}
