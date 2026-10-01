package wasm

import (
    "kame/core"
    "solod.dev/so/mem"
)

func CompletionValueFromJSON(a mem.Allocator, data []byte, out *core.Value) bool {
    return core.ParseJSON(a, data, out)
}
