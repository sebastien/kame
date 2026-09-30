// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/expr"
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
	// Values are cloned into the scope allocator, so callers must hold
	// context.Run == s.Alloc (true via newScope(context.Run)). Callable
	// clones share the function pointer: the scope becomes a co-owner, so
	// callers transfer (shallow-free the original) or read back a fresh
	// borrow, never return the same value.
	//
	// Raw setValue performs no strand check. Form stores that can target an
	// ancestor must go through trySetValue; fresh-child stores in callValues
	// use this directly because a fresh child cannot be an ancestor.
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingValue, Name: owned(s.Alloc, name), Value: value.Clone(s.Alloc)})
}

// trySetValue stores value under name unless it would strand its capture.
// It reports false without storing when wouldStrandCapture holds; callers
// reject with rejectStrandedCapture on false. Returns true after storing,
// after which the caller transfers (shallow-frees the original).
func (s *Scope) trySetValue(name string, value core.Value) bool {
	if wouldStrandCapture(s, value) {
		return false
	}
	s.setValue(name, value)
	return true
}

func (s *Scope) setFunction(name string, function *Function) {
	s.Bindings = slices.Append(s.Alloc, s.Bindings, binding{Kind: bindingFunction, Name: owned(s.Alloc, name), Function: function})
}

// RenderChildScope creates a child scope binding each record field as a name
// for template payloads. It returns nil without storing when a binding would
// strand its capture; the caller reports EXPR_INVALID and frees the child.
func RenderChildScope(c *Context, parent *Scope, record core.Value) *Scope {
	child := newScope(c.Run, parent)
	for i := range record.Record {
		if !child.trySetValue(record.Record[i].Key, record.Record[i].Value) {
			child.Free()
			return nil
		}
	}
	return child
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
