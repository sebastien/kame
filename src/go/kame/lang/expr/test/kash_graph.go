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
		"$(printf a >&out)",
		"$(printf a >&err)",
		"$(printf a |(broadcast) (>&out, >&err))",
		"$((printf a |(frame :lines) |(filter :true | cat >&reports) | cat, &reports | cat))",
		"$(diff -u <(printf a) <(printf b))",
		"$(printf a | cat <&)",
		"$(printf a | cat < ./stdin <& <(printf b))",
		"$(cat <&reports)",
		"$(printf a |(frame :lines) |(balance) (cat, cat))",
		"$(printf a |(frame :lines) |(balance :buffer 4096 :strategy round-robin) (cat, cat))",
		"$((printf a, printf b) |(merge :buffer 1) cat)",
		"$(printf a |(broadcast :buffer 65536) (cat, cat))",
		"$(printf a |(frame :lines) |(broadcast :overflow drop-new :items 8 :buffer 4096) (cat, cat))",
		"$(printf a |(frame :lines) |(broadcast :overflow drop-old) (cat, cat))",
		"$(printf a |(frame :lines) |(broadcast :overflow latest :items 1) (cat, cat))",
		"$((printf a, printf b) |(merge :overflow block) cat)",
		"$((printf a |(frame :lines), printf b |(frame :lines)) |(merge :order round-robin :buffer 8) cat)",
		"$((printf a, printf b) |(merge :order ready) cat)",
		"$((printf a, printf b) |(merge :failure collect) cat)",
		"$(printf a |(broadcast :failure fail-fast) (cat, cat))",
		"$(printf a |(frame :lines) |(each) cat)",
		"$(printf a |(frame :lines) |(balance) (|(each) cat, |(each) cat))",
		"$(printf a |(frame :lines) |(each) sh -c \"cat; printf done\" | cat)",
	}
	for i := range texts {
		r := expr.Parse(t.Allocator(), "graph.km", texts[i])
		if len(r.Diagnostics) != 0 {
			t.Error("process group failed to parse")
			r.Free()
			continue
		}
		clone := expr.Clone(t.Allocator(), r.Expr)
		formatted := expr.Format(t.Allocator(), clone)
		expr.Free(t.Allocator(), clone)
		copy := expr.Parse(t.Allocator(), "formatted.km", formatted)
		if len(copy.Diagnostics) != 0 {
			t.Error("formatted graph did not reparse")
		}
		again := expr.Format(t.Allocator(), copy.Expr)
		if again != formatted {
			t.Errorf("graph formatting is not idempotent: %s -> %s", formatted, again)
		}
		mem.FreeString(t.Allocator(), formatted)
		mem.FreeString(t.Allocator(), again)
		copy.Free()
		r.Free()
	}
}

func TestCommandListParseFormatBoundaries(t *testing.T) {
	a := t.Allocator()
	input := "$(printf a&&printf b ||\nprintf c)"
	r := expr.Parse(a, "list.km", input)
	if len(r.Diagnostics) != 0 || r.Expr.Kind != expr.CommandList || len(r.Expr.Items) != 3 || len(r.Expr.CommandOps) != 2 {
		t.Error("command list lost its operands or operators")
		r.Free()
		return
	}
	if r.Expr.CommandOps[0].Text != "&&" || r.Expr.CommandOps[1].Text != "||" || !r.Expr.CommandOps[1].AfterNewline {
		t.Error("command list lost operator order or multiline boundary")
	}
	formatted := expr.Format(a, r.Expr)
	copy := expr.Parse(a, "formatted.km", formatted)
	if len(copy.Diagnostics) != 0 {
		t.Error("formatted command list did not reparse")
	} else {
		again := expr.Format(a, copy.Expr)
		if again != formatted {
			t.Error("command-list formatting is not idempotent")
		}
		mem.FreeString(a, again)
	}
	copy.Free()
	mem.FreeString(a, formatted)
	r.Free()

	valid := []string{
		"$(printf %s \"&&\")",
		"$(printf %s (str \"||\"))",
		"$(printf a ? && printf b)",
	}
	for i := range valid {
		parsed := expr.Parse(a, "valid-list.km", valid[i])
		if len(parsed.Diagnostics) != 0 {
			t.Error("quoted, embedded, or accepted command-list boundary failed to parse")
		}
		parsed.Free()
	}
	invalid := []string{
		"$(printf a\n || printf b)",
		"$((printf a&&printf b, printf c))",
		"$(printf a ||)",
		"$(printf a && printf b &)",
	}
	for i := range invalid {
		parsed := expr.Parse(a, "invalid-list.km", invalid[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid command-list boundary was accepted")
		}
		parsed.Free()
	}
}

func TestProcessGroupDiscriminationAndMalformedGraphs(t *testing.T) {
	values := []string{"(nop [1 2])", "(str \"a,b\")", "(nop $(printf \"a,b\"))", "(nop ./a{b,c})"}
	for i := range values {
		if expr.IsCommandGroup(values[i], 0) {
			t.Error("value expression was classified as a process group")
		}
	}
	invalid := []string{"$((printf a,) |(merge) cat)", "$((, printf b) |(merge) cat)", "$((printf a, printf b)", "$(printf a |(unknown) cat)", "$(printf a |(broadcast))", "$(printf a 2>&1)", "$(cat <&out)", "$(printf a >&in)", "$(printf a |(balance))", "$(printf a |(broadcast :buffer 0) (cat, cat))", "$(printf a |(broadcast :buffer 65537) (cat, cat))", "$(printf a |(balance :strategy random) (cat, cat))", "$(printf a |(merge :strategy round-robin) cat)", "$(printf a |(balance :buffer (read! ./config)) (cat, cat))", "$(printf a |(balance :buffer 4 :buffer 8) (cat, cat))"}
	for i := range invalid {
		r := expr.Parse(t.Allocator(), "invalid.km", invalid[i])
		if len(r.Diagnostics) == 0 {
			t.Error("malformed process graph was accepted")
		}
		r.Free()
	}
}
