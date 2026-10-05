package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type resourceURICase struct {
	input string
	want  string
}

func TestResourceURICanonicalization(t *testing.T) {
	a := t.Allocator()
	cases := []resourceURICase{
		{"file:///a//b/./c/../d", "file:///a/b/d"},
		{"file:///a/%2e/b/%2E%2e/c", "file:///a/c"},
		{"mem://workspace/a/./b/../item%20one", "mem://workspace/a/item one"},
		{"mem://Other/", "mem://Other/"},
	}
	for _, test := range cases {
		result := core.ParseResourceURI(a, test.input)
		if result.Error != "" {
			t.Errorf("ParseResourceURI(%q): %s at %d", test.input, result.Error, result.Offset)
			continue
		}
		canonical := result.URI.Canonical(a)
		if canonical != test.want {
			t.Errorf("ParseResourceURI(%q) = %q, want %q", test.input, canonical, test.want)
		}
		mem.FreeString(a, canonical)
		result.URI.Free()
	}
}

func TestResourceURIRejectsInvalidInputs(t *testing.T) {
	a := t.Allocator()
	invalid := []string{
		"/relative/path",
		"HTTP://host/path",
		"http://host/path",
		"file://host/path",
		"file:///../../escape",
		"mem:///path",
		"mem://space name/path",
		"mem://workspace/%Q0",
		"mem://workspace/path?query",
		"mem://workspace/path#fragment",
		"file:///bad/%FF",
	}
	for _, input := range invalid {
		result := core.ParseResourceURI(a, input)
		if result.Error == "" {
			result.URI.Free()
			t.Errorf("ParseResourceURI(%q) accepted invalid URI", input)
		}
	}
}

func TestResourceURIParserReleasesTemporaryStorage(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	before := tracker.Stats().Alloc
	for i := 0; i < 100; i++ {
		result := core.ParseResourceURI(a, "mem://workspace/a/b/../file%20name")
		if result.Error != "" {
			t.Fatal("valid URI failed to parse")
			return
		}
		result.URI.Free()
	}
	if got := tracker.Stats().Alloc - before; got != 0 {
		t.Errorf("resource URI parser retained %d bytes", got)
	}
}
