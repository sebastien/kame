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

func TestSchedulingInspectionReportsEffectiveClosedPolicies(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil)
	defer buffer.Free()
	text := "printf $(touch ./must-not-run) |(frame :lines) |(balance :buffer 4096 :strategy round-robin) (cat, cat) |(merge :buffer 8192) cat"
	if ast.WriteAST(&buffer, "kash", "scheduling.kash", text) != 0 { t.Error("scheduling failed inspection") }
	fields := []string{"\"kind\":\"balance\"", "\"bufferBytes\":4096", "\"bufferBytes\":8192", "\"strategy\":\"round-robin\"", "\"strategy\":\"round-robin-ready\"", "\"overflow\":\"block\"", "\"failure\":\"fail-fast\"", "\"outputCount\":2"}
	for _, field := range fields { if !strings.Contains(buffer.String(), field) { t.Errorf("missing scheduling metadata: %s", field) } }
}

func TestLossyInspectionReportsPendingAndProtectedFlightBounds(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil)
	defer buffer.Free()
	text := "printf $(touch ./must-not-run) |(frame :lines) |(broadcast :buffer 4096 :items 8 :overflow drop-old) (cat, cat)"
	if ast.WriteAST(&buffer, "kash", "lossy.kash", text) != 0 { t.Error("lossy topology failed inspection") }
	fields := []string{"\"overflow\":\"drop-old\"", "\"queueItems\":8", "\"queueBufferBytes\":4096", "\"inFlightRecords\":1"}
	for _, field := range fields { if !strings.Contains(buffer.String(), field) { t.Errorf("missing lossy bound: %s", field) } }
}

func TestOrderedMergeInspectionRetainsStrictPolicyWithoutEffects(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil); defer buffer.Free()
	text := "(printf $(touch ./must-not-run) |(frame :lines), printf b |(frame :lines)) |(merge :order round-robin :buffer 8) |(encode :lines)"
	if ast.WriteAST(&buffer, "kash", "ordered.kash", text) != 0 { t.Error("ordered graph failed inspection") }
	if !strings.Contains(buffer.String(), "\"ordering\":\"round-robin\"") || !strings.Contains(buffer.String(), "\"bufferBytes\":8") { t.Error("ordered policy lost during preparation/clone") }
}

func TestGraphFailureCollectionInspectionAndConflicts(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil); defer buffer.Free()
	text := "(printf $(touch ./must-not-run), printf b) |(merge :failure collect) cat"
	if ast.WriteAST(&buffer, "kash", "collect.kash", text) != 0 || !strings.Contains(buffer.String(), "\"failure\":\"collect\"") { t.Error("graph-wide collection policy not inspectable") }
	other := bytes.NewBuffer(t.Allocator(), nil); defer other.Free()
	text = "(printf a, printf b) |(merge :failure collect) |(broadcast :failure fail-fast) (cat, cat)"
	if ast.WriteAST(&other, "kash", "conflict.kash", text) != 0 || !strings.Contains(other.String(), "KASH_FAILURE_POLICY") { t.Error("conflicting graph failure policies not diagnosed statically") }
}

func TestEachWorkerInspectionRequiresFramesBeforeArgumentEvaluation(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil); defer buffer.Free()
	text := "printf a |(frame :lines) |(each) cat $(touch ./must-not-run)"
	if ast.WriteAST(&buffer, "kash", "each.kash", text) != 0 || !strings.Contains(buffer.String(), "\"kind\":\"each\"") || !strings.Contains(buffer.String(), "\"maxWorkers\":1") { t.Error("per-record worker template not inspectable") }
	other := bytes.NewBuffer(t.Allocator(), nil); defer other.Free()
	text = "printf a |(each) cat $(touch ./must-not-run)"
	if ast.WriteAST(&other, "kash", "unframed.kash", text) != 0 || !strings.Contains(other.String(), "KASH_FRAME_REQUIRED") { t.Error("unframed worker not rejected statically") }
}
