package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestDocumentBareTrimMarkerRender(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "trim.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(render """@if(:false)@if(fail! "unselected")bad@end(if)@else-safe@end(if)""")`,
		`(render """@if(:true)body@end-tail""")`,
		`(render """@raw-body@end-tail""")`,
		`(render """@raw
body@end-tail""")`,
		`(render """@end2 @else_foo @raw/path @endif""")`,
		`(render """@if(:true)body@end!""")`,
		`(render """@raw-body@end!""")`,
	}
	wants := []string{"safe", "bodytail", "bodytail", "bodytail", "@end2 @else_foo @raw/path @endif", "body!", "body!"}
	for i := range texts {
		result := evaluate(t, p, texts[i])
		if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != wants[i] {
			t.Error("bare trim render failed: " + texts[i])
		}
		result.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
