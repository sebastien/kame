package wasm

import (
	"kame/cli"
	"kame/core"
	"solod.dev/so/bytes"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/strconv"
)

func RenderPresentation(a mem.Allocator, text string) PureResult {
	return renderPresentation(a, text, nil)
}

// Presentation lives in a dedicated CLI instance, never in the execution heap.
type Presentation struct {
	Alloc mem.Allocator
	State cli.ProgressState
}

func NewPresentation(heap *Heap) *Presentation {
	p := mem.Alloc[Presentation](heap)
	p.Alloc = heap
	return p
}

func (p *Presentation) Observe(text string, scope int64, nowNS int64) PureResult {
	var value core.Value
	if len(text) > 1024*1024 || nowNS < 0 || !core.ParseJSON(mem.System, []byte(text), &value) {
		return invalidPresentation(mem.System)
	}
	defer value.Free(mem.System)
	if value.Kind != core.Record {
		return invalidPresentation(mem.System)
	}
	if presentationField(value, "type").Text == "reset" {
		if len(value.Record) != 1 {
			return invalidPresentation(mem.System)
		}
		p.State.Free(p.Alloc)
		p.State.Scope = scope
		return PureResult{}
	}
	if presentationField(value, "type").Text == "cycle" {
		if len(value.Record) != 1 {
			return invalidPresentation(mem.System)
		}
		p.State.FreeOutcomes(p.Alloc)
		return PureResult{}
	}
	observation := cli.ParseProgressObservation(value)
	if !observation.OK {
		return invalidPresentation(mem.System)
	}
	p.State.Scope = scope
	p.State.Observe(p.Alloc, observation, nowNS)
	return PureResult{}
}

func (p *Presentation) Render(text string) PureResult {
	return renderPresentation(mem.System, text, &p.State)
}

func invalidPresentation(a mem.Allocator) PureResult {
	return PureResult{Code: pureText(a, "OPT_VALUE_INVALID"), Message: pureText(a, "invalid presentation snapshot")}
}

func presentationField(value core.Value, name string) core.Value {
	for i := range value.Record {
		if value.Record[i].Key == name {
			return value.Record[i].Value
		}
	}
	return core.Value{}
}

func renderPresentation(a mem.Allocator, text string, state *cli.ProgressState) PureResult {
	var value core.Value
	if len(text) > 1024*1024 || !core.ParseJSON(a, []byte(text), &value) {
		return invalidPresentation(a)
	}
	defer value.Free(a)
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	if state != nil && presentationField(value, "op").Text == "counts" {
		e := json.NewEncoder(&buffer)
		e.BeginObject()
		e.Str("active")
		e.Int(int64(state.Counts.Active))
		e.Str("completed")
		e.Int(int64(state.Counts.Completed))
		e.Str("failed")
		e.Int(int64(state.Counts.Failed))
		e.Str("cancelled")
		e.Int(int64(state.Counts.Cancelled))
		e.EndObject()
		e.Flush()
		return PureResult{Text: pureText(a, buffer.String())}
	}
	var tracked cli.ProgressState
	var tracker *cli.ProgressState
	if state != nil {
		tracked = *state
		tracker = &tracked
		field := presentationField(value, "scope")
		if field.Kind != core.Nil {
			if field.Kind != core.String {
				return invalidPresentation(a)
			}
			scope, err := strconv.ParseInt(field.Text, 10, 64)
			if err != nil {
				return invalidPresentation(a)
			}
			tracked.Scope = scope
		}
	}
	now := presentationField(value, "nowNS")
	if now.Kind != core.Nil && (now.Kind != core.Int || now.Int < 0) {
		return invalidPresentation(a)
	}
	if !cli.WriteTrackedProgressDocument(&buffer, value, tracker, now.Int) {
		return invalidPresentation(a)
	}
	return PureResult{Text: pureText(a, buffer.String())}
}
