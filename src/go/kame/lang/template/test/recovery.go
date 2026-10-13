package template_test

import (
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/testing"
)

func TestDocumentLetTransfersBranchConditionsAndPatterns(t *testing.T) {
	inputs := []string{
		"@if(:true)\n@let(x 1)\nyes\n@elif(:false)\nno\n@end(if)\n",
		"@match(x)\n@case(1)\n@let(y 2)\nyes\n@case(2)\nno\n@end(match)\n",
	}
	for i := range inputs {
		doc := template.ParseDocument(t.Allocator(), "let.tmpl", inputs[i], "plain")
		if len(doc.Diagnostics) != 0 { t.Error("let discarded the enclosing branch's terminator arguments") }
		doc.Free()
	}
}

func TestDocumentLetRetainsEndLabelDiagnostic(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "let.tmpl", "@if(:true)\n@let(x 1)\nyes\n@end(for)\n", "plain")
	defer doc.Free()
	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Code != "TPL_BLOCK" || doc.Diagnostics[0].Message != "template block end label does not match opening directive" || !sameSpan(doc.Diagnostics[0].Span, 25, 34) {
		t.Error("let discarded the end label or diagnostic span")
	}
}

func TestDocumentLetTransfersTerminatorRightTrim(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "let.tmpl", "@if(:true)\n@let(x 1)\nyes\n@elif(:false)-\n no\n@end(if)-\n tail\n", "plain")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || doc.Root == nil || len(doc.Root.Items) < 3 {
		t.Fatal("trimmed let document did not lower")
		return
	}
	branch := doc.Root.Items[1]
	if branch.Kind != expr.Application || len(branch.Items) != 5 || branch.Items[4].Kind != expr.Application || len(branch.Items[4].Items) != 2 || branch.Items[4].Items[0].Text != "template-trim-start" {
		t.Error("let discarded elif right trim")
	}
	if doc.Root.Items[2].Kind != expr.Application || len(doc.Root.Items[2].Items) != 2 || doc.Root.Items[2].Items[0].Text != "template-trim-start" { t.Error("end right trim was not retained") }
	ending := template.ParseDocument(t.Allocator(), "end-trim.tmpl", "@if(:true)\n@let(x 1)\nyes\n@end(if)-\n tail\n", "plain")
	defer ending.Free()
	if len(ending.Diagnostics) != 0 || len(ending.Root.Items) != 3 || ending.Root.Items[2].Kind != expr.Application || len(ending.Root.Items[2].Items) != 2 || ending.Root.Items[2].Items[0].Text != "template-trim-start" { t.Error("let discarded end right trim") }
}

func TestDocumentRecoveryFreesDiscardedTrees(t *testing.T) {
	inputs := []string{"hello @(name", "@match(x)\n@case(1)\nyes\n@else\nno\n", "@match(x)\n@case(1)\nyes\n@else\nno\n@case(2)\n"}
	for i := range inputs {
		doc := template.ParseDocument(t.Allocator(), "recovery.tmpl", inputs[i], "plain")
		if i != 0 && len(doc.Diagnostics) == 0 { t.Error("invalid match fallback was accepted") }
		doc.Free()
	}
}

func TestDocumentNewerInputSelectorRetainsSuffix(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "selector.tmpl", "@<?", "plain")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || len(doc.Root.Items) != 2 || doc.Root.Items[1].Kind != expr.Selector || doc.Root.Items[1].Text != "@<?" || !sameSpan(doc.Root.Items[1].Span, 0, 3) {
		t.Error("document changed the newer-input selector into a first-input selector")
	}
}

func TestStringRangeBoundsSelectorsAndExpressions(t *testing.T) {
	a := t.Allocator()
	inputs := []string{"@<123", "@(name) outside", "@(f name) outside"}
	ends := []int{2, 4, 5}
	for i := range inputs {
		s := source.New(a, "range.tmpl", inputs[i])
		parsed := template.ParseStringRange(a, s, 0, ends[i])
		for j := range parsed.Parts {
			if parsed.Parts[j].Span.End > ends[i] { t.Error("template part escaped its requested range") }
		}
		for j := range parsed.Diagnostics {
			if parsed.Diagnostics[j].Span.End > ends[i] { t.Error("template diagnostic escaped its requested range") }
		}
		if i == 0 && (len(parsed.Parts) != 1 || parsed.Parts[0].Text != "@<") { t.Error("selector scanned outside requested range") }
		parsed.Free()
		s.Free(a)
	}
}
