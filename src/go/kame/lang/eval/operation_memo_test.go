package eval

import (
	"slices"
	"testing"
)

func TestMemoEnvironmentReuseKeepsOrderedSnapshot(t *testing.T) {
	p := &Program{}
	c := &Context{Environment: []string{"BASE=inherited", "OTHER=keep"}}
	base := p.environmentDigest(c.CommandEnvironment())
	c.Environment = slices.Clone(c.Environment)
	if got := p.environmentDigest(c.CommandEnvironment()); !got.Equal(base) {
		t.Fatal("equivalent snapshot changed its digest")
	}
	if &p.environmentDigestInput[0] == &c.Environment[0] {
		t.Fatal("equivalent snapshot replaced the retained cache input")
	}
	if got := p.environmentDigest([]string{"OTHER=keep", "BASE=inherited"}); got.Equal(base) {
		t.Fatal("reordered assignments kept the old digest")
	}
	c.setEnvironment("BASE", "changed", true)
	edited := p.environmentDigest(c.CommandEnvironment())
	if edited.Equal(base) {
		t.Fatal("an environment edit kept the old digest")
	}
	c.StageEnvironment = []string{"BASE=stage"}
	if got := p.environmentDigest(c.CommandEnvironment()); got.Equal(edited) {
		t.Fatal("a stage override kept the old digest")
	}
}
