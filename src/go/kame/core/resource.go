// Package core provides Kame's portable reactive values and streams.
package core

import "solod.dev/so/mem"

type ResourceKind int

const (
	ResourceDefinition ResourceKind = iota
	ResourceTarget
	ResourceFile
	ResourceTask
	ResourceService
	ResourceGlob
	ResourceEnvironment
	ResourceTool
)

// ResourceKey is owned by the allocator that created or cloned it.
type ResourceKey struct {
	Kind ResourceKind
	Name string
}

func NewResourceKey(a mem.Allocator, kind ResourceKind, name string) ResourceKey {
	if name == "" {
		return ResourceKey{Kind: kind}
	}
	b := mem.AllocSlice[byte](a, len(name), len(name))
	copy(b, []byte(name))
	return ResourceKey{Kind: kind, Name: string(b)}
}

func (k *ResourceKey) Clone(a mem.Allocator) ResourceKey {
	return NewResourceKey(a, k.Kind, k.Name)
}

func (k *ResourceKey) Free(a mem.Allocator) {
	mem.FreeString(a, k.Name)
	*k = ResourceKey{}
}
