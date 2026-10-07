package eval_test

import (
	"kame/lang/eval"
	"solod.dev/so/testing"
)

func TestProcessEnvironmentSignature(t *testing.T) {
	a := t.Allocator()
	initial := eval.ProcessEnvironmentSignature(a, []string{"PATH=/bin", "LANG=C"})
	reordered := eval.ProcessEnvironmentSignature(a, []string{"LANG=C", "PATH=/bin"})
	if !initial.Equal(reordered) {
		t.Error("assignment order changed environment identity")
	}
	changed := eval.ProcessEnvironmentSignature(a, []string{"PATH=/other", "LANG=C"})
	removed := eval.ProcessEnvironmentSignature(a, []string{"PATH=/bin"})
	added := eval.ProcessEnvironmentSignature(a, []string{"PATH=/bin", "LANG=C", "APP=value"})
	if initial.Equal(changed) || initial.Equal(removed) || initial.Equal(added) {
		t.Error("child environment changes did not change identity")
	}
}
