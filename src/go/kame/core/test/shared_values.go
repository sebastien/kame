package corecheck

import (
	"kame/core"
	"kame/internal/testing"
)

func sharedTestList(t *testing.T) core.Value {
	a := t.Allocator()
	items := []core.Value{core.NewString(a, "one"), core.NewList(a, []core.Value{core.NewString(a, "nested")})}
	value := core.NewList(a, items)
	for i := range items {
		items[i].Free(a)
	}
	return value
}

func TestSharedValuesCloneWithoutCopyingAndFreeWithLastOwner(t *testing.T) {
	a := t.Allocator()
	owner := sharedTestList(t)
	owner.Share(a)
	if owner.Shared == nil {
		t.Fatal("callable-free list was not shared")
		return
	}
	borrowed := owner.Clone(a)
	if &borrowed.List[0] != &owner.List[0] {
		t.Error("clone of a shared list copied its backing")
	}
	borrowed.Free(a)
	if len(owner.List) != 2 || owner.List[0].Text != "one" || owner.List[1].List[0].Text != "nested" {
		t.Error("freeing one owner released the shared elements")
	}
	appended := owner.Clone(a)
	appended.List = append(appended.List, core.NewString(a, "extra"))
	if len(owner.List) != 2 {
		t.Error("append by one owner changed another owner's list")
	}
	appended.List = appended.List[:2]
	appended.Free(a)
	owned := owner.CloneOwned(a)
	owned.List[1].List[0].Text = "changed"
	if owned.Shared != nil || owner.List[1].List[0].Text != "nested" {
		t.Error("owned clone aliases shared storage")
	}
	owned.Free(a)
	private := owner.Clone(a)
	private.Unshare(a)
	private.List[0].Free(a)
	if private.Shared != nil || owner.List[0].Text != "one" {
		t.Error("unshared value still aliases shared storage")
	}
	private.Free(a)
	owner.Free(a)
}

func TestSharedValuesExcludeCallables(t *testing.T) {
	a := t.Allocator()
	value := core.NewList(a, []core.Value{{Kind: core.Callable}})
	value.Share(a)
	if value.Shared != nil {
		t.Error("a list containing a callable was shared")
	}
	value.Free(a)
}
