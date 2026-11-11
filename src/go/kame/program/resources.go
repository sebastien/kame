package program

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"slices"
	"strings"
)

// FilesystemResources returns caller-owned external file and glob keys reachable
// from retained roots. Produced outputs are excluded to avoid watching our writes.
func (p *Program) FilesystemResources() []core.ResourceKey {
	nodes := p.Engine.TrackedNodes()
	var keys []core.ResourceKey
	for i := range nodes {
		n := nodes[i]
		if n.Interest != 0 && (n.Key.Kind == core.ResourceFile || n.Key.Kind == core.ResourceGlob) && p.instanceIndex(n) < 0 {
			keys = append(keys, n.Key.Clone(p.Alloc))
		}
		if n.Interest == 0 {
			continue
		}
		for j := range n.Observations {
			item := n.Observations[j]
			if item.Key.Kind != core.ResourceFile || item.Aspect != core.ObservationContent {
				continue
			}
			// Interested resource nodes are listed separately, even later in nodes.
			if resource := p.Engine.Lookup(item.Key); resource != nil && (resource.Interest != 0 || p.instanceIndex(resource) >= 0) {
				continue
			}
			present := false
			for k := range keys {
				if keys[k].Kind == item.Key.Kind && keys[k].Name == item.Key.Name {
					present = true
					break
				}
			}
			if !present {
				keys = append(keys, item.Key.Clone(p.Alloc))
			}
		}
	}
	mem.FreeSlice(nil, nodes)
	return keys
}

// InvalidateResource clears a changed resource and all transitive consumers.
func (p *Program) InvalidateResource(key core.ResourceKey) {
	p.clearChangedProductionInputs()
	if key.Kind == core.ResourceFile && p.Eval != nil {
		p.Eval.RevalidateModuleSource(key.Name)
	}
	node := p.Engine.Lookup(key)
	if node != nil {
		p.Engine.Revalidate(node)
	}
	if key.Kind == core.ResourceFile {
		// Context-local reads can be observations without graph edges.
		nodes := p.Engine.TrackedNodes()
		for i := range nodes {
			// Graph-backed consumers already participate in resource revalidation.
			if nodes[i] == node || (node != nil && (slices.Contains(nodes[i].Static, node) || slices.Contains(nodes[i].Dynamic, node))) {
				continue
			}
			for j := range nodes[i].Observations {
				item := nodes[i].Observations[j]
				if item.Key.Kind == key.Kind && item.Key.Name == key.Name && item.Aspect == core.ObservationContent {
					p.Engine.Revalidate(nodes[i])
					break
				}
			}
		}
		mem.FreeSlice(nil, nodes)
	}
	p.drainCancellations()
}

// ResourceFingerprint polls content for files and membership for globs. It
// does not change graph state or evaluate definitions.
func (p *Program) ResourceFingerprint(key core.ResourceKey) string {
	if key.Kind == core.ResourceGlob {
		result := p.wildcard(key.Name)
		if result.Diagnostic.Code != "" {
			result.Diagnostic.Free(p.Alloc)
			return cloneText(p.Alloc, "unavailable")
		}
		value := result.Value
		b := strings.Builder{}
		for i := range value.List {
			b.WriteString(value.List[i].Text)
			b.WriteByte(0)
		}
		stamp := cloneText(p.Alloc, b.String())
		b.Reset()
		value.Free(p.Alloc)
		return stamp
	}
	name := p.canonicalTarget(key.Name, true)
	metadataObserved := p.ResourceMetadataObserved(key)
	completion := p.fileCompletion(host.Request{}, host.OpFileContent, name)
	signature := contentObservation(completion.Value)
	completion.Value.Free(p.Alloc)
	if completion.Diagnostic.Code != "" {
		completion.Diagnostic.Free(p.Alloc)
		if !metadataObserved {
			mem.FreeString(p.Alloc, name)
			return cloneText(p.Alloc, "unavailable")
		}
	}
	if signature.Mode == core.SignatureMissing && !metadataObserved {
		mem.FreeString(p.Alloc, name)
		return cloneText(p.Alloc, "missing")
	}
	if !metadataObserved {
		mem.FreeString(p.Alloc, name)
		return cacheHex(p.Alloc, signature.Digest[:])
	}
	digest := core.NewDigest()
	digest.Uint64(uint64(signature.Mode))
	digest.Write(signature.Digest[:])
	ops := []string{host.OpStat, host.OpLstat, host.OpReadlink}
	for i := range ops {
		completion = p.fileCompletion(host.Request{}, ops[i], name)
		metadata := core.MetadataSignature(completion.Value)
		digest.Uint64(uint64(metadata.Mode))
		digest.Write(metadata.Digest[:])
		completion.Value.Free(p.Alloc)
		completion.Diagnostic.Free(p.Alloc)
	}
	mem.FreeString(p.Alloc, name)
	var bytes [32]byte
	digest.Sum(bytes[:])
	return cacheHex(p.Alloc, bytes[:])
}

// A newly discovered watch must start from accepted bytes, not a live read
// that could swallow an edit made just after publication. Metadata views get
// one conservative first scan when no combined accepted baseline is available.
func (p *Program) ResourceAcceptedFingerprint(key core.ResourceKey) string {
	if key.Kind != core.ResourceFile {
		return p.ResourceFingerprint(key)
	}
	if p.ResourceMetadataObserved(key) {
		return cloneText(p.Alloc, "unobserved")
	}
	nodes := p.Engine.TrackedNodes()
	var accepted core.Signature
	found := false
	for i := range nodes {
		if nodes[i].Interest == 0 {
			continue
		}
		for j := range nodes[i].Observations {
			item := nodes[i].Observations[j]
			if item.Key.Kind != key.Kind || item.Key.Name != key.Name || item.Aspect != core.ObservationContent {
				continue
			}
			if found && !accepted.Equal(item.Signature) {
				mem.FreeSlice(nil, nodes)
				return cloneText(p.Alloc, "unobserved")
			}
			accepted, found = item.Signature, true
		}
	}
	mem.FreeSlice(nil, nodes)
	if found {
		if accepted.Mode == core.SignatureMissing {
			return cloneText(p.Alloc, "missing")
		}
		if accepted.Mode == core.SignatureContent {
			return cacheHex(p.Alloc, accepted.Digest[:])
		}
		return cloneText(p.Alloc, "unobserved")
	}
	node := p.Engine.Lookup(key)
	if node != nil && node.Current {
		if node.Signature.Mode == core.SignatureMissing {
			return cloneText(p.Alloc, "missing")
		}
		if node.Signature.Mode == core.SignatureContent {
			return cacheHex(p.Alloc, node.Signature.Digest[:])
		}
	}
	if node != nil {
		// Pending file nodes have not consumed bytes yet; avoid a spurious change.
		return p.ResourceFingerprint(key)
	}
	return cloneText(p.Alloc, "unobserved")
}

// Intentional stat reads subscribe to metadata as well as resource content.
func (p *Program) ResourceMetadataObserved(key core.ResourceKey) bool {
	nodes := p.Engine.TrackedNodes()
	observed := false
	for i := range nodes {
		if nodes[i].Interest == 0 {
			continue
		}
		for j := range nodes[i].Observations {
			item := nodes[i].Observations[j]
			if item.Key.Kind == key.Kind && item.Key.Name == key.Name && item.Aspect >= core.ObservationMetadata {
				observed = true
				break
			}
		}
		if observed {
			break
		}
	}
	mem.FreeSlice(nil, nodes)
	return observed
}
