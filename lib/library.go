// Package lib registers LittleMake's portable pure operations.
package lib

import (
	"littlemake/core"
	"littlemake/lang/eval"
	"solod.dev/so/slices"
	"solod.dev/so/unicode/utf8"
)

// Register installs the small pure-operation base required by expressions.
func Register(registry *eval.Registry) bool {
	return registry.Add(eval.Operation{Name: "not", Call: opNot, MinArity: 1, MaxArity: 1}) &&
		registry.Add(eval.Operation{Name: "bool", Call: opBool, MinArity: 1, MaxArity: 1}) &&
		registry.Add(eval.Operation{Name: "count", Call: opCount, MinArity: 1, MaxArity: 1}) &&
		registry.Add(eval.Operation{Name: "first", Call: opFirst, MinArity: 1, MaxArity: 1}) &&
		registry.Add(eval.Operation{Name: "list", Call: opList, MinArity: 0, MaxArity: -1}) &&
		registry.Add(eval.Operation{Name: "map", Call: opMap, MinArity: 2, MaxArity: 2}) &&
		registry.Add(eval.Operation{Name: "nop", Call: opNop, MinArity: 0, MaxArity: -1})
}

func truth(value core.Value) bool { return value.Kind != core.Nil && !(value.Kind == core.Bool && !value.Bool) }

func opNot(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = context, state
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: !truth(values[0])}}
}

func opBool(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = context, state
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: truth(values[0])}}
}

func opCount(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = context, state
	value := values[0]
	count := 0
	if value.Kind == core.String { count = utf8.RuneCountInString(value.Text)
	} else if value.Kind == core.Bytes { count = len(value.Bytes)
	} else if value.Kind == core.List { count = len(value.List)
	} else if value.Kind == core.Record { count = len(value.Record)
	} else { return invalid() }
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(count)}}
}

func opFirst(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.List { return invalid() }
	if len(values[0].List) == 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }
	return eval.Result{Value: values[0].List[0].Clone(context.Run)}
}

func opList(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	return eval.Result{Value: core.NewList(context.Run, values)}
}

func opNop(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) == 0 { return eval.Result{Value: core.Value{Kind: core.Nil}} }
	return eval.Result{Value: values[len(values)-1].Clone(context.Run)}
}

func opMap(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Callable || values[1].Kind != core.List { return invalid() }
	defer context.FreeCallable(&values[0])
	var result []core.Value
	for i := range values[1].List {
		mapped := context.Call(values[0], values[1].List[i:i+1])
		if mapped.Waiting || mapped.Diagnostic.Code != "" { freeValues(context, result); return mapped }
		result = slices.Append(context.Run, result, mapped.Value)
	}
	value := core.NewList(context.Run, result)
	freeValues(context, result)
	return eval.Result{Value: value}
}

func freeValues(context *eval.Context, values []core.Value) {
	for i := range values { values[i].Free(context.Run) }
	slices.Free(context.Run, values)
}

func invalid() eval.Result {
	return eval.Result{Diagnostic: core.Diagnostic{Code: "EXPR_INVALID"}}
}
