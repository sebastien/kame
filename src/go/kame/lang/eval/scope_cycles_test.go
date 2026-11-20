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
