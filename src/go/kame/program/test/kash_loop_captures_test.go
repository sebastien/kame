package programcheck

import (
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"strings"
	"testing"
)

func TestKashLoopCaptureScope(t *testing.T) {
	for _, tc := range []struct {
		name, iterable, body, want, diagnostic string
	}{
		{"literal", `(./${stem}/{stem:*}.txt)`, `		def captured () = stem
		out! (captured ())
`, "abitems", ""},
		{"computed", "source", `		out! "$stem"
		def captured () = stem
		out! (captured ())
		if true
			let stem = "unused inner"
			let stem = "inner"
			(out! stem)
`, "aainnerbbinneritems", ""},
		{"computed assignment", "source", `		set stem = "wrong"
`, "", "PARSE_ERR"},
		{"list", "(if false then source else [1])", `		set stem = "changed"
		(out! stem)
`, "changedchanged", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := wasm.NewMemoryHost(nil)
			defer host.Free()
			host.Mkdir("items", 0o755)
			host.SetFile("items/a.txt", nil)
			host.SetFile("items/b.txt", nil)
			parsed := script.ParseKameFile(nil, "Kamefile", `task default
	let stem = "unused"
	let stem = "items"
	let source = ./${stem}/{stem:*}.txt
	for item in `+tc.iterable+"\n"+tc.body+`	(out! stem)
`)
			defer parsed.Free()
			registry := eval.NewRegistry(nil)
			defer registry.Free()
			operations.Register(registry)
			compiled := program.Compile(nil, parsed, registry, program.Options{Host: host, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
			defer compiled.Free(nil)
			if compiled.Program == nil {
				t.Fatalf("compile: %v", compiled.Diagnostics)
			}
			p := compiled.Program
			defer p.Free()
			result := p.Materialize("default")
			defer result.Free(nil)
			if result.Diagnostic.Code != tc.diagnostic {
				t.Fatalf("materialize: %v, wanted diagnostic %q", result.Diagnostic, tc.diagnostic)
			}
			var output strings.Builder
			for next := p.NextEvent(); next.OK; next = p.NextEvent() {
				if next.Event.Kind == program.Stdout && next.Event.Target == "default" {
					output.Write(next.Event.Data)
				}
				next.Event.Free(nil)
			}
			if got := output.String(); got != tc.want {
				t.Fatalf("stdout = %q, want %q", got, tc.want)
			}
		})
	}
}
