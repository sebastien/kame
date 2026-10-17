package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestProcessGroupsAndMuxRoundTrip(t *testing.T) {
	texts := []string{
		"$((printf a, printf b) |(merge) cat)",
		"$(printf a |(broadcast) (cat > ./left, cat > ./right))",
		"$((printf a | cat, (printf b, printf c) |(merge) cat) |(merge) cat)",
		"$((printf \"a,b\" (nop [1 2]), printf %s $(printf \"(x,y)\")) |(merge) cat)",
		"$((printf %s ./a{b,c}, printf %s ./@(nop \"value\")) |(merge) cat)",
		"$(printf a |(frame :lines) |(filter (eq item \"a\") | cat > ./selected) | cat)",
		"$(printf a |(frame :lines) |(filter :true | cat |(frame :lines) >&) | cat)",
		"$(printf a |(frame :lines) |(filter :true | cat |(frame :lines) >@) |(filter :false | cat > ./selected) | cat)",
		"$((printf a |(frame :lines), printf b |(frame :lines)) |(merge) |(encode :lines) | cat)",
		"$(printf a |(frame :lines) |(filter :true |(encode :lines) | cat > ./selected) | cat)",
		"$((printf a >&reports, &reports | cat))",
		"$(printf a |(broadcast) (>&left, >&right))",
		"$((printf a |(frame :lines) |(filter :true | cat >&reports) | cat, &reports | cat))",
		"$(diff -u <(printf a) <(printf b))",
		"$(printf a | cat <&)",
		"$(printf a | cat < ./stdin <& <(printf b))",
		"$(cat <&reports)",
	}
	for i := range texts {
		r := expr.Parse(t.Allocator(), "graph.km", texts[i])
		if len(r.Diagnostics) != 0 { t.Error("process group failed to parse"); r.Free(); continue }
		clone := expr.Clone(t.Allocator(), r.Expr)
		formatted := expr.Format(t.Allocator(), clone)
		expr.Free(t.Allocator(), clone)
		copy := expr.Parse(t.Allocator(), "formatted.km", formatted)
		if len(copy.Diagnostics) != 0 { t.Error("formatted graph did not reparse") }
		again := expr.Format(t.Allocator(), copy.Expr)
		if again != formatted { t.Errorf("graph formatting is not idempotent: %s -> %s", formatted, again) }
		mem.FreeString(t.Allocator(), formatted); mem.FreeString(t.Allocator(), again)
		copy.Free(); r.Free()
	}
}

func TestProcessGroupDiscriminationAndMalformedGraphs(t *testing.T) {
	values := []string{"(nop [1 2])", "(str \"a,b\")", "(nop $(printf \"a,b\"))", "(nop ./a{b,c})"}
	for i := range values { if expr.IsCommandGroup(values[i], 0) { t.Error("value expression was classified as a process group") } }
	invalid := []string{"$((printf a,) |(merge) cat)", "$((, printf b) |(merge) cat)", "$((printf a, printf b)", "$(printf a |(unknown) cat)", "$(printf a |(broadcast))", "$(printf a 2>&1)"}
	for i := range invalid {
		r := expr.Parse(t.Allocator(), "invalid.km", invalid[i])
		if len(r.Diagnostics) == 0 { t.Error("malformed process graph was accepted") }
		r.Free()
	}
}
