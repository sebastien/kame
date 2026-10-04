package program

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// A context record names the physical output set, independently of the rule's
// environment. Its digest binds execution context to the successful output times.
// Hosts transport facts and opaque bytes; the portable runtime decides freshness.
type fileContextState struct {
	Rendered renderResult
	Paths    []string
	Outputs  int
	Phase    int
	Times    core.Value
 DigestValid bool
}

func (p *Program) freeFileContext(state *fileContextState) {
	if state == nil {
		return
	}
	mem.FreeString(p.Alloc, state.Rendered.Commands)
	eval.FreeEffects(p.Alloc, state.Rendered.Effects)
	freeStrings(p.Alloc, state.Rendered.WritePaths)
	slices.Free(p.Alloc, state.Rendered.LineSpans)
	state.Rendered.Diagnostic.Free(p.Alloc)
	freeStrings(p.Alloc, state.Paths)
	state.Times.Free(p.Alloc)
	mem.Free(p.Alloc, state)
}

func (p *Program) beginFileContext(c *core.EngineContext, index int, rendered renderResult) core.ProducerResult {
	entry := &p.Instances[index]
	state := mem.Alloc[fileContextState](p.Alloc)
	state.Rendered, state.Outputs, state.DigestValid = rendered, len(entry.Plan.Outputs), true
	for i := range entry.Plan.Outputs {
		state.Paths = slices.Append(p.Alloc, state.Paths, p.canonicalTarget(entry.Plan.Outputs[i], true))
	}
	inputs := entry.Plan.Inputs
	if entry.Plan.Resolved {
		inputs = entry.Plan.ResolvedInputs
	}
 resources := entry.Plan.ResourceInputs
 if entry.Plan.Resolved { resources = entry.Plan.ResolvedResourceInputs }
	for i := range inputs {
  if i < len(resources) && resources[i].OrderOnly { continue }
		if isFileName(inputs[i]) {
			p.appendContextPath(state, inputs[i])
		}
	}
	for i := range entry.Node.Dynamic {
  if slices.Contains(entry.Node.OrderOnly, entry.Node.Dynamic[i]) { continue }
		if entry.Node.Dynamic[i].Key.Kind == core.ResourceFile {
			p.appendContextPath(state, entry.Node.Dynamic[i].Key.Name)
		}
	}
	var identity, context hashSink
	identity.state, context.state = newSHA256(), newSHA256()
	identity.appendText("kame-file-context-v1")
	identity.appendText(state.Paths[0])
	identity.state.Sum(entry.FileContextKey[:])
	context.appendText("kame-file-context-v1")
	if entry.Kash { context.appendText("kash-v1"); context.appendText(p.Parsed.Source.Text) } else { context.appendText("shell-v1") }
	for i := range entry.Shell { context.appendText(entry.Shell[i]) }
	for i := range entry.Environment {
		context.appendText(entry.Environment[i])
	}
 if entry.NewerInputs != nil {
  // The selector changes after publication. Hash its stable authored context,
  // rather than commands containing the transient newer-input subset.
  context.appendText("newer-input-context-v1")
  context.appendText(p.Parsed.Source.Text)
  context.appendU64(uint64(len(p.Configuration)))
  for i := range p.Configuration { context.appendText(p.Configuration[i]) }
  context.appendU64(uint64(len(p.Options.Shell)))
  for i := range p.Options.Shell { context.appendText(p.Options.Shell[i]) }
  context.appendU64(uint64(len(p.Eval.DefinitionArgs)))
  for i := range p.Eval.DefinitionArgs {
   encoded := EncodeFingerprintValue(p.Alloc, p.Eval.DefinitionArgs[i])
   if len(encoded) == 0 { state.DigestValid = false }
   context.appendU64(uint64(len(encoded)))
   context.state.Write(encoded)
   slices.Free(p.Alloc, encoded)
  }
 } else {
	context.appendText(rendered.Commands)
	for i := range rendered.Effects {
		context.appendU64(uint64(rendered.Effects[i].Kind))
		context.appendU64(uint64(len(rendered.Effects[i].Data)))
		context.state.Write(rendered.Effects[i].Data)
	}
	for i := range rendered.WritePaths {
		context.appendText(rendered.WritePaths[i])
	}
 }
	for i := range state.Paths {
		context.appendText(state.Paths[i])
	}
	context.state.Sum(entry.FileContextDigest[:])
	entry.FileContextWanted = entry.ScopedEnvironment || len(entry.Rule.Environment) != 0 || entry.Rule.Metadata != nil || entry.ScopedShell
	entry.FileContext = state
	if !p.Forwarding {
		var times []core.Value
		for i := range state.Paths {
			result := p.Host.Stat(state.Paths[i])
			value := core.Value{Kind: core.Nil}
			if result.Exists {
				value.Kind, value.Int = core.Int, result.Info.ModTime
			}
			times = slices.Append(p.Alloc, times, value)
		}
		state.Times = core.NewList(p.Alloc, times)
		slices.Free(p.Alloc, times)
		name := p.fileContextPath(entry)
		bytes, err := p.Host.ReadFile(p.Alloc, name)
		mem.FreeString(p.Alloc, name)
		record := core.Value{Kind: core.Nil}
		if err == nil {
			record = core.NewBytes(p.Alloc, bytes)
			entry.FileContextWanted = true
		}
		mem.FreeSlice(p.Alloc, bytes)
		p.decideFileContext(entry, state, record)
		record.Free(p.Alloc)
		return p.finishFileContext(c, index)
	}
	p.submitFileTimes(c, state.Paths)
	return core.ProducerSubmitted
}

func (p *Program) appendContextPath(state *fileContextState, name string) {
	canonical := p.canonicalTarget(name, true)
	// Outputs remain a prefix even when a dependency happens to name one.
	if slices.Contains(state.Paths[state.Outputs:], canonical) {
		mem.FreeString(p.Alloc, canonical)
		return
	}
	state.Paths = slices.Append(p.Alloc, state.Paths, canonical)
}

func (p *Program) submitFileTimes(c *core.EngineContext, names []string) {
	b := strings.NewBuilder(p.Alloc)
	e := json.NewEncoder(&b)
	e.BeginArray()
	for i := range names {
		e.Str(names[i])
	}
	e.EndArray()
	e.Flush()
	payload := host.FilePayload(p.Alloc, host.OpFileTimes, b.String())
	b.Free()
	p.nextRequest++
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestReadFile, Payload: payload})
	c.Submit(p.nextRequest)
}

func (p *Program) continueFileContext(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	state := entry.FileContext
	completion := c.Completion()
	if completion.RequestID == 0 {
		return core.ProducerWaiting
	}
	if state.Phase == 0 {
		state.Times = completion.Value
		completion.Diagnostic.Free(p.Alloc)
		state.Phase = 1
		payload := host.CacheGetPayload(p.Alloc, entry.FileContextKey[:])
		p.nextRequest++
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestCacheGet, Payload: payload})
		c.Submit(p.nextRequest)
		return core.ProducerSubmitted
	}
	if completion.Diagnostic.Code != "" {
		entry.FileContextWanted = true
	}
	if completion.Value.Kind == core.Bytes {
		entry.FileContextWanted = true
	}
	p.decideFileContext(entry, state, completion.Value)
	completion.Value.Free(p.Alloc)
	completion.Diagnostic.Free(p.Alloc)
	return p.finishFileContext(c, index)
}

func contextTime(value core.Value, out *int64) bool {
	if value.Kind == core.Int {
		*out = value.Int
		return true
	}
	if value.Kind != core.String {
		return false
	}
	n, err := strconv.ParseInt(value.Text, 10, 64)
	if err != nil {
		return false
	}
	*out = n
	return true
}

func contextRecord(base []byte, times []core.Value, outputs int, digest []byte) bool {
	if len(times) < outputs {
		return false
	}
	var sink hashSink
	sink.state = newSHA256()
	sink.state.Write(base)
	for i := 0; i < outputs; i++ {
		var timestamp int64
		if !contextTime(times[i], &timestamp) {
			return false
		}
		sink.appendU64(uint64(timestamp))
	}
	sink.state.Sum(digest)
	return true
}

func (p *Program) decideFileContext(entry *instance, state *fileContextState, record core.Value) {
	_ = p
	entry.Plan.Freshness = Stale
	if !state.DigestValid || entry.Rule.Always || state.Times.Kind != core.List || len(state.Times.List) != len(state.Paths) || len(state.Paths) <= state.Outputs {
		return
	}
	var oldest int64
	for i := 0; i < state.Outputs; i++ {
		var timestamp int64
		if !contextTime(state.Times.List[i], &timestamp) {
			return
		}
		if i == 0 || timestamp < oldest {
			oldest = timestamp
		}
	}
	for i := state.Outputs; i < len(state.Times.List); i++ {
		var timestamp int64
		if !contextTime(state.Times.List[i], &timestamp) || timestamp > oldest {
			return
		}
	}
	if entry.FileContextWanted {
		var digest [32]byte
		if record.Kind != core.Bytes || len(record.Bytes) != 32 || !contextRecord(entry.FileContextDigest[:], state.Times.List, state.Outputs, digest[:]) {
			return
		}
		for i := range digest {
			if digest[i] != record.Bytes[i] {
				return
			}
		}
	}
	entry.Plan.Freshness = Fresh
}

func (p *Program) finishFileContext(c *core.EngineContext, index int) core.ProducerResult {
	state := p.Instances[index].FileContext
	rendered := state.Rendered
	state.Rendered = renderResult{}
	p.freeFileContext(state)
	p.Instances[index].FileContext, p.Instances[index].FileContextReady = nil, true
	return p.finishRenderedRule(c, index, rendered)
}

func (p *Program) fileContextPath(entry *instance) string {
	hex := cacheHex(p.Alloc, entry.FileContextKey[:])
	root := path.Join(p.Alloc, p.Options.Directory, ".kame/cache/file-context")
	name := path.Join(p.Alloc, root, hex)
	mem.FreeString(p.Alloc, hex)
	mem.FreeString(p.Alloc, root)
	return name
}

func (p *Program) saveNativeFileContext(entry *instance) {
	if !entry.FileContextWanted {
		return
	}
	var values []core.Value
	for i := range entry.Plan.Outputs {
		name := p.canonicalTarget(entry.Plan.Outputs[i], true)
		result := p.Host.Stat(name)
		mem.FreeString(p.Alloc, name)
		value := core.Value{Kind: core.Nil}
		if result.Exists {
			value.Kind, value.Int = core.Int, result.Info.ModTime
		}
		values = slices.Append(p.Alloc, values, value)
	}
	var digest [32]byte
	ok := contextRecord(entry.FileContextDigest[:], values, len(entry.Plan.Outputs), digest[:])
	slices.Free(p.Alloc, values)
	if !ok {
		return
	}
	name := p.fileContextPath(entry)
	if p.mkdirParent(name) {
		_ = p.Host.WriteFileAtomic(name, digest[:], 0o644, true)
	}
	mem.FreeString(p.Alloc, name)
}
