package operationscheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestDocumentBareTrimMarkerRender(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "trim.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(render """@if false
@if fail! "unselected"
bad
@end if
@else -
 safe
@-end if""")`,
		`(render "html" """<!-- @if true -->body <!-- @-end if - --> tail""")`,
		`(render "html" """<!-- @raw - --> body <!-- @-end raw - --> tail""")`,
		`(render """@raw -
 body
@-end raw -
 tail""")`,
		`(render """@end2 @else_foo @raw/path @endif""")`,
		`(render """outside @end!""")`,
		`(render "html" """<!-- @raw -->body<!-- @end raw -->!""")`,
	}
	wants := []string{"safe", "bodytail", "bodytail", "bodytail", "@end2 @else_foo @raw/path @endif", "outside @end!", "body!"}
	for i := range texts {
		result := evaluate(t, p, texts[i])
		if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != wants[i] {
			t.Errorf("rendered %q (diagnostic %s), want %q for %s", result.Value.Text, result.Diagnostic.Code, wants[i], texts[i])
		}
		result.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
