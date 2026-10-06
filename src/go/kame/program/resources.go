package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// FilesystemResources returns caller-owned external file and glob keys reachable
// from retained roots. Produced outputs are excluded to avoid watching our writes.
func (p *Program) FilesystemResources() []core.ResourceKey {
	nodes := p.Engine.TrackedNodes()
	var keys []core.ResourceKey
	for i := range nodes {
		n := nodes[i]
		if n.Interest != 0 && (n.Key.Kind == core.ResourceFile || n.Key.Kind == core.ResourceGlob) && p.instanceIndex(n) < 0 {
			keys = slices.Append(p.Alloc, keys, n.Key.Clone(p.Alloc))
		}
	}
	slices.Free(p.Alloc, nodes)
	return keys
}

// InvalidateResource clears a changed resource and all transitive consumers.
func (p *Program) InvalidateResource(key core.ResourceKey) {
	node := p.Engine.Lookup(key)
	if node != nil {
		p.Engine.Revalidate(node)
	}
	p.drainCancellations()
}

// ResourceFingerprint polls content for files and membership for globs. It
// does not change graph state or evaluate definitions.
func (p *Program) ResourceFingerprint(key core.ResourceKey) string {
	if key.Kind == core.ResourceGlob {
		value := p.wildcard(key.Name)
		b := strings.NewBuilder(p.Alloc)
		for i := range value.List {
			b.WriteString(value.List[i].Text)
			b.WriteByte(0)
		}
		stamp := cloneText(p.Alloc, b.String())
		b.Free()
		value.Free(p.Alloc)
		return stamp
	}
	name := p.canonicalTarget(key.Name, true)
	completion := p.fileCompletion(host.Request{}, host.OpFileContent, name)
	signature := contentObservation(completion.Value)
	completion.Value.Free(p.Alloc)
	if completion.Diagnostic.Code != "" {
		completion.Diagnostic.Free(p.Alloc)
		mem.FreeString(p.Alloc, name)
		return cloneText(p.Alloc, "unavailable")
	}
	if signature.Mode == core.SignatureMissing {
		mem.FreeString(p.Alloc, name)
		return cloneText(p.Alloc, "missing")
	}
	if !p.ResourceMetadataObserved(key) {
		mem.FreeString(p.Alloc, name)
		return cacheHex(p.Alloc, signature.Digest[:])
	}
	digest := core.NewDigest()
	digest.Uint64(uint64(signature.Mode))
	digest.Write(signature.Digest[:])
	if p.ResourceMetadataObserved(key) {
		completion = p.fileCompletion(host.Request{}, host.OpStat, name)
		metadata := core.ValueSignature(completion.Value)
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
	node := p.Engine.Lookup(key)
	if node != nil && node.Current {
		if node.Signature.Mode == core.SignatureMissing {
			return cloneText(p.Alloc, "missing")
		}
		if node.Signature.Mode == core.SignatureContent {
			return cacheHex(p.Alloc, node.Signature.Digest[:])
		}
	}
	return p.ResourceFingerprint(key)
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
			if item.Key.Kind == key.Kind && item.Key.Name == key.Name && item.Aspect == core.ObservationMetadata {
				observed = true
				break
			}
		}
		if observed {
			break
		}
	}
	slices.Free(p.Alloc, nodes)
	return observed
}
