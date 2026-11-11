package program

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/rule"
	"path"
	"slices"
)

// Timings are advisory, inclusive wall times: parallel dependencies are already
// accounted for by the observed parent duration. They never enter a signature.
const timingMaxMS int64 = 7 * 24 * 60 * 60 * 1000

func (p *Program) timingNow() int64 {
	if p.Options.TimingNow != nil {
		return p.Options.TimingNow(p.Options.TimingClock)
	}
	return p.Host.Monotonic()
}

func (p *Program) timingPath(entry *instance) string {
	hex := cacheHex(p.Alloc, entry.timingKey[:])
	name := path.Join(p.Options.Directory, ".kame/cache/timing", hex)
	mem.FreeString(p.Alloc, hex)
	return name
}

func (p *Program) timingEnabled(entry *instance) bool {
	return p.Options.TrackTimings && p.Host != nil && !p.Options.DryRun && entry.Rule.Kind != rule.ServiceRule && entry.Rule != p.automaticCleanRule
}

func (p *Program) timingLoad(c *core.EngineContext, entry *instance) core.ProducerResult {
	if !p.timingEnabled(entry) || entry.timingReady {
		return core.ProducerCompleted
	}
	var data []byte
	if entry.timingPending {
		completion := c.Completion()
		if completion.RequestID == 0 {
			return core.ProducerWaiting
		}
		entry.timingPending = false
		if completion.Diagnostic.Code == "" && completion.Value.Kind == core.Bytes {
			data = slices.Clone(completion.Value.Bytes)
		}
		completion.Value.Free(p.Alloc)
		completion.Diagnostic.Free(p.Alloc)
	} else {
		key := p.cacheKey(entry)
		digest := core.NewDigest()
		digest.Text("kame-timing-v1")
		digest.Write(key)
		digest.Sum(entry.timingKey[:])
		mem.FreeSlice(nil, key)
		if p.Forwarding {
			entry.timingPending = true
			p.submitFileContextRequest(c, host.RequestCacheGet, host.CacheGetPayload(p.Alloc, entry.timingKey[:]))
			return core.ProducerSubmitted
		}
		name := p.timingPath(entry)
		info := p.Host.Lstat(name)
		if info.Exists && info.Info.Regular && info.Info.Size == 12 {
			read, err := p.Host.ReadFile(p.Alloc, name)
			if err == nil {
				data = read
			} else {
				mem.FreeSlice(nil, read)
			}
		}
		mem.FreeString(p.Alloc, name)
	}
	entry.timingReady = true
	entry.timingStarted = p.timingNow()
	if len(data) == 12 && string(data[:4]) == "KTM1" {
		at := 4
		duration := int64(takeU64(data, &at))
		p.timingObserve(entry, duration)
	}
	mem.FreeSlice(nil, data)
	return core.ProducerCompleted
}

func (p *Program) timingObserve(entry *instance, duration int64) {
	if duration <= 0 || duration > timingMaxMS || entry.timingEstimate > 0 {
		return
	}
	entry.timingEstimate = duration
	p.emit(Event{Kind: TargetEstimate, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, EstimateMS: duration})
}

// Only measured execution refreshes history. Pure cache hits (including empty
// aggregates whose descendants all hit) cannot replace it with lookup latency.
func (p *Program) timingDidWork(root *core.Node, since int64) bool {
	var pending []*core.Node
	var seen []*core.Node
	pending = append(pending, root)
	worked := false
	for len(pending) != 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node == nil || slices.Contains(seen, node) {
			continue
		}
		seen = append(seen, node)
		if index := p.instanceIndex(node); index >= 0 && p.Instances[index].timingExecuted && p.Instances[index].timingExecutedAt >= since {
			worked = true
			break
		}
		for i := range node.Static {
			pending = append(pending, node.Static[i])
		}
		for i := range node.Dynamic {
			pending = append(pending, node.Dynamic[i])
		}
	}
	mem.FreeSlice(nil, pending)
	mem.FreeSlice(nil, seen)
	return worked
}

func (p *Program) timingSave(entry *instance) {
	if !p.timingEnabled(entry) || !entry.timingReady || entry.Plan.Freshness == Fresh {
		return
	}
	if !p.timingDidWork(entry.Node, entry.timingStarted) {
		return
	}
	duration := (p.timingNow() - entry.timingStarted) / 1000000
	if duration < 1 {
		duration = 1
	}
	if duration > timingMaxMS {
		return
	}
	data := make([]byte, 12)
	copy(data[:4], "KTM1")
	for i := 0; i < 8; i++ {
		data[4+i] = byte(uint64(duration) >> uint(i*8))
	}
	if p.Forwarding {
		p.nextRequest++
		p.Outbound = append(p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestCachePut, Payload: host.CachePutPayload(p.Alloc, entry.timingKey[:], data)})
	} else {
		name := p.timingPath(entry)
		if p.mkdirParent(name) {
			_ = p.Host.WriteFileAtomic(name, data, 0o644, true)
			directory := path.Dir(name)
			p.pruneCacheDirectory(directory)
			mem.FreeString(p.Alloc, directory)
		}
		mem.FreeString(p.Alloc, name)
	}
	mem.FreeSlice(nil, data)
}
