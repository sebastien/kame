package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type stderrCase struct {
	text  string
	order []string
	alias bool // the form needs 2>&1 pinned after a separate writer node
}

type teeCase struct {
	text   string
	append bool
}

// redirections collects the redirection operators of a pipeline in authored
// order, which is what decides stdout and stderr destination identity.
type redirections struct {
	texts [8]string
	count int
	alias bool // a 2>&1 followed an authored stdout disposition
}

func stageRedirections(e *expr.Expr) redirections {
	var list redirections
	for i := range e.Items {
		for j := range e.Items[i].Items {
			item := e.Items[i].Items[j]
			if item.Kind != expr.CommandRedirection || list.count == len(list.texts) {
				continue
			}
			list.texts[list.count] = item.Text
			list.count++
			if item.Text == "2>&1" {
				list.alias = item.Bool
			}
		}
	}
	return list
}

func TestStderrRedirectionPreservesAuthoredOrder(t *testing.T) {
	a := t.Allocator()
	// The alias copies whichever stdout destination this position sees, so the
	// authored order must survive parsing and formatting unchanged.
	cases := []stderrCase{
		// Same-stage order carries the meaning, so no alias bit is needed.
		{"$(printf a > ./f 2>&1)", []string{">", "2>&1"}, false},
		{"$(printf a 2>&1 > ./f)", []string{"2>&1", ">"}, false},
		{"$(printf a > ./f 2>> ./e)", []string{">", "2>>"}, false},
		{"$(printf a 2> ./e)", []string{"2>"}, false},
		// A channel writer is its own node, so the alias must record that it
		// follows it rather than the pipeline's presentation stdout.
		{"$(printf a >&out 2>&1)", []string{"2>&1"}, true},
	}
	for i := range cases {
		r := expr.Parse(a, "stderr.km", cases[i].text)
		if len(r.Diagnostics) != 0 {
			t.Errorf("stderr redirection failed to parse: %s", cases[i].text)
			r.Free()
			continue
		}
		list := stageRedirections(r.Expr)
		if list.count != len(cases[i].order) {
			t.Errorf("stderr redirection count changed: %s", cases[i].text)
		} else {
			for j := range cases[i].order {
				if list.texts[j] != cases[i].order[j] {
					t.Errorf("stderr redirection order changed: %s", cases[i].text)
				}
			}
			if list.alias != cases[i].alias {
				t.Errorf("stderr alias lost its position relative to stdout: %s", cases[i].text)
			}
		}
		formatted := expr.Format(a, r.Expr)
		copy := expr.Parse(a, "formatted.km", formatted)
		if len(copy.Diagnostics) != 0 {
			t.Errorf("formatted stderr redirection did not reparse: %s", formatted)
		} else {
			again := expr.Format(a, copy.Expr)
			if again != formatted {
				t.Errorf("stderr formatting is not idempotent: %s -> %s", formatted, again)
			}
			mem.FreeString(a, again)
			reparsed := stageRedirections(copy.Expr)
			if reparsed.count != list.count {
				t.Errorf("formatting changed the stderr redirection count: %s", formatted)
			} else {
				for j := range list.count {
					if reparsed.texts[j] != list.texts[j] {
						t.Errorf("formatting reordered stderr redirection: %s", formatted)
					}
				}
				if reparsed.alias != list.alias {
					t.Errorf("formatting moved the stderr alias across stdout: %s", formatted)
				}
			}
		}
		copy.Free()
		mem.FreeString(a, formatted)
		r.Free()
	}
}

func TestTeeOperatorKeepsPathAndAppendFlag(t *testing.T) {
	a := t.Allocator()
	cases := []teeCase{
		{"$(printf a |(tee ./log) | cat)", false},
		{"$(printf a |(tee :append ./log) | cat)", true},
		{"$(printf a |(tee ./log))", false},
	}
	for i := range cases {
		r := expr.Parse(a, "tee.km", cases[i].text)
		if len(r.Diagnostics) != 0 {
			t.Errorf("tee failed to parse: %s", cases[i].text)
			r.Free()
			continue
		}
		found, appendFlag := false, false
		for j := range r.Expr.Items {
			if r.Expr.Items[j].Kind != expr.CommandMux || r.Expr.Items[j].Text != "tee" {
				continue
			}
			found, appendFlag = true, r.Expr.Items[j].Bool
			if len(r.Expr.Items[j].Items) != 1 {
				t.Errorf("tee lost its destination argument: %s", cases[i].text)
			}
		}
		if !found || appendFlag != cases[i].append {
			t.Errorf("tee operator or :append flag was lost: %s", cases[i].text)
		}
		formatted := expr.Format(a, r.Expr)
		if formatted != cases[i].text {
			t.Errorf("tee formatting changed the authored operator: %s -> %s", cases[i].text, formatted)
		}
		mem.FreeString(a, formatted)
		r.Free()
	}
}

func TestStderrDescriptorBoundaries(t *testing.T) {
	a := t.Allocator()
	// A descriptor must be unquoted and glued to '>'; otherwise it is an
	// argument or string data, never a redirection.
	arguments := []string{"$(printf \"%s\" 2 > ./out)", "$(printf \"%s\" \"2>\" > ./out)"}
	for i := range arguments {
		r := expr.Parse(a, "argument.km", arguments[i])
		if len(r.Diagnostics) != 0 {
			t.Errorf("descriptor-adjacent argument failed to parse: %s", arguments[i])
		} else if list := stageRedirections(r.Expr); list.count != 1 || list.texts[0] != ">" {
			t.Errorf("descriptor-shaped argument became a redirection: %s", arguments[i])
		}
		r.Free()
	}
	invalid := []string{
		"$(printf a 3> ./out)",
		"$(printf a 1>&2)",
		"$(printf a 2>&-)",
		"$(printf a 2>&1 2>&1)",
		"$(printf a 2> ./e 2>> ./f)",
		"$(printf a 2>)",
		"$(printf a 2> ./e > ./f | cat)",
		"$(printf a |(tee))",
		"$(printf a |(tee :append))",
	}
	for i := range invalid {
		r := expr.Parse(a, "invalid-stderr.km", invalid[i])
		if len(r.Diagnostics) == 0 {
			t.Errorf("unsupported stderr form was accepted: %s", invalid[i])
		}
		r.Free()
	}
}
