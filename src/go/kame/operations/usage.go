package operations

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/usage"
)

// Command specs (spec 058) cross into Kame as records with the fields of
// their JSON form: empty fields are omitted, unknown fields are ignored.

func specValue(c *eval.Context, spec usage.Spec) eval.Result {
	return eval.Result{Value: specRecord(c.Run, spec)}
}

type specFields struct {
	a      mem.Allocator
	fields []core.RecordField
}

func (f *specFields) add(key string, value core.Value) {
	f.fields = append(f.fields, core.RecordField{Key: core.NewString(f.a, key).Text, Value: value})
}

func (f *specFields) text(key, value string, always bool) {
	if value != "" || always {
		f.add(key, core.NewString(f.a, value))
	}
}

func (f *specFields) flag(key string, value bool) {
	if value {
		f.add(key, core.Value{Kind: core.Bool, Bool: true})
	}
}

func (f *specFields) texts(key string, values []string, always bool) {
	if len(values) == 0 && !always {
		return
	}
	list := make([]core.Value, len(values))
	for i := range values {
		list[i] = core.NewString(f.a, values[i])
	}
	f.add(key, core.Value{Kind: core.List, List: list})
}

func (f *specFields) record() core.Value { return core.Value{Kind: core.Record, Record: f.fields} }

func specRecord(a mem.Allocator, spec usage.Spec) core.Value {
	f := specFields{a: a}
	f.text("name", spec.Name, false)
	f.text("help", spec.Help, false)
	f.texts("aliases", spec.Aliases, false)
	if len(spec.Flags) != 0 {
		flags := make([]core.Value, len(spec.Flags))
		for i, flag := range spec.Flags {
			g := specFields{a: a}
			g.texts("names", flag.Names, true)
			g.text("help", flag.Help, false)
			g.text("value", flag.Value, false)
			g.flag("optional", flag.Optional)
			g.flag("global", flag.Global)
			g.texts("choices", flag.Choices, false)
			g.text("complete", flag.Complete, false)
			flags[i] = g.record()
		}
		f.add("flags", core.Value{Kind: core.List, List: flags})
	}
	if len(spec.Args) != 0 {
		args := make([]core.Value, len(spec.Args))
		for i, arg := range spec.Args {
			g := specFields{a: a}
			g.text("name", arg.Name, true)
			g.text("help", arg.Help, false)
			g.flag("optional", arg.Optional)
			g.flag("variadic", arg.Variadic)
			g.texts("choices", arg.Choices, false)
			g.text("complete", arg.Complete, false)
			args[i] = g.record()
		}
		f.add("args", core.Value{Kind: core.List, List: args})
	}
	if len(spec.Commands) != 0 {
		commands := make([]core.Value, len(spec.Commands))
		for i := range spec.Commands {
			commands[i] = specRecord(a, spec.Commands[i])
		}
		f.add("commands", core.Value{Kind: core.List, List: commands})
	}
	return f.record()
}

// specReader decodes a spec record; ok turns false on a mistyped field.
type specReader struct{ ok bool }

func (r *specReader) text(v core.Value, out *string) {
	switch v.Kind {
	case core.String:
		*out = v.Text
	case core.Nil:
	default:
		r.ok = false
	}
}

func (r *specReader) flag(v core.Value, out *bool) {
	switch v.Kind {
	case core.Bool:
		*out = v.Bool
	case core.Nil:
	default:
		r.ok = false
	}
}

func (r *specReader) texts(v core.Value, out *[]string) {
	switch v.Kind {
	case core.List:
		*out = make([]string, len(v.List))
		for i := range v.List {
			if v.List[i].Kind != core.String {
				r.ok = false
			}
			(*out)[i] = v.List[i].Text
		}
	case core.Nil:
	default:
		r.ok = false
	}
}

// records calls each for every record of a list value.
func (r *specReader) records(v core.Value, each func(int, core.Value)) {
	switch v.Kind {
	case core.List:
		for i := range v.List {
			if v.List[i].Kind != core.Record {
				r.ok = false
				return
			}
			each(i, v.List[i])
		}
	case core.Nil:
	default:
		r.ok = false
	}
}

func (r *specReader) spec(v core.Value, spec *usage.Spec) {
	for _, field := range v.Record {
		switch field.Key {
		case "name":
			r.text(field.Value, &spec.Name)
		case "help":
			r.text(field.Value, &spec.Help)
		case "aliases":
			r.texts(field.Value, &spec.Aliases)
		case "flags":
			spec.Flags = make([]usage.Flag, len(field.Value.List))
			r.records(field.Value, func(i int, item core.Value) { r.flagRecord(item, &spec.Flags[i]) })
		case "args":
			spec.Args = make([]usage.Arg, len(field.Value.List))
			r.records(field.Value, func(i int, item core.Value) { r.argRecord(item, &spec.Args[i]) })
		case "commands":
			spec.Commands = make([]usage.Spec, len(field.Value.List))
			r.records(field.Value, func(i int, item core.Value) { r.spec(item, &spec.Commands[i]) })
		}
	}
}

func (r *specReader) flagRecord(v core.Value, f *usage.Flag) {
	for _, field := range v.Record {
		switch field.Key {
		case "names":
			r.texts(field.Value, &f.Names)
		case "help":
			r.text(field.Value, &f.Help)
		case "value":
			r.text(field.Value, &f.Value)
		case "optional":
			r.flag(field.Value, &f.Optional)
		case "global":
			r.flag(field.Value, &f.Global)
		case "choices":
			r.texts(field.Value, &f.Choices)
		case "complete":
			r.text(field.Value, &f.Complete)
		}
	}
}

func (r *specReader) argRecord(v core.Value, a *usage.Arg) {
	for _, field := range v.Record {
		switch field.Key {
		case "name":
			r.text(field.Value, &a.Name)
		case "help":
			r.text(field.Value, &a.Help)
		case "optional":
			r.flag(field.Value, &a.Optional)
		case "variadic":
			r.flag(field.Value, &a.Variadic)
		case "choices":
			r.texts(field.Value, &a.Choices)
		case "complete":
			r.text(field.Value, &a.Complete)
		}
	}
}

func valueSpec(_ *eval.Context, value core.Value) (usage.Spec, bool) {
	var spec usage.Spec
	if value.Kind != core.Record {
		return spec, false
	}
	r := specReader{ok: true}
	r.spec(value, &spec)
	return spec, r.ok
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
	if target := usage.ManPageRedirect([]byte(text)); target != "" {
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
