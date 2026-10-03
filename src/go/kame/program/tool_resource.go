package program

import (
 "kame/core"
 "kame/host"
 "solod.dev/so/mem"
 "solod.dev/so/slices"
 "solod.dev/so/strings"
)

func (p *Program) observeToolDependency(key core.ResourceKey) {
 state := mem.Alloc[externalValueState](p.Alloc)
 state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
 if p.Engine.AddOwned(key, produceTool, state, freeExternalValueState) == nil { freeExternalValueState(p.Alloc, state) }
}

func produceTool(c *core.EngineContext, nodeID int64) core.ProducerResult {
 _ = nodeID
 state := c.Context().(*externalValueState)
 p := state.Program
 index := p.toolIndex(state.Name)
 if index < 0 {
  p.Tools = slices.Append(p.Alloc, p.Tools, Tool{Name: cloneText(p.Alloc, state.Name), Declarative: true})
  index = len(p.Tools)-1
 }
 p.Tools[index].Declarative = true
 if p.Forwarding && !p.Tools[index].Resolved {
  completion := c.Completion()
  if completion.RequestID == 0 {
   payload := host.FilePayload(c.Allocator(), "resolve-tool", p.toolRequestName(state.Name))
   id := p.Eval.Requests.Submit(c.NodeID(), c.Generation(), c.Attempt(), host.RequestReadFile, payload)
   payload.Free(c.Allocator())
   c.Submit(id)
   return core.ProducerSubmitted
  }
  if completion.Diagnostic.Code != "" {
   completion.Value.Free(c.Allocator()); c.Fail(completion.Diagnostic); return core.ProducerFailed
  }
  if completion.HasValue && completion.Value.Kind == core.String { p.SetToolPath(state.Name, completion.Value.Text) }
  completion.Value.Free(c.Allocator())
 }
 resolved, ok := p.toolPath(state.Name)
 if !ok { c.Fail(failure(p.Alloc, "TOOL_MISSING", "cannot resolve tool: "+state.Name)); return core.ProducerFailed }
 file := mem.Alloc[externalFileState](p.Alloc)
 file.Program, file.Name, file.Tool = p, cloneText(p.Alloc, resolved), true
 if p.Engine.AddOwned(core.ResourceKey{Kind: core.ResourceFile, Name: resolved}, produceExternalFile, file, freeExternalFileState) == nil { freeExternalFileState(p.Alloc, file) }
 c.Publish(core.NewString(c.Allocator(), resolved))
 return core.ProducerCompleted
}

func (p *Program) declareTool(name string) {
 if name == "" { return }
 index := p.toolIndex(name)
 if index >= 0 { p.Tools[index].Declarative = true; return }
 p.Tools = slices.Append(p.Alloc, p.Tools, Tool{Name: cloneText(p.Alloc, name), Declarative: true})
}

func (p *Program) toolRequestName(name string) string {
 prefix := name + "="
 for i := len(p.Options.ToolOverrides)-1; i >= 0; i-- {
  if strings.HasPrefix(p.Options.ToolOverrides[i], prefix) { return p.Options.ToolOverrides[i][len(prefix):] }
 }
 return name
}
