package wasm_test

import (
    "kame/core"
    "kame/diagnostic"
    "kame/host"
    "kame/host/wasm"
    "solod.dev/so/slices"
    "solod.dev/so/testing"
)

func TestCollectedShellForwardsAnExplicitEmptySnapshot(t *testing.T) {
    a := t.Allocator()
    buffer := slices.Make[byte](a, 1024*1024)
    defer slices.Free(a, buffer)
    heap := wasm.NewHeap(buffer)
    started := wasm.NewRuntimeWithHeap(&heap, "scoped.kmk", "VALUE = (shell \"true\")\nchosen :\n\t@(out (get VALUE \"status\"))\n")
    if started.Runtime == nil { t.Fatal("compile collected shell"); return }
    r := started.Runtime
    defer r.Free()
    r.SetForwarding(true)
    begin := r.RequestTarget("chosen")
    if begin.Code != "" { begin.Free(r.Alloc); t.Fatal("request collected shell"); return }
    begin.Free(r.Alloc)
    processes := 0
    for step := 0; step < 128; step++ {
        next := r.Step()
        if next.OK {
            if next.Request.Kind == host.RequestProcess {
                processes++
                found := false
                capture := false
                for i := range next.Request.Payload.Record {
                    field := next.Request.Payload.Record[i]
                    if field.Key == host.FieldEnvironment && field.Value.Kind == core.List && len(field.Value.List) == 0 { found = true }
                }
                var decoded core.Value
                if core.ParseJSON(r.Alloc, []byte(host.PayloadText(next.Request.Payload, host.FieldData)), &decoded) {
                    for i := range decoded.Record { if decoded.Record[i].Key == host.FieldCapture && decoded.Record[i].Value.Kind == core.Bool && decoded.Record[i].Value.Bool { capture = true } }
                    decoded.Free(r.Alloc)
                }
                if !found || !capture || host.PayloadText(next.Request.Payload, host.FieldOp) != "recipe" || host.PayloadText(next.Request.Payload, host.FieldScript) != "true" {
                    t.Error("collected shell did not carry exact empty snapshot")
                }
                r.ProcessTerminal(next.Request, nil, nil, 0, 0, 0, "", "")
            } else { r.Complete(next.Request, core.Value{Kind: core.Nil}, diagnostic.Diagnostic{}) }
            next.Request.Free(r.Alloc)
        }
        result := r.Result()
        done := result.Done
        if done && result.Diagnostic.Code != "" { t.Error("collected shell failed: "+result.Diagnostic.Code) }
        result.Free(r.Alloc)
        if done {
            if processes != 1 { t.Error("collected shell did not launch exactly once") }
            return
        }
    }
    t.Error("collected shell did not complete")
}
