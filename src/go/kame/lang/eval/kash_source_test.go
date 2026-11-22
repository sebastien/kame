package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/script"
	"testing"
)

func TestRuntimeSourceReusesAuthoredRevision(t *testing.T) {
	p := &Program{Alloc: mem.System}
	defer func() {
		p.freeSourceParts()
		for _, module := range p.Modules {
			module.Free()
		}
	}()
	makeSource := func(path, text string, imported *kashModule) *kashModule {
		s := moduleSource{Path: path, Parsed: script.ParseKash(p.Alloc, path, text), Signature: core.ContentSignature([]byte(text))}
		s.namespaces = map[string]*kashModule{"config": imported}
		return &kashModule{Program: p, Sources: []moduleSource{s}}
	}
	firstImport, nextImport := &kashModule{}, &kashModule{}
	initial := p.retainRuntimeSource(makeSource("/profile", "let x = 1", firstImport))
	for range 100 {
		if got := p.retainRuntimeSource(makeSource("/profile", "let x = 1", firstImport)); got != initial {
			t.Fatal("unchanged source did not reuse its authored origin")
		}
	}
	if len(p.Modules) != 1 || len(p.SourceParts) != 1 {
		t.Fatal("retained duplicate source storage")
	}
	if got := p.retainRuntimeSource(makeSource("/profile", "let x = 2", firstImport)); got == initial {
		t.Fatal("changed file reused stale syntax")
	}
	if got := p.retainRuntimeSource(makeSource("/profile", "let x = 1", nextImport)); got == initial {
		t.Fatal("changed import reused stale bindings")
	}
}
