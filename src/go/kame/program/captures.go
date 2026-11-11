package program

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/template"

	"strconv"
)

// Captures are lexical values in both prerequisite expressions and recipes.
func (p *Program) ruleScope(c *eval.Context, captures []template.CaptureValue, arguments []ArgumentValue) *eval.Scope {
	var fields []core.RecordField
	var positionalNames []string
	for i := range captures {
		if captures[i].Name != "" {
			duplicate := false
			for j := 0; j < i; j++ {
				if captures[j].Name == captures[i].Name {
					duplicate = true
					break
				}
			}
			if !duplicate {
				fields = append(fields, core.RecordField{Key: captures[i].Name, Value: core.NewString(p.Alloc, captures[i].Text)})
			}
		}

		key := cloneText(p.Alloc, "_"+strconv.Itoa(i))
		duplicate := false
		for j := range fields {
			if fields[j].Key == key {
				duplicate = true
				break
			}
		}
		if duplicate {
			mem.FreeString(p.Alloc, key)
			continue
		}
		positionalNames = append(positionalNames, key)
		fields = append(fields, core.RecordField{Key: key, Value: core.NewString(p.Alloc, captures[i].Text)})
	}
	for i := range arguments {
		fields = append(fields, core.RecordField{Key: arguments[i].Name, Value: core.NewString(p.Alloc, arguments[i].Value)})
	}
	payload := core.NewRecord(p.Alloc, fields)
	for i := range fields {
		fields[i].Value.Free(p.Alloc)
	}
	for i := range positionalNames {
		mem.FreeString(p.Alloc, positionalNames[i])
	}
	mem.FreeSlice(nil, positionalNames)
	mem.FreeSlice(nil, fields)
	scope := eval.RenderChildScope(c, p.Eval.Scope, payload)
	payload.Free(p.Alloc)
	return scope
}
