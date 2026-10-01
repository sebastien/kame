package program

import (
    "kame/core"
    "solod.dev/so/mem"
    "solod.dev/so/slices"
    "solod.dev/so/strconv"
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
    if node != nil { p.Engine.Invalidate(node) }
    p.drainCancellations()
}

// ResourceFingerprint polls metadata for files and membership for globs. It
// does not change graph state or evaluate definitions.
func (p *Program) ResourceFingerprint(key core.ResourceKey) string {
    if key.Kind == core.ResourceGlob {
        value := p.wildcard(key.Name)
        b := strings.NewBuilder(p.Alloc)
        for i := range value.List { b.WriteString(value.List[i].Text); b.WriteByte(0) }
        stamp := cloneText(p.Alloc, b.String())
        b.Free()
        value.Free(p.Alloc)
        return stamp
    }
    name := p.canonicalTarget(key.Name, true)
    info := p.Host.Stat(name)
    mem.FreeString(p.Alloc, name)
    if !info.Exists { return cloneText(p.Alloc, "missing") }
    b := strings.NewBuilder(p.Alloc)
    var buf [strconv.MaxIntBase10Len]byte
    b.WriteString(strconv.FormatInt(buf[:], info.Info.ModTime, 10))
    b.WriteByte(':')
    b.WriteString(strconv.FormatInt(buf[:], info.Info.Size, 10))
    b.WriteByte(':')
    b.WriteString(strconv.FormatInt(buf[:], int64(info.Info.Mode), 10))
    stamp := cloneText(p.Alloc, b.String())
    b.Free()
    return stamp
}
