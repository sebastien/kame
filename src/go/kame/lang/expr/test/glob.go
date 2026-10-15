package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/testing"
)

func TestValidGlob(t *testing.T) {
	valid := []string{"*", "**", "?", "literal", `\{\}\:\[`, "[a-z]", "[!a-c-a]", "[-a]", "[a-]", "[a-a]", "[}]", "[:]", "[[a]", "a]"}
	invalid := []string{"", `\`, "{", "}", ":", "[", "[]", "[!", "[!]", "[!]a]", "[a", "[z-a]", "[!z-a]", "[a-z][9-0]"}
	for i := range valid {
		if !expr.ValidGlob(valid[i]) { t.Errorf("ValidGlob(%q) = false", valid[i]) }
	}
	for i := range invalid {
		if expr.ValidGlob(invalid[i]) { t.Errorf("ValidGlob(%q) = true", invalid[i]) }
	}
}
