package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
)

type bindingKind int

const (
	bindingValue bindingKind = iota
	bindingFunction
	bindingDefinition
	bindingLocalDefinition
	bindingEager
)

type binding struct {
	Kind        bindingKind
	Name        string
	Value       core.Value
	Function    *Function
	Definition  core.ResourceKey
	Local       *localDefinition
	Initialized bool
	Captured    bool // A compiled source or session entry closes over this let.
	LetBinding  *expr.LetBinding
}

type bindingIndexEntry struct {
	name  string
	index int
}

type Scope struct {
	Alloc        mem.Allocator
	Parent       *Scope
	Bindings     []binding
	bindingIndex []bindingIndexEntry
	letSlots     map[*expr.LetBinding]int
	References   int
	// Interactive rebinding replaces slots; other scopes keep authored shadowing.
	replaceBindings bool
	// Assignment can reach enclosing blocks, up to the current invocation.
	assignmentBoundary bool
	// Call scopes pin their code while suspended bodies or cleanup borrow it.
	sourceStorage *FunctionStorage
	// Section holds the positional arguments of an enclosing placeholder
	// section call. The scope owns these values and frees them with itself.
	Section   []core.Value
	pipeInput *pipeInput
	// breakingCycles prevents recursive scope releases from trying to
	// discover the same unreachable callable cycle while it is dismantled.
	breakingCycles       bool
	hasDescendantCapture bool
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
	stored := value.Clone(s.Alloc)
	// Bound values are read by every lookup; share their backing once here.
	stored.Share(s.Alloc)
	s.appendBinding(binding{Kind: bindingValue, Name: owned(s.Alloc, name), Value: stored})
}

func (s *Scope) appendBinding(value binding) {
	s.storeBinding(value, s.replaceBindings)
}

func (s *Scope) storeBinding(value binding, replace bool) {
	index := len(s.Bindings)
	lo, hi := 0, len(s.bindingIndex)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if s.bindingIndex[mid].name < value.Name {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(s.bindingIndex) && s.bindingIndex[lo].name == value.Name {
		if replace {
			index = s.bindingIndex[lo].index
			old := s.Bindings[index]
			delete(s.letSlots, old.LetBinding)
			s.Bindings[index] = value
			s.bindingIndex[lo].name = value.Name
			s.freeBinding(&old)
		} else {
			s.Bindings = append(s.Bindings, value)
			s.bindingIndex[lo].index = index
		}
	} else {
		s.Bindings = append(s.Bindings, value)
		s.bindingIndex = append(s.bindingIndex, bindingIndexEntry{})
		copy(s.bindingIndex[lo+1:], s.bindingIndex[lo:])
		s.bindingIndex[lo] = bindingIndexEntry{name: value.Name, index: index}
	}
	if value.LetBinding != nil {
		if s.letSlots == nil {
			s.letSlots = make(map[*expr.LetBinding]int)
		}
		s.letSlots[value.LetBinding] = index
	}
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
	if function.PayloadAlloc == nil {
		function.PayloadAlloc = s.Alloc
	}
	s.appendBinding(binding{Kind: bindingFunction, Name: owned(s.Alloc, name), Function: function})
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

// DeclarationChildScope takes ownership of the value on success.
func DeclarationChildScope(a mem.Allocator, parent *Scope, bindingName string, value *core.Value) *Scope {
	child := newScope(a, parent)
	if !child.trySetValue(bindingName, *value) {
		child.Free()
		return nil
	}
	value.Free(a)
	return child
}

func (s *Scope) setDefinition(name string) {
	key := core.NewResourceKey(s.Alloc, core.ResourceDefinition, name)
	s.appendBinding(binding{Kind: bindingDefinition, Name: owned(s.Alloc, name), Definition: key})
}

func (s *Scope) setLocalDefinition(name string, expression *expr.Expr) *localDefinition {
	local := mem.Alloc[localDefinition](s.Alloc)
	local.Expression, local.Scope = expression, s
	s.appendBinding(binding{Kind: bindingLocalDefinition, Name: owned(s.Alloc, name), Local: local})
	return local
}

func (s *Scope) reserveEager(e *expr.Expr, shadow bool) {
	s.storeBinding(binding{Kind: bindingEager, Name: owned(s.Alloc, e.Text), LetBinding: e.LetBinding}, s.replaceBindings && !shadow)
}

func (s *Scope) lookup(name string) *binding {
	return s.lookupResolved(name, nil)
}

func (s *Scope) lookupResolved(name string, let *expr.LetBinding) *binding {
	for current := s; current != nil; current = current.Parent {
		if b := current.localResolved(name, let); b != nil {
			return b
		}
	}
	return nil
}

func (s *Scope) localResolved(name string, let *expr.LetBinding) *binding {
	if let != nil {
		if slot, ok := s.letSlots[let]; ok {
			return &s.Bindings[slot]
		}
		if !s.replaceBindings {
			return nil
		}
	}
	return s.localBinding(name)
}

func (s *Scope) localBinding(name string) *binding {
	lo, hi := 0, len(s.bindingIndex)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if s.bindingIndex[mid].name < name {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(s.bindingIndex) && s.bindingIndex[lo].name == name {
		return &s.Bindings[s.bindingIndex[lo].index]
	}
	return nil
}

func (s *Scope) Free() {
	if s == nil {
		return
	}
	s.References--
	if s.References != 0 {
		if !s.breakingCycles && !s.breakCallableCycle() {
			for owner := s; owner != nil; owner = owner.Parent {
				if owner.hasDescendantCapture {
					s.collectCaptureCycles()
					break
				}
			}
		}
		return
	}
	for i := range s.Bindings {
		s.freeBinding(&s.Bindings[i])
	}
	for i := range s.Section {
		freeCallables(s.Alloc, &s.Section[i])
		s.Section[i].Free(s.Alloc)
	}
	mem.FreeSlice(nil, s.Section)
	mem.FreeSlice(nil, s.Bindings)
	mem.FreeSlice(nil, s.bindingIndex)
	if s.pipeInput != nil {
		s.pipeInput.result.Free(s.Alloc)
		FreeEffects(s.Alloc, s.pipeInput.effects)
		for _, path := range s.pipeInput.writePaths {
			mem.FreeString(s.Alloc, path)
		}
		mem.FreeSlice(nil, s.pipeInput.writePaths)
		expr.Free(s.Alloc, s.pipeInput.expression)
		mem.Free(s.Alloc, s.pipeInput)
	}
	releaseFunctionStorage(s.sourceStorage)
	parent := s.Parent
	mem.Free(s.Alloc, s)
	parent.Free()
}

func (s *Scope) freeBinding(b *binding) {
	mem.FreeString(s.Alloc, b.Name)
	if b.Kind == bindingValue || b.Kind == bindingEager {
		freeCallables(s.Alloc, &b.Value)
		b.Value.Free(s.Alloc)
	}
	if b.Kind == bindingDefinition {
		b.Definition.Free(s.Alloc)
	}
	if b.Kind == bindingLocalDefinition {
		local := b.Local
		local.Value.Free(s.Alloc)
		local.Diagnostic.Free(s.Alloc)
		FreeEffects(s.Alloc, local.Effects)
		for j := range local.WritePaths {
			mem.FreeString(s.Alloc, local.WritePaths[j])
		}
		mem.FreeSlice(nil, local.WritePaths)
		if local.Aggregate != nil {
			mem.FreeSlice(nil, local.Aggregate.Expressions)
			mem.FreeSlice(nil, local.Aggregate.Scopes)
		}
		expr.Free(s.Alloc, local.Configured)
		mem.Free(s.Alloc, local)
	}
	if b.Kind == bindingFunction && b.Function.Kind == FunctionDefinition {
		freeFunctionStorage(s.Alloc, b.Function)
		mem.Free(s.Alloc, b.Function)
	}
}
