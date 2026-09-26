// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type bindingKind int

const (
	bindingValue bindingKind = iota
	bindingFunction
	bindingDefinition
)

type binding struct {
	Kind       bindingKind
	Name       string
	Value      core.Value
	Function   *Function
	Definition core.ResourceKey
}

type Scope struct {
	Alloc      mem.Allocator
	Parent     *Scope
	Bindings   []binding
	References int
	// Section holds the positional arguments of an enclosing placeholder
	// section call. The scope owns these values and frees them with itself.
	Section []core.Value
	// breakingCycles prevents recursive scope releases from trying to
	// discover the same unreachable callable cycle while it is dismantled.
	breakingCycles bool
}

func newScope(a mem.Allocator, parent *Scope) *Scope {
	s := mem.Alloc[Scope](a)
	s.Alloc, s.Parent, s.References = a, parent, 1
	if parent != nil {
		parent.Retain()
	}
	return s
}

func (s *Scope) Retain() {
	if s != nil {
		s.References++
	}
}

func (s *Scope) setValue(name string, value core.Value) {
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingValue, Name: owned(s.Alloc, name), Value: value.Clone(s.Alloc)})
}

func (s *Scope) setFunction(name string, function *Function) {
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingFunction, Name: owned(s.Alloc, name), Function: function})
}

func (s *Scope) setDefinition(name string) {
	key := core.NewResourceKey(s.Alloc, core.ResourceDefinition, name)
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingDefinition, Name: owned(s.Alloc, name), Definition: key})
}

func (s *Scope) lookup(name string) *binding {
	for current := s; current != nil; current = current.Parent {
		for i := len(current.Bindings) - 1; i >= 0; i-- {
			if current.Bindings[i].Name == name {
				return &current.Bindings[i]
			}
		}
	}
	return nil
}

func (s *Scope) Free() {
	if s == nil {
		return
	}
	s.References--
	if s.References != 0 {
		if !s.breakingCycles && s.breakCallableCycle() {
			return
		}
		return
	}
	for i := range s.Bindings {
		mem.FreeString(s.Alloc, s.Bindings[i].Name)
		if s.Bindings[i].Kind == bindingValue {
			freeCallables(s.Alloc, &s.Bindings[i].Value)
			s.Bindings[i].Value.Free(s.Alloc)
		}
		if s.Bindings[i].Kind == bindingDefinition {
			s.Bindings[i].Definition.Free(s.Alloc)
		}
		if s.Bindings[i].Kind == bindingFunction && s.Bindings[i].Function.Kind == FunctionDefinition {
			if s.Bindings[i].Function.ParametersOwned {
				if s.Bindings[i].Function.ParameterNamesOwned {
					for j := range s.Bindings[i].Function.Parameters {
						mem.FreeString(s.Alloc, s.Bindings[i].Function.Parameters[j].Name)
					}
				}
				slices.Free(s.Alloc, s.Bindings[i].Function.Parameters)
			}
			if s.Bindings[i].Function.BodyOwned {
				for j := range s.Bindings[i].Function.Body {
					expr.Free(s.Alloc, s.Bindings[i].Function.Body[j])
				}
				slices.Free(s.Alloc, s.Bindings[i].Function.Body)
			}
			mem.Free(s.Alloc, s.Bindings[i].Function)
		}
	}
	for i := range s.Section {
		freeCallables(s.Alloc, &s.Section[i])
		s.Section[i].Free(s.Alloc)
	}
	slices.Free(s.Alloc, s.Section)
	slices.Free(s.Alloc, s.Bindings)
	parent := s.Parent
	mem.Free(s.Alloc, s)
	parent.Free()
}
