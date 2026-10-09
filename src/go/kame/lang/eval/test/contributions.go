package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestContributionsAreLazyOrderedAndImmutable(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "contributions.km", "items ?= [0]\nitems += later\nitems ?= missing\nitems += [[3] 4]\nlater = [1 2]\nunused += missing\n")
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	p := eval.Compile(a, engine, parsed, registry)
	if !p.Valid || len(parsed.Diagnostics) != 0 {
		t.Fatal("contributions failed registration")
	}
	node := p.Definition("items")
	if node.Requested {
		t.Error("registration demanded contributions")
	}
	engine.Request(node)
	stepUntilCurrent(engine, node)
	value := node.Latest
	if !node.Current || value.Kind != core.List || len(value.List) != 5 {
		t.Fatal("contributions failed concatenation")
	}
	if value.List[0].Int != 0 || value.List[1].Int != 1 || value.List[2].Int != 2 || value.List[3].Kind != core.List || value.List[4].Int != 4 {
		t.Error("contribution order or nesting changed")
	}
	if p.Definition("unused").Requested {
		t.Error("unused contribution was demanded")
	}
	if parsed.Items[0].Definition.Aggregate || !parsed.Items[1].Definition.Contribution {
		t.Error("registration mutated authored AST")
	}
	engine.Free()
	p.Free()
	registry.Free()
	parsed.Free()
}

func TestContributionOverrideReplacesUnevaluatedOperands(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "contributions.km", "items += missing\nitems += items\n")
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	p := eval.Compile(a, engine, parsed, registry)
	if !p.OverrideDefinition("items", "literal") {
		t.Fatal("aggregate override rejected")
	}
	node := p.Definition("items")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Latest.Kind != core.String || node.Latest.Text != "literal" {
		t.Error("override evaluated original operands")
	}
	engine.Free()
	p.Free()
	registry.Free()
	parsed.Free()
}

func TestContributionFailuresRetainOrdinaryDiagnostics(t *testing.T) {
	a := t.Allocator()
	sources := []string{"items += 1", "items = 1\nitems += []", "items += items", "items += later\nlater = items", "items += []\nitems = []", "(items) = []\nitems += []"}
	codes := []string{"EXPR_INVALID", "EXPR_INVALID", "DEP_CYCLE", "DEP_CYCLE", "DEF_INVALID", "DEF_INVALID"}
	for i := range sources {
		parsed := script.Parse(a, "contributions.km", sources[i])
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		checked := eval.CompileChecked(a, engine, parsed, registry)
		if codes[i] == "DEF_INVALID" {
			if len(checked.Diagnostics) == 0 || checked.Diagnostics[0].Code != codes[i] {
				t.Error("immutable duplicate accepted")
			}
		} else {
			if checked.Program == nil {
				t.Fatal("contribution failed registration")
			}
			node := checked.Program.Definition("items")
			engine.Request(node)
			stepUntilCurrent(engine, node)
			if node.Diagnostic.Code != codes[i] {
				t.Error("unexpected contribution diagnostic")
			}
		}
		engine.Free()
		if checked.Program != nil {
			checked.Program.Free()
		}
		checked.Free(a)
		registry.Free()
		parsed.Free()
	}
}

func TestContributionOperandsSurviveSuspensionAndInvalidation(t *testing.T) {
	checkRetainedOperands(t, "result += (list (once))\nresult += (list (async))")
}

func TestScopedContributionOverrideOwnsItsLiteralSnapshot(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "contributions.km", "items += missing\nitems += items\n")
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	p := eval.Compile(a, engine, parsed, registry)
	if !p.OverrideDefinition("items", "first") {
		t.Fatal("aggregate override rejected")
	}
	c := eval.Context{Program: p, Scope: p.Scope, Run: a, HasDefinitionNamespace: true}
	c.DefinitionNamespace[0] = 1
	snapshot := p.DefinitionWith("items", &c)
	if !p.OverrideDefinition("items", "second") {
		t.Fatal("replacement override rejected")
	}
	engine.Request(snapshot)
	stepUntilCurrent(engine, snapshot)
	if !snapshot.Current || snapshot.Latest.Text != "first" {
		t.Error("scoped literal borrowed a replaced canonical AST")
	}
	canonical := p.Definition("items")
	engine.Request(canonical)
	stepUntilCurrent(engine, canonical)
	if !canonical.Current || canonical.Latest.Text != "second" {
		t.Error("canonical literal was not replaced")
	}
	engine.Free()
	p.Free()
	registry.Free()
	parsed.Free()
}
