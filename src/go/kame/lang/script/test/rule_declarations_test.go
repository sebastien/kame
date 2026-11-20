package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"strings"
	"testing"
)

func TestKeywordRuleDeclarations(t *testing.T) {
	const text = "task clean :\n\tprintf clean\ntask cached lint : ./a ./b, ./c | ./ready ; {env: {MODE: mode}}\nservice dev {port=8080}\n\tserve $port\nrule always ./out =\n\tvalue + 1 # result\nversion = 2\n"
	authored := source.New(nil, "declarations.km", text)
	defer authored.Free(nil)
	parsed := script.ParseFragment(nil, authored, "km", 0, len(text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 5 {
		t.Fatalf("declarations failed to parse: %v", parsed.Diagnostics)
	}
	task, cached, service, file := parsed.Items[0].Rule, parsed.Items[1].Rule, parsed.Items[2].Rule, parsed.Items[3].Rule
	if task.Kind != rule.TaskRule || len(task.Inputs) != 0 || len(task.Body) != 1 {
		t.Errorf("wrong phony task: %+v", task)
	}
	if cached.Kind != rule.CachedTaskRule || len(cached.Inputs) != 4 || !cached.Inputs[1].SequenceEnd || !cached.Inputs[3].OrderOnly || cached.Metadata == nil || cached.Metadata.Kind != expr.Record {
		t.Errorf("wrong cached task: %+v", cached)
	}
	if service.Kind != rule.ServiceRule || len(service.Arguments) != 1 || service.Arguments[0].Default != "8080" {
		t.Errorf("wrong service: %+v", service)
	}
	if !file.Always || file.Kind != rule.FileRule || file.BodyExpression == nil || file.BodyExpression.Infix != "+" || file.HeaderComment != "# result" {
		t.Errorf("wrong expression body: %+v", file)
	}
	formatted := script.Format(nil, parsed)
	want := strings.Replace(text, "task clean :", "task clean", 1)
	want = strings.Replace(want, " =\n\tvalue", " = value", 1)
	if formatted != want {
		t.Fatalf("formatted\n%s\nwant\n%s", formatted, want)
	}
	second := source.New(nil, "formatted.km", formatted)
	defer second.Free(nil)
	again := script.ParseFragment(nil, second, "km", 0, len(formatted))
	defer again.Free()
	if len(again.Diagnostics) != 0 || script.Format(nil, again) != formatted {
		t.Fatalf("keyword declaration format was not idempotent: %v", again.Diagnostics)
	}
}

func TestConditionalRulesKeepSiblingAndRecipeBoundaries(t *testing.T) {
	const input = "if true\n  task first\n    echo first\n  rule ./artifact = \"value\"\n  if true\n    task default : first ./artifact\nelse\n  task default\nvalue = 42\n"
	const want = "if true\n\ttask first\n\t\techo first\n\trule ./artifact = \"value\"\n\tif true\n\t\ttask default : first ./artifact\nelse\n\ttask default\nvalue = 42\n"
	for _, text := range []string{input, want} {
		authored := source.New(nil, "conditional.km", text)
		defer authored.Free(nil)
		parsed := script.ParseFragment(nil, authored, script.KameFileLanguage, 0, len(text))
		defer parsed.Free()
		if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
			t.Fatalf("conditional declarations failed to parse: %v", parsed.Diagnostics)
		}
		branches := parsed.Items[0].Branches
		if len(branches) != 2 || len(branches[0].Items) != 3 || len(branches[0].Items[0].Rule.Body) != 1 || len(branches[1].Items) != 1 {
			t.Fatalf("conditional rule consumed a sibling declaration: %+v", branches)
		}
		if formatted := script.Format(nil, parsed); formatted != want {
			t.Fatalf("formatted conditional as %q, want %q", formatted, want)
		}
	}
}

func TestKeywordRuleDeclarationRejectsMalformedHeaders(t *testing.T) {
	for _, text := range []string{
		"rule artifact",
		"task ./artifact",
		"task fresh : ,",
		"task fresh ; env \"NAME=value\"",
		"service dev {port=1} {port=2}",
		"rule ./artifact =\nvalue = 42\n",
	} {
		t.Run(text, func(t *testing.T) {
			authored := source.New(nil, "invalid.km", text)
			defer authored.Free(nil)
			parsed := script.ParseFragment(nil, authored, "km", 0, len(text))
			defer parsed.Free()
			if len(parsed.Diagnostics) == 0 {
				t.Fatal("malformed declaration was accepted")
			}
		})
	}
}

func TestKeywordRuleFormatsExpandedMetadataAndKeepsPathPunctuation(t *testing.T) {
	value := "\"" + strings.Repeat("x", 90) + "\""
	text := "rule ./file:part ; {shell: " + value + ", env: {MODE: \"dev\"}}\n"
	authored := source.New(nil, "declarations.km", text)
	defer authored.Free(nil)
	parsed := script.ParseFragment(nil, authored, "km", 0, len(text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 || parsed.Items[0].Rule.Outputs[0].Text != "./file:part" {
		t.Fatalf("path punctuation or metadata did not parse: %v", parsed.Diagnostics)
	}
	formatted := script.Format(nil, parsed)
	if !strings.Contains(formatted, "\n\tenv: {MODE: \"dev\"},\n}\n") {
		t.Fatalf("expanded metadata used the wrong indentation: %q", formatted)
	}
	reparsed := source.New(nil, "formatted.km", formatted)
	defer reparsed.Free(nil)
	again := script.ParseFragment(nil, reparsed, "km", 0, len(formatted))
	defer again.Free()
	if len(again.Diagnostics) != 0 || script.Format(nil, again) != formatted {
		t.Fatalf("multiline metadata was not idempotent: %v", again.Diagnostics)
	}
}

func TestRuleHeaderKeepsEscapedPathBytesInOneOperand(t *testing.T) {
	const text = "rule ./has\\ space : ./literal\\* ./literal\\,\n"
	authored := source.New(nil, "escaped.km", text)
	defer authored.Free(nil)
	parsed := script.ParseFragment(nil, authored, "km", 0, len(text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 1 {
		t.Fatalf("escaped rule operands failed to parse: %v", parsed.Diagnostics)
	}
	r := parsed.Items[0].Rule
	if len(r.Outputs) != 1 || r.Outputs[0].Text != "./has\\ space" ||
		len(r.Inputs) != 2 || r.Inputs[0].Kind != rule.InputPath || r.Inputs[1].SequenceEnd {
		t.Fatalf("escaped rule operands changed meaning: %+v", r)
	}
	if formatted := script.Format(nil, parsed); formatted != text {
		t.Errorf("escaped rule operands formatted as %q", formatted)
	}
}
