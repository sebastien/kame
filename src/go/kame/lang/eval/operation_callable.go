package eval

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func (p *Program) newOperationCallable(name string) core.Value {
	for i := range p.OperationFunctions {
		f := p.OperationFunctions[i]
		if f.OperationName == name { return core.Value{Kind: core.Callable, Callable: f, CallableOwner: p, Text: f.OperationName} }
	}
	f := mem.Alloc[Function](p.Alloc)
	f.Kind, f.Owner, f.OperationName = FunctionOperation, p, name
	operation := p.Registry.lookup(name)
	if operation != nil && operation.MinArity == operation.MaxArity {
		f.Arity = operation.MinArity
	}
	p.OperationFunctions = slices.Append(p.Alloc, p.OperationFunctions, f)
	return core.Value{Kind: core.Callable, Callable: f, CallableOwner: p, Text: f.OperationName}
}

func (p *Program) operationForCallable(f *Function) *Operation {
	if f.Owner != p { return nil }
	return p.Registry.lookup(f.OperationName)
}
