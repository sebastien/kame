package wasmcheck

import (
	"kame/host/wasm"
	"kame/internal/mem"
	"strings"
	"testing"
)

func TestSessionSeparatesSplitKameFilesFromInlineExpressions(t *testing.T) {
	for _, example := range []struct {
		fields string
		want   string
	}{
		{``, "PARSE_ERR"},
		{`,"inline":1,"sourceDirectory":"/program"`, "PARSE_ERR"},
		{`,"inline":1`, ""},
	} {
		start := wasm.NewRuntime(mem.System, "")
		if start.Runtime == nil {
			t.Fatal("could not create WASM runtime")
		}
		result := start.Runtime.PrepareSession([]byte(`{"fragments":[{"lang":"km","name":"program.km","text":"(out! 42)"` + example.fields + `}]}`))
		if result.Code != example.want || (example.want != "" && !strings.Contains(result.Message, "require declarations")) {
			t.Errorf("session with fields %q returned %q: %s", example.fields, result.Code, result.Message)
		}
		result.Free(mem.System)
		start.Runtime.Free()
	}
}
