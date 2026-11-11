package astcheck

import (
	"bytes"
	"kame/internal/testing"
	"kame/lang/ast"
	"strings"
)

func TestComputedPatternASTOwnsInterpolationExpressions(t *testing.T) {
	b := bytes.Buffer{}
	defer b.Reset()
	if ast.WriteAST(&b, "expr", "computed", `@{ROOT}/{name:*}.@{EXT}`) != 0 {
		t.Error("computed AST failed")
		return
	}
	text := b.String()
	if !strings.Contains(text, `"kind":"pattern"`) || !strings.Contains(text, `"kind":"interpolation","span":{"start":0,"end":7},"index":0,"expr":`) || !strings.Contains(text, `"name":"name"`) || !strings.Contains(text, `"index":1,"expr":`) || !strings.Contains(text, `"text":"ROOT"`) || !strings.Contains(text, `"text":"EXT"`) {
		t.Error("computed AST omitted interpolation expression or source coordinates")
	}
}
