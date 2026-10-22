package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

func optionLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func optionDigit(b byte) bool  { return b >= '0' && b <= '9' }

func validOptionAlias(name string) bool {
	if len(name) == 2 {
		return name[0] == '-' && (optionLetter(name[1]) || optionDigit(name[1]))
	}
	if len(name) < 3 || name[:2] != "--" || !optionLetter(name[2]) {
		return false
	}
	for i := 3; i < len(name); i++ {
		if !optionLetter(name[i]) && !optionDigit(name[i]) && name[i] != '-' {
			return false
		}
	}
	return true
}

func optionIndex(schema core.Value, alias string) int {
	for i := range schema.Record {
		names := recordField(schema.Record[i].Value, "names")
		for j := range names.List {
			if names.List[j].Text == alias {
				return i
			}
		}
	}
	return -1
}

func validArgumentSchema(schema core.Value) bool {
	if schema.Kind != core.Record {
		return false
	}
	for i := range schema.Record {
		d := schema.Record[i].Value
		if d.Kind != core.Record || len(d.Record) != 3 {
			return false
		}
		names, arity := recordField(d, "names"), recordField(d, "arity")
		repeat := recordField(d, "repeat")
		if names.Kind != core.List || len(names.List) == 0 || arity.Kind != core.String || repeat.Kind != core.String {
			return false
		}
		if arity.Text != "flag" && arity.Text != "one" && arity.Text != "many" {
			return false
		}
		if (repeat.Text != "reject" && repeat.Text != "replace" && repeat.Text != "append") || (arity.Text == "flag" && repeat.Text == "append") {
			return false
		}
		for j := range names.List {
			alias := names.List[j]
			if alias.Kind != core.String || !validOptionAlias(alias.Text) || optionIndex(schema, alias.Text) != i {
				return false
			}
			for k := 0; k < j; k++ {
				if names.List[k].Text == alias.Text {
					return false
				}
			}
		}
	}
	return true
}

// Report location and schema identity, never user-supplied values.
func argumentFailure(c *eval.Context, index int, field string, message string) eval.Result {
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	b.WriteString("argument ")
	var number [strconv.MaxIntBase10Len]byte
	b.WriteString(strconv.Itoa(number[:], index))
	if field != "" {
		b.WriteString(" (option ")
		b.WriteString(field)
		b.WriteByte(')')
	}
	b.WriteString(": ")
	b.WriteString(message)
	return ownedFailure(c.Run, "ARGS_INVALID", b.String())
}

func optionLike(text string) bool { return len(text) > 1 && text[0] == '-' }

func opParseArgs(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if !validArgumentSchema(values[0]) {
		discardArgCallables(c, values)
		return c.InvalidOperation("expects a schema with unique aliases, arity and repeat policy")
	}
	if values[1].Kind != core.List {
		return invalidArgument(c, values, 1, "list of strings")
	}
	for i := range values[1].List {
		if values[1].List[i].Kind != core.String {
			discardArgCallables(c, values)
			return c.InvalidElement(1, i, "string", values[1].List[i].Kind)
		}
	}
	schema, argv := values[0], values[1].List
	fields := slices.Make[core.RecordField](c.Run, len(schema.Record))
	seen := slices.Make[bool](c.Run, len(fields))
	defer slices.Free(c.Run, seen)
	for i := range fields {
		fields[i].Key = schema.Record[i].Key
		fields[i].Value.Kind = core.List
		if recordField(schema.Record[i].Value, "arity").Text == "flag" {
			fields[i].Value.Kind = core.Bool
		}
	}
	var operands []core.Value
	var failed eval.Result
	ended := false
	for i := 0; i < len(argv); i++ {
		token := argv[i].Text
		if !ended && token == "--" {
			ended = true
			continue
		}
		if ended || !optionLike(token) {
			operands = slices.Append(c.Run, operands, argv[i].Clone(c.Run))
			continue
		}
		alias, attached, hasAttached := token, "", false
		if strings.HasPrefix(token, "--") {
			if cut := strings.Index(token, "="); cut >= 0 {
				alias, attached, hasAttached = token[:cut], token[cut+1:], true
			}
		}
		index := optionIndex(schema, alias)
		if index < 0 {
			failed = argumentFailure(c, i, "", "unknown option")
			break
		}
		d := schema.Record[index]
		arity, repeat := recordField(d.Value, "arity"), recordField(d.Value, "repeat")
		if seen[index] && repeat.Text == "reject" {
			failed = argumentFailure(c, i, d.Key, "repeated option")
			break
		}
		seen[index] = true
		if arity.Text == "flag" {
			if hasAttached {
				failed = argumentFailure(c, i, d.Key, "flag takes no value")
				break
			}
			fields[index].Value.Bool = true
			continue
		}
		if repeat.Text == "replace" {
			fields[index].Value.Free(c.Run)
			fields[index].Value.Kind = core.List
		}
		start, count := i, 0
		if hasAttached {
			fields[index].Value.List = slices.Append(c.Run, fields[index].Value.List, core.NewString(c.Run, attached))
			count++
		}
		for i+1 < len(argv) && !optionLike(argv[i+1].Text) && (arity.Text == "many" || count == 0) {
			i++
			fields[index].Value.List = slices.Append(c.Run, fields[index].Value.List, argv[i].Clone(c.Run))
			count++
		}
		if count == 0 {
			failed = argumentFailure(c, start, d.Key, "missing value")
			break
		}
	}
	var result core.Value
	if failed.Diagnostic.Code == "" {
		result = core.NewRecord(c.Run, []core.RecordField{
			{Key: "options", Value: core.Value{Kind: core.Record, Record: fields}},
			{Key: "operands", Value: core.Value{Kind: core.List, List: operands}},
		})
	}
	for i := range fields {
		fields[i].Value.Free(c.Run)
	}
	slices.Free(c.Run, fields)
	freeValues(c, operands)
	if failed.Diagnostic.Code != "" {
		return failed
	}
	return eval.Result{Value: result}
}
