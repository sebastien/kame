package ast_test

import (
	"kame/lang/ast"
	"solod.dev/so/bytes"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestComputedPatternASTOwnsInterpolationExpressions(t *testing.T) {
	b := bytes.NewBuffer(t.Allocator(), nil)
	defer b.Free()
	if ast.WriteAST(&b, "expr", "computed", `@(ROOT)/{name:*}.@(EXT)`) != 0 { t.Error("computed AST failed"); return }
	text := b.String()
	if !strings.Contains(text, `"kind":"pattern"`) || !strings.Contains(text, `"kind":"interpolation","span":{"start":0,"end":7},"index":0,"expr":`) || !strings.Contains(text, `"name":"name"`) || !strings.Contains(text, `"index":1,"expr":`) || !strings.Contains(text, `"text":"ROOT"`) || !strings.Contains(text, `"text":"EXT"`) { t.Error("computed AST omitted interpolation expression or source coordinates") }
}
