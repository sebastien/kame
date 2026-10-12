package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestInterpolatedPathsRetainStructuralChildren(t *testing.T) {
	a := t.Allocator()
	texts := []string{`./build/@(component)/main.js`, `@(root)/sh/**/*.sh`, `../@(join ["a" "b"] " ")/@(name)`, "/@(nop\n \"nested )\")/file", `$(printf "%s" ./@(name)/file)`, `$(printf "%s" @(root)/file)`, `./journal/[a@(name)].md`, `./journal/[@(get [name: "b"] "name")].md`}
	for i := range texts {
		parsed := expr.Parse(a, "paths.km", texts[i])
		if len(parsed.Diagnostics) != 0 || parsed.Expr == nil {
			t.Error("interpolated path failed to parse: " + texts[i])
			parsed.Free()
			continue
		}
		path := parsed.Expr
		if path.Kind == expr.CommandCapture { path = path.Items[0].Items[2].Parts[0].Expr }
		if path.Kind != expr.Path || len(path.Parts) == 0 || path.Pattern != nil { t.Error("path lost its structural parts") }
		cloned := expr.Clone(a, parsed.Expr)
		formatted := expr.Format(a, cloned)
		again := expr.Parse(a, "formatted.km", formatted)
		if len(again.Diagnostics) != 0 { t.Error("formatted path did not reparse") }
		canonical := expr.Format(a, again.Expr)
		if canonical != formatted { t.Error("path formatting was not idempotent") }
		mem.FreeString(a, canonical)
		again.Free()
		mem.FreeString(a, formatted)
		expr.Free(a, cloned)
		parsed.Free()
	}
}

func TestInterpolatedPathsRejectMissingBoundaries(t *testing.T) {
	texts := []string{`@(root)`, `@(root)suffix`, `./@()/file`, `./@(name`, `./@(nop "unclosed)/file`, `./@(root`, `$(printf ./@(name)"suffix")`}
	for i := range texts {
		parsed := expr.Parse(t.Allocator(), "invalid.km", texts[i])
		if len(parsed.Diagnostics) == 0 { t.Error("malformed path was accepted") }
		parsed.Free()
	}
}

func TestGlobCharacterClassesKeepContainerBoundaries(t *testing.T) {
	a := t.Allocator()
	parsed := expr.Parse(a, "glob.km", `[./journal/[ab].md ./journal/[!x].md ./journal/[a\]].md ./journal/[@(get [name: "b"] "name")]]`)
	if len(parsed.Diagnostics) != 0 || parsed.Expr == nil || len(parsed.Expr.Items) != 4 { t.Error("glob classes consumed list delimiters"); parsed.Free(); return }
	formatted := expr.Format(a, parsed.Expr)
	again := expr.Parse(a, "formatted.km", formatted)
	if len(again.Diagnostics) != 0 || len(again.Expr.Items) != 4 { t.Error("formatted glob classes changed boundaries") }
	again.Free()
	mem.FreeString(a, formatted)
	parsed.Free()
}
