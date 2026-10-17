package ast_test

import (
	"kame/lang/ast"
	"solod.dev/so/bytes"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestProcessTopologyInspectionDoesNotEvaluatePredicates(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil)
	defer buffer.Free()
	text := "printf a |(frame :lines) |(filter $(touch ./must-not-run) | cat |(frame :lines) >&) | cat"
	if ast.WriteAST(&buffer, "kash", "topology.kash", text) != 0 { t.Error("topology failed inspection") }
	fields := []string{"\"topology\"", "\"edges\"", "\"kind\":\"merge\"", "\"predicateSpan\"", "\"outputKind\":\"lines\"", "\"rejoin\":\"next\""}
	for _, field := range fields {
		if !strings.Contains(buffer.String(), field) { t.Errorf("missing topology metadata: %s", field) }
	}
}

func TestInvalidFramedRejoinStaysInspectable(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil)
	defer buffer.Free()
	if ast.WriteAST(&buffer, "kash", "invalid.kash", "printf a |(frame :lines) |(filter :true | cat >&) | cat") != 0 { t.Error("semantic framing mismatch became a parse error") }
	if !strings.Contains(buffer.String(), "KASH_FRAME_MISMATCH") { t.Error("missing static framing diagnostic") }
}
