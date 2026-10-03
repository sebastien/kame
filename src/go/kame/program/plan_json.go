package program

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
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
