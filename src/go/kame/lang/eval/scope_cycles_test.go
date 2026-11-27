package eval

import (
	"kame/core"
	"testing"
)

func TestCaptureCycleWaitsForLastExternalOwner(t *testing.T) {
	owner := newScope(nil, nil)
	capture := newScope(nil, owner)
	fn := &Function{Kind: FunctionTemporary, Scope: capture}
	capture.Retain()
	owner.setValue("callback", core.Value{Kind: core.Callable, Callable: fn})
	owner.hasDescendantCapture = true
	outside := borrowFunction(nil, fn)
	capture.Free()
	owner.Free()
	if owner.lookup("callback") == nil || outside.Scope.Parent != owner {
		t.Fatal("live closure lost the scope of its binding")
	}
	freeBorrowedWrapper(nil, outside)
	if owner.Bindings != nil || capture.Parent != nil || owner.References != 0 || capture.References != 0 {
		t.Fatal("last closure release retained the scope cycle")
	}
}

func TestPublishedSourceScopeWaitsForLastExternalOwner(t *testing.T) {
	caller := newScope(nil, nil)
	loaded := newScope(nil, caller)
	loaded.setFunction("hello", &Function{Kind: FunctionDefinition, Scope: loaded})
	publishSourceDefinitions(caller, loaded, &Context{})
	saved := borrowFunction(nil, loaded.localBinding("hello").Function)
	loaded.Free()
	caller.Free()
	if caller.localBinding("hello") == nil || loaded.Parent != caller {
		t.Fatal("published scope was released while its function was still held")
	}
	freeBorrowedWrapper(nil, saved)
	if caller.References != 0 || loaded.References != 0 {
		t.Fatal("published source kept its caller alive")
	}
}
