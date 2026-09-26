// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/lang/expr"
	"solod.dev/so/slices"
)

func (p *Program) list(scope *Scope, items []*expr.Expr, context *Context) Result {
	values := slices.Make[core.Value](context.Run, len(items))
	for i := range items {
		r := p.evaluate(context.Engine, scope, items[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			freeValues(context.Run, values)
			return r
		}
		values[i] = r.Value
	}
	result := Result{Value: core.NewList(context.Run, values)}
	freeValues(context.Run, values)
	return result
}

func (p *Program) record(scope *Scope, fields []expr.Field, context *Context) Result {
	values := slices.Make[core.RecordField](context.Run, len(fields))
	for i := range fields {
		r := p.evaluate(context.Engine, scope, fields[i].Value, context)
		if r.Waiting || r.Diagnostic.Code != "" {
			freeRecord(context.Run, values)
			return r
		}
		values[i] = core.RecordField{Key: fields[i].Key, Value: r.Value}
	}
	result := Result{Value: core.NewRecord(context.Run, values)}
	freeRecord(context.Run, values)
	return result
}
