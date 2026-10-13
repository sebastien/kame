package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
	"solod.dev/so/strings"
)

func contractNoop(c *eval.Context, state any, values []core.Value) eval.Result {
	_, _, _ = c, state, values
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func TestActionMigrationPreservesBindingsAndDiagnostics(t *testing.T) {
	a := t.Allocator()
	r := eval.NewRegistry(a)
	operations.Register(r)
	engine := core.NewEngine(a)
	source := script.Parse(a, "migration.km", "")
	p := eval.Compile(a, engine, source, r)
	names := []string{"write", "yield", "out", "err", "run", "pipe", "shell", "sh", "shellrun", "shell-template", "fail"}
	for i := range names {
		name := names[i]
		texts := []string{name, "(" + name + " (fail! \"must not evaluate operands\"))"}
		for j := range texts {
			parsed := expr.Parse(a, "migration.km", texts[j])
			context := eval.Context{Program: p, Scope: p.Scope, Run: a}
			result := p.EvaluateWith(parsed.Expr, &context)
			code := "REF_MISSING"
			if j == 1 { code = "OP_UNKNOWN" }
			if result.Diagnostic.Code != code || !strings.Contains(result.Diagnostic.Message, "`" + name + "!`") || result.Diagnostic.Span.End-result.Diagnostic.Span.Start != len(name) {
				t.Error("missing action migration diagnostic: " + texts[j])
			}
			result.Free(a)
			parsed.Free()
		}
		parsed := expr.Parse(a, "migration.km", "(let [" + name + " ([x] x)] (" + name + " 42))")
		context := eval.Context{Program: p, Scope: p.Scope, Run: a}
		result := p.EvaluateWith(parsed.Expr, &context)
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 { t.Error("migration reserved a lexical binding: " + name) }
		result.Free(a)
		parsed.Free()
	}
	if !r.Add(eval.Operation{Name: "out", Call: contractNoop}) { t.Error("custom legacy-named operation rejected") }
	parsed := expr.Parse(a, "migration.km", "(out)")
	context := eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.EvaluateWith(parsed.Expr, &context)
	if result.Diagnostic.Code != "" { t.Error("migration replaced custom registration") }
	result.Free(a)
	parsed.Free()
	p.Free()
	source.Free()
	engine.Free()
	r.Free()
}

func TestEveryStandardOperationDeclaresAContract(t *testing.T) {
	r := eval.NewRegistry(t.Allocator())
	if !operations.Register(r) { t.Error("standard operation registration failed") }
	for i := range r.Items {
		o := &r.Items[i]
		if !o.HasContract() || !o.ValidContract() || !o.AllowsPhase(eval.EvaluatePhase) {
			t.Error("incomplete standard contract: " + o.Name)
		}
		if o.Category == eval.PureOperation && o.Behavior & ^eval.OperationBehavior(eval.CallsCallable) != 0 {
			t.Error("pure operation declares a direct effect: " + o.Name)
		}
		if o.Category == eval.OutputOperation || o.Category == eval.ProcessOperation || o.Category == eval.VolatileOperation {
			if o.AllowsPhase(eval.PlanningPhase) || o.AllowsPhase(eval.ResolvingPhase) {
				t.Error("runtime operation declares read-only phases: " + o.Name)
			}
		}
		if o.Category == eval.ProcessOperation {
			if len(o.Capabilities) != 1 || o.Capabilities[0] != eval.Run || o.AllowsPhase(eval.RenderingPhase) {
				t.Error("shell operation authority/phase contract changed: " + o.Name)
			}
		}
		if o.Name == "fail!" && (o.Category != eval.ControlOperation || o.Phases != eval.AllPhases || len(o.Capabilities) != 0 || o.Behavior != eval.RaisesFailure) {
			t.Error("authored failure must be available without host authority in all phases")
		}
		if o.Name == "render" && (len(o.Capabilities) != 0 || len(o.ConditionalCapabilities) != 1 || o.ConditionalCapabilities[0] != eval.Read || o.Behavior & eval.CallsCallable == 0) {
			t.Error("inline render must not unconditionally require read authority; nested effects must propagate")
		}
		if o.Name == "tool" && (len(o.Capabilities) != 0 || len(o.ConditionalCapabilities) != 0 || o.Behavior != eval.TracksDependency) {
			t.Error("internal tool metadata must not widen user read grants")
		}
	}
	r.Free()
}

func TestRegistryRejectsPartialOperationContracts(t *testing.T) {
	r := eval.NewRegistry(t.Allocator())
	cases := []eval.Operation{
		{Name: "missing-phases", Call: contractNoop, Category: eval.PureOperation},
		{Name: "missing-category", Call: contractNoop, Phases: eval.AllPhases},
		{Name: "invalid-category", Call: contractNoop, Category: eval.OperationCategory(99), Phases: eval.AllPhases},
		{Name: "invalid-phases", Call: contractNoop, Category: eval.PureOperation, Phases: eval.PhaseMask(32)},
		{Name: "invalid-behavior", Call: contractNoop, Category: eval.PureOperation, Phases: eval.AllPhases, Behavior: eval.OperationBehavior(256)},
		{Name: "hidden-action", Call: contractNoop, Category: eval.PureOperation, Phases: eval.AllPhases, Behavior: eval.StartsProcess},
		{Name: "planning-output", Call: contractNoop, Category: eval.OutputOperation, Phases: eval.AllPhases, Behavior: eval.EmitsOutput},
		{Name: "untracked-observation", Call: contractNoop, Category: eval.ObservationOperation, Phases: eval.AllPhases},
		{Name: "invalid-capability", Call: contractNoop, Category: eval.ObservationOperation, Phases: eval.AllPhases, Capabilities: []eval.Capability{eval.Capability(99)}},
		{Name: "invalid-conditional-capability", Call: contractNoop, Category: eval.ObservationOperation, Phases: eval.AllPhases, ConditionalCapabilities: []eval.Capability{eval.Capability(99)}},
	}
	for i := range cases {
		if r.Add(cases[i]) { t.Error("accepted invalid contract: " + cases[i].Name) }
	}
	if !r.Add(eval.Operation{Name: "custom", Call: contractNoop}) {
		t.Error("legacy custom registrations must retain their runtime capability checks")
	}
	if !r.Add(eval.Operation{Name: "pure!", Call: contractNoop, Category: eval.PureOperation, Phases: eval.AllPhases}) {
		t.Error("operation names must not determine effect metadata")
	}
	r.Free()
}

func TestContractMetadataDoesNotAuthorizeNestedEffects(t *testing.T) {
	a := t.Allocator()
	r := eval.NewRegistry(a)
	operations.Register(r)
	engine := core.NewEngine(a)
	source := script.Parse(a, "contracts.km", "")
	p := eval.Compile(a, engine, source, r)
	phases := []eval.Phase{eval.EvaluatePhase, eval.PlanningPhase, eval.ResolvingPhase, eval.RenderingPhase}
	for i := range phases {
		parsed := expr.Parse(a, "contracts.km", `(fail! "authored failure")`)
		context := eval.Context{Program: p, Scope: p.Scope, Run: a, Phase: phases[i]}
		result := p.EvaluateWith(parsed.Expr, &context)
		if result.Diagnostic.Code != "USER_ERROR" || context.PhaseInvalid() || len(context.Effects) != 0 {
			t.Error("control failure changed authority/phase contract")
		}
		result.Free(a)
		parsed.Free()
		parsed = expr.Parse(a, "contracts.km", `(if :false (fail! "unreached") (render """@(str "ok")""" "plain"))`)
		context = eval.Context{Program: p, Scope: p.Scope, Run: a, Phase: phases[i]}
		result = p.EvaluateWith(parsed.Expr, &context)
		if result.Diagnostic.Code != "" || result.Value.Text != "ok" || context.PhaseInvalid() {
			t.Error("inline rendering or unreachable failure acquired authority")
		}
		result.Free(a)
		parsed.Free()
	}
	parsed := expr.Parse(a, "contracts.km", `(map ([x] (out! x)) ["blocked"])`)
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, Phase: eval.PlanningPhase}
	result := p.EvaluateWith(parsed.Expr, &context)
	if !context.PhaseInvalid() || len(context.Effects) != 0 {
		t.Error("callback computation metadata permitted output while planning")
	}
	result.Free(a)
	parsed.Free()
	p.Free()
	source.Free()
	engine.Free()
	r.Free()
}
