package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
)

func (p *Program) list(scope *Scope, expression *expr.Expr, context *Context) Result {
	operands := p.argumentValues(scope, expression.Items, context, expression.Span)
	if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	values := operands.Values
	var flattened []core.Value
	for i := range values {
		if expression.Items[i].Kind == expr.ListSpread {
			for j := range values[i].List {
				flattened = append(flattened, values[i].List[j])
			}
		} else {
			flattened = append(flattened, values[i])
		}
	}
	result := Result{Value: core.NewList(context.Run, flattened)}
	mem.FreeSlice(nil, flattened)
	// Transfer: NewList shallow-cloned callables into the result; release
	// only storage so scopes are freed once by Result.Free.
	freeValues(context.Run, values)
	return result
}

func (p *Program) record(scope *Scope, fields []expr.Field, context *Context) Result {
	values := make([]core.RecordField, len(fields))
	for i := range fields {
		r := p.evaluate(context.Engine, scope, fields[i].Value, context)
		if r.Waiting || r.Diagnostic.Code != "" {
			// Discard: fields are freshly evaluated and share nothing with a
			// result yet.
			freeRecordWithCallables(context.Run, values)
			return r
		}
		values[i] = core.RecordField{Key: fields[i].Key, Value: r.Value}
	}
	result := Result{Value: core.NewRecord(context.Run, values)}
	// Transfer: NewRecord shallow-cloned callables into the result.
	freeRecord(context.Run, values)
	return result
}
