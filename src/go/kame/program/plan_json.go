package program

import (
	"kame/lang/expr"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

// WritePlan emits the schema-1 plan document the CLI prints. It is portable so
// the freestanding host produces identical JSON.
func WritePlan(out io.Writer, plan *Plan) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("plan")
	e.Str("target")
	e.Str(plan.Target)
	if len(plan.Tools) > 0 {
		e.Str("tools"); e.BeginObject()
		for i := range plan.Tools { e.Str(plan.Tools[i].Name); e.Str(plan.Tools[i].Path) }
		e.EndObject()
	}
	if len(plan.Configuration) > 0 {
		e.Str("configuration")
		e.BeginObject()
		for i := range plan.Configuration {
			equal := strings.IndexByte(plan.Configuration[i], '=')
			e.Str(plan.Configuration[i][:equal]); e.Str(plan.Configuration[i][equal+1:])
		}
		e.EndObject()
	}
	e.Str("inputs")
	writeStringArray(&e, plan.Inputs)
 ordered := false
 for i := range plan.ResourceInputs { if plan.ResourceInputs[i].OrderOnly { ordered = true } }
 if ordered {
  e.Str("orderOnlyInputs"); e.BeginArray()
  for i := range plan.ResourceInputs { if plan.ResourceInputs[i].OrderOnly { e.Str(plan.ResourceInputs[i].Display) } }
  e.EndArray()
 }
	e.Str("outputs")
	writeStringArray(&e, plan.Outputs)
	e.Str("captures")
	e.BeginArray()
	for i := range plan.Captures {
		e.BeginObject()
		e.Str("name")
		e.Str(plan.Captures[i].Name)
		e.Str("value")
		e.Str(plan.Captures[i].Text)
		e.EndObject()
	}
	e.EndArray()
	e.Str("freshness")
	if plan.Freshness == Fresh {
		e.Str("fresh")
	} else if plan.Freshness == Stale {
		e.Str("stale")
	} else {
		e.Str("unknown")
	}
	if plan.Rule != nil {
		if plan.Rule.Metadata != nil {
			metadata := expr.Format(mem.System, plan.Rule.Metadata)
			e.Str("metadata"); e.Str(metadata)
			mem.FreeString(mem.System, metadata)
		}
  if len(plan.Rule.Environment) != 0 {
   e.Str("environment"); e.BeginArray()
   for i := range plan.Rule.Environment { e.Str(plan.Rule.Environment[i].Value) }
   e.EndArray()
  }
		if plan.Rule.Always {
			e.Str("always"); e.Bool(true)
		}
		e.Str("rule")
		e.BeginObject()
		e.Str("start")
		e.Int(int64(plan.RuleSpan.Start))
		e.Str("end")
		e.Int(int64(plan.RuleSpan.End))
		e.EndObject()
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func writeStringArray(e *json.Encoder, values []string) {
	e.BeginArray()
	for i := range values {
		e.Str(values[i])
	}
	e.EndArray()
}
