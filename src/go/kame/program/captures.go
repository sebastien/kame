package program

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/template"
	"solod.dev/so/slices"
)

// Captures are lexical values in both prerequisite expressions and recipes.
func (p *Program) ruleScope(c *eval.Context, captures []template.CaptureValue, arguments []ArgumentValue) *eval.Scope {
	var fields []core.RecordField
	for i := range captures {
		if captures[i].Name == "" {
			continue
		}
		duplicate := false
		for j := 0; j < i; j++ {
			if captures[j].Name == captures[i].Name {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		fields = slices.Append(p.Alloc, fields, core.RecordField{Key: captures[i].Name, Value: core.NewString(p.Alloc, captures[i].Text)})
	}
	for i := range arguments {
		fields = slices.Append(p.Alloc, fields, core.RecordField{Key: arguments[i].Name, Value: core.NewString(p.Alloc, arguments[i].Value)})
	}
	payload := core.NewRecord(p.Alloc, fields)
	for i := range fields {
		fields[i].Value.Free(p.Alloc)
	}
	slices.Free(p.Alloc, fields)
	scope := eval.RenderChildScope(c, p.Eval.Scope, payload)
	payload.Free(p.Alloc)
	return scope
}
