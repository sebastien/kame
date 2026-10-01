package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func opNot(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: !truth(v[0])}}
}
func opBool(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: truth(v[0])}}
}

func opStr(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := stringValue(c.Run, v[0], false)
	if !ok {
		return invalidArgument(c, v, 0, "text-coercible value (nil, bool, int, float, string, pattern, list, or record)")
	}
	result := eval.Result{Value: core.NewString(c.Run, value)}
	mem.FreeString(c.Run, value)
	return result
}

func stringValue(a mem.Allocator, v core.Value, quoted bool) (string, bool) {
	b := strings.NewBuilder(a)
	defer b.Free()
	if v.Kind == core.Nil {
		b.WriteString("nil")
	} else if v.Kind == core.Bool {
		if v.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	} else if v.Kind == core.Int {
		var buf [strconv.MaxIntBase10Len]byte
		b.WriteString(strconv.FormatInt(buf[:], v.Int, 10))
	} else if v.Kind == core.Float {
		var buf [strconv.MaxFloat64Len]byte
		b.WriteString(strconv.FormatFloat(buf[:], v.Float, 'g', -1, 64))
	} else if v.Kind == core.String {
		if quoted {
			quote(&b, v.Text)
		} else {
			b.WriteString(v.Text)
		}
	} else if v.Kind == core.Pattern {
		if quoted {
			quote(&b, v.Text)
		} else {
			b.WriteString(v.Text)
		}
	} else if v.Kind == core.List {
		b.WriteByte('[')
		for i := range v.List {
			if i != 0 {
				b.WriteByte(',')
			}
			item, ok := stringValue(a, v.List[i], true)
			if !ok {
				return "", false
			}
			b.WriteString(item)
			mem.FreeString(a, item)
		}
		b.WriteByte(']')
	} else if v.Kind == core.Record {
		b.WriteByte('{')
		for i := 0; i < len(v.Record); i++ {
			selected := -1
			for j := range v.Record {
				rank := 0
				for k := range v.Record {
					if strings.Compare(v.Record[k].Key, v.Record[j].Key) < 0 {
						rank++
					}
				}
				if rank == i {
					selected = j
					break
				}
			}
			if i != 0 {
				b.WriteByte(',')
			}
			quote(&b, v.Record[selected].Key)
			b.WriteByte(':')
			item, ok := stringValue(a, v.Record[selected].Value, true)
			if !ok {
				return "", false
			}
			b.WriteString(item)
			mem.FreeString(a, item)
		}
		b.WriteByte('}')
	} else {
		return "", false
	}
	return strings.Clone(a, b.String()), true
}

func quote(b *strings.Builder, value string) {
	b.WriteByte('"')
	for i := range value {
		if value[i] == '"' || value[i] == '\\' {
			b.WriteByte('\\')
		}
		if value[i] == '\n' {
			b.WriteString("\\n")
		} else if value[i] == '\r' {
			b.WriteString("\\r")
		} else if value[i] == '\t' {
			b.WriteString("\\t")
		} else {
			b.WriteByte(value[i])
		}
	}
	b.WriteByte('"')
}

func opCount(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	n := 0
	if v[0].Kind == core.String {
		n = utf8.RuneCountInString(v[0].Text)
	} else if v[0].Kind == core.Bytes {
		n = len(v[0].Bytes)
	} else if v[0].Kind == core.List {
		n = len(v[0].List)
	} else if v[0].Kind == core.Record {
		n = len(v[0].Record)
	} else {
		return invalidArgument(c, v, 0, "string, bytes, list, or record")
	}
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(n)}}
}
func opFirst(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.List {
		return invalidArgument(c, v, 0, "list")
	}
	if len(v[0].List) == 0 {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return eval.Result{Value: v[0].List[0].Clone(c.Run)}
}
func opNth(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[1].Kind != core.Int {
		return invalidArgument(c, v, 1, "int")
	}
	index := int(v[1].Int)
	if v[0].Kind == core.List {
		if index < 0 {
			index += len(v[0].List)
		}
		if index < 0 || index >= len(v[0].List) {
			return eval.Result{Value: core.Value{Kind: core.Nil}}
		}
		return eval.Result{Value: v[0].List[index].Clone(c.Run)}
	}
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "list or string")
	}
	if index < 0 {
		index += utf8.RuneCountInString(v[0].Text)
	}
	start, end := runeOffset(v[0].Text, index), runeOffset(v[0].Text, index+1)
	if start < 0 || end < 0 {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return eval.Result{Value: core.NewString(c.Run, v[0].Text[start:end])}
}
func runeOffset(text string, index int) int {
	if index < 0 {
		return -1
	}
	offset := 0
	for i := 0; i < index; i++ {
		if offset == len(text) {
			return -1
		}
		_, width := utf8.DecodeRuneInString(text[offset:])
		offset += width
	}
	return offset
}
func opApply(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	// Legacy sources pass the argument list first; the function-first order is
	// also accepted for symmetry.
	//
	// Frees are explicit, never deferred: Solod emits textually-prior defers
	// at every later return, so conditional defers would also run on paths
	// whose branch never executed (double free with freeArgCallables below).
	if v[0].Kind == core.List && v[1].Kind == core.Callable {
		arguments := applyArguments(c.Run, v[0], v[1])
		result := c.Call(v[1], arguments.Values)
		if arguments.Owned {
			slices.Free(c.Run, arguments.Values)
		}
		c.FreeCallable(&v[1])
		return result
	}
	if v[0].Kind == core.Callable && v[1].Kind == core.List {
		arguments := applyArguments(c.Run, v[1], v[0])
		result := c.Call(v[0], arguments.Values)
		if arguments.Owned {
			slices.Free(c.Run, arguments.Values)
		}
		c.FreeCallable(&v[0])
		return result
	}
	if v[0].Kind == core.Callable {
		return invalidArgument(c, v, 1, "list")
	}
	if v[0].Kind == core.List {
		return invalidArgument(c, v, 1, "callable")
	}
	return invalidArgument(c, v, 0, "list or callable")
}

type applyCall struct {
	Values []core.Value
	Owned  bool
}

func applyArguments(a mem.Allocator, values core.Value, function core.Value) applyCall {
	callable := function.Callable.(*eval.Function)
	// Legacy apply treats a one-parameter function as a list consumer. Keep
	// function-first splatting for multi-parameter callbacks.
	if len(callable.Parameters) == 1 || callable.Arity == 1 {
		arguments := mem.AllocSlice[core.Value](a, 1, 1)
		arguments[0] = values
		return applyCall{Values: arguments, Owned: true}
	}
	return applyCall{Values: values.List}
}
func opList(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return eval.Result{Value: core.NewList(c.Run, v)}
}
func opNop(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if len(v) == 0 {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return eval.Result{Value: v[len(v)-1].Clone(c.Run)}
}
