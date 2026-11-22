package operations

import (
	"encoding/json"
	"kame/core"
	"kame/lang/eval"
	"kame/lang/usage"
)

// Command specs (spec 058) cross into Kame as records through their JSON form.
// ponytail: encoding/json round trip; convert fields directly if specs get hot.

func specValue(c *eval.Context, spec usage.Spec) eval.Result {
	data, err := json.Marshal(spec)
	var value core.Value
	if err != nil || !core.ParseJSON(c.Run, data, &value) {
		return failure("EXPR_INVALID", "cannot represent the command spec")
	}
	return eval.Result{Value: value}
}

func valueSpec(c *eval.Context, value core.Value) (usage.Spec, bool) {
	var data []byte
	var spec usage.Spec
	if value.Kind != core.Record || !core.ValueJSON(c.Run, value, &data) {
		return spec, false
	}
	return spec, json.Unmarshal(data, &spec) == nil
}

func textArgument(v core.Value) (string, bool) {
	switch v.Kind {
	case core.String:
		return v.Text, true
	case core.Bytes:
		return string(v.Bytes), true
	}
	return "", false
}

func opParseUsage(c *eval.Context, _ any, v []core.Value) eval.Result {
	text, ok := textArgument(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	spec, err := usage.ParseKDL(text)
	if err != nil {
		return ownedFailure(c.Run, "PARSE_ERR", "invalid Usage spec: "+err.Error())
	}
	return specValue(c, spec)
}

func opRenderUsage(c *eval.Context, _ any, v []core.Value) eval.Result {
	spec, ok := valueSpec(c, v[0])
	if !ok {
		return invalidArgument(c, v, 0, "command spec record")
	}
	return eval.Result{Value: core.NewString(c.Run, usage.RenderKDL(spec))}
}

func opMergeUsage(c *eval.Context, _ any, v []core.Value) eval.Result {
	if v[0].Kind != core.List {
		return invalidArgument(c, v, 0, "list")
	}
	specs := make([]usage.Spec, 0, len(v[0].List))
	for _, item := range v[0].List {
		if item.Kind == core.Nil {
			continue
		}
		spec, ok := valueSpec(c, item)
		if !ok {
			return invalidArgument(c, v, 0, "list of command spec records")
		}
		specs = append(specs, spec)
	}
	return specValue(c, usage.Merge(specs...))
}

func opParseMan(c *eval.Context, _ any, v []core.Value) eval.Result {
	text, ok := textArgument(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string or bytes")
	}
	// Discovered pages are not the user's to fix: an unreadable one adds nothing.
	page, err := usage.DecodeMan([]byte(text))
	if err != nil {
		return specValue(c, usage.Spec{})
	}
	return specValue(c, usage.ParseMan(page))
}

func opManRedirect(c *eval.Context, _ any, v []core.Value) eval.Result {
	text, ok := textArgument(v[0])
	if !ok {
		return invalidArgument(c, v, 0, "string or bytes")
	}
	page, err := usage.DecodeMan([]byte(text))
	if target := usage.ManRedirect(page); err == nil && target != "" {
		return eval.Result{Value: core.NewString(c.Run, target)}
	}
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func opParseFishCompletions(c *eval.Context, _ any, v []core.Value) eval.Result {
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	text, ok := textArgument(v[1])
	if !ok {
		return invalidArgument(c, v, 1, "string")
	}
	spec, err := usage.ParseFish(v[0].Text, text)
	if err != nil {
		spec = usage.Spec{Name: v[0].Text} // statements could not be split: ignore the file
	}
	return specValue(c, spec)
}

func registerUsage(r *eval.Registry) bool {
	return pure(r, "parse-usage", opParseUsage, 1, 1) && pure(r, "render-usage", opRenderUsage, 1, 1) &&
		pure(r, "merge-usage", opMergeUsage, 1, 1) && pure(r, "parse-man", opParseMan, 1, 1) &&
		pure(r, "man-redirect", opManRedirect, 1, 1) && pure(r, "parse-fish-completions", opParseFishCompletions, 2, 2)
}
