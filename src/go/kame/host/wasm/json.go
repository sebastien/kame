package wasm

import (
	"kame/core"
	"kame/internal/mem"
)

func CompletionValueFromJSON(a mem.Allocator, data []byte, out *core.Value) bool {
	return core.ParseJSON(a, data, out)
}
