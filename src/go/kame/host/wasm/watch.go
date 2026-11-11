package wasm

import (
	"bytes"
	"kame/core"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/program"

	"strings"
)

// RequestWatch retains all roots in one program. Validate the entire descriptor
// before registering roots; scheduling does not dispatch effects until Step.
func (r *Runtime) RequestWatch(data []byte) PureResult {
	var targets core.Value
	if !CompletionValueFromJSON(r.Alloc, data, &targets) {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid watch targets")}
	}
	defer targets.Free(r.Alloc)
	if targets.Kind != core.List || len(targets.List) == 0 || len(targets.List) > 1024 {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "watch targets must be a nonempty string list of at most 1024 entries")}
	}
	for i := range targets.List {
		target := targets.List[i]
		if target.Kind != core.String || target.Text == "" || strings.IndexByte(target.Text, 0) >= 0 {
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid watch target name")}
		}
	}
	started := r.RequestTarget(targets.List[0].Text)
	if started.Code != "" {
		return started
	}
	for i := 1; i < len(targets.List); i++ {
		next := r.Program.Start(targets.List[i].Text)
		if next.Diagnostic.Code != "" {
			result := PureResult{Code: pureText(r.Alloc, next.Diagnostic.Code), Message: pureText(r.Alloc, next.Diagnostic.Message)}
			next.Diagnostic.Free(r.Alloc)
			r.freeWatchHandles()
			r.Handle.Free()
			r.Handle = nil
			return result
		}
		r.WatchHandles = append(r.WatchHandles, next.Handle)
	}
	r.Watching = true
	r.Program.Options.Watch = true
	return PureResult{}
}

func (r *Runtime) watchHandle(index int) *program.Handle {
	if index == 0 {
		return r.Handle
	}
	return r.WatchHandles[index-1]
}

// WatchStateJSON is a repeat-safe snapshot. PollRetained must not drop root
// interest when a host asks whether the current build has settled.
func (r *Runtime) WatchStateJSON() PureResult {
	if !r.Watching || r.Program == nil || r.Handle == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no active watch session")}
	}
	buffer := bytes.Buffer{}
	e := json.NewEncoder(&buffer)
	e.BeginObject()
	busy := false
	e.Str("roots")
	e.BeginArray()
	for i := 0; i <= len(r.WatchHandles); i++ {
		h := r.watchHandle(i)
		polled := h.PollRetained()
		r.Program.ObserveDefinition(h)
		if !polled.Done {
			busy = true
		}
		e.BeginObject()
		e.Str("index")
		e.Int(int64(i))
		e.Str("done")
		e.Bool(polled.Done)
		e.Str("revision")
		e.Int(int64(h.Node.Revision))
		e.Str("generation")
		e.Int(int64(h.Node.Generation))
		if polled.Done && polled.Result.Diagnostic.Code != "" {
			diagnostic := bytes.Buffer{}
			program.WriteJSONDiagnostic(&diagnostic, polled.Result.Diagnostic)
			e.Str("diagnosticJSON")
			e.Str(diagnostic.String())
			diagnostic.Reset()
		} else if polled.Done && h.Definition {
			text := eval.Display(r.Alloc, polled.Result.Value)
			e.Str("value")
			e.Str(text)
			mem.FreeString(r.Alloc, text)
		} else if polled.Done && polled.Result.Path != "" {
			e.Str("value")
			e.Str(polled.Result.Path)
		}
		e.Str("kind")
		if h.Definition {
			e.Int(1)
		} else if polled.Result.Path != "" {
			e.Int(2)
		} else {
			e.Int(0)
		}
		e.EndObject()
		polled.Result.Free(r.Alloc)
	}
	e.EndArray()
	e.Str("busy")
	e.Bool(busy)
	e.Str("resources")
	e.BeginArray()
	keys := r.Program.FilesystemResources()
	for i := range keys {
		e.BeginObject()
		e.Str("kind")
		if keys[i].Kind == core.ResourceGlob {
			e.Str("glob")
		} else {
			e.Str("file")
		}
		e.Str("name")
		e.Str(keys[i].Name)
		if keys[i].Kind == core.ResourceFile {
			e.Str("metadata")
			e.Bool(r.Program.ResourceMetadataObserved(keys[i]))
			node := r.Program.Engine.Lookup(keys[i])
			if node != nil && node.Current && node.Signature.Mode == core.SignatureContent {
				const hex = "0123456789abcdef"
				var bytes [64]byte
				for j := range node.Signature.Digest {
					bytes[j*2] = hex[node.Signature.Digest[j]>>4]
					bytes[j*2+1] = hex[node.Signature.Digest[j]&15]
				}
				e.Str("signature")
				e.Str(string(bytes[:]))
			}
			if node != nil && node.Current {
				e.Str("missing")
				e.Bool(node.Latest.Kind == core.Nil)
			}
		}
		e.EndObject()
		keys[i].Free(r.Alloc)
	}
	mem.FreeSlice(nil, keys)
	e.EndArray()
	e.Str("changedInputs")
	e.BeginArray()
	for _, key := range r.Program.ChangedProductionInputs() {
		e.BeginObject()
		e.Str("kind")
		e.Str("file")
		e.Str("name")
		e.Str(key.Name)
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
	e.Flush()
	text := pureText(r.Alloc, buffer.String())
	buffer.Reset()
	return PureResult{Text: text}
}

func freeWatchKeys(a mem.Allocator, keys []core.ResourceKey) {
	for i := range keys {
		keys[i].Free(a)
	}
	mem.FreeSlice(nil, keys)
}

type watchBatch struct {
	Keys     []core.ResourceKey
	Observed []bool
	Result   PureResult
}

// parseWatchBatch owns keys and, when requested, the observed-only flags.
func (r *Runtime) parseWatchBatch(data []byte, withObserved bool) watchBatch {
	var batch core.Value
	if !CompletionValueFromJSON(r.Alloc, data, &batch) {
		return watchBatch{Result: PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid watch invalidation batch")}}
	}
	defer batch.Free(r.Alloc)
	if batch.Kind != core.List || len(batch.List) > 65536 {
		return watchBatch{Result: PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "watch invalidations must be a bounded resource list")}}
	}
	var keys []core.ResourceKey
	var observedOnly []bool
	for i := range batch.List {
		item := batch.List[i]
		var kind, name core.Value
		observed := false
		if item.Kind == core.Record {
			for j := range item.Record {
				if item.Record[j].Key == "kind" {
					kind = item.Record[j].Value
				}
				if item.Record[j].Key == "name" {
					name = item.Record[j].Value
				}
				if item.Record[j].Key == "observedOnly" {
					if item.Record[j].Value.Kind != core.Bool {
						freeWatchKeys(r.Alloc, keys)
						mem.FreeSlice(nil, observedOnly)
						return watchBatch{Result: PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "observedOnly must be a boolean")}}
					}
					observed = item.Record[j].Value.Bool
				}
			}
		}
		if kind.Kind != core.String || (kind.Text != "file" && kind.Text != "glob" && kind.Text != "tool") || name.Kind != core.String || name.Text == "" || strings.IndexByte(name.Text, 0) >= 0 {
			freeWatchKeys(r.Alloc, keys)
			mem.FreeSlice(nil, observedOnly)
			return watchBatch{Result: PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid watch resource key")}}
		}
		resourceKind := core.ResourceFile
		if kind.Text == "glob" {
			resourceKind = core.ResourceGlob
		}
		if kind.Text == "tool" {
			resourceKind = core.ResourceTool
		}
		keys = append(keys, core.NewResourceKey(r.Alloc, resourceKind, name.Text))
		if withObserved {
			observedOnly = append(observedOnly, observed)
		}
	}
	return watchBatch{Keys: keys, Observed: observedOnly}
}

// InvalidateWatch validates the entire batch before mutating graph state.
func (r *Runtime) InvalidateWatch(data []byte) PureResult {
	if !r.Watching || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no active watch session")}
	}
	batch := r.parseWatchBatch(data, true)
	if batch.Result.Code != "" {
		return batch.Result
	}
	defer freeWatchKeys(r.Alloc, batch.Keys)
	defer mem.FreeSlice(nil, batch.Observed)
	for i := range batch.Keys {
		if batch.Observed[i] {
			r.Program.NoteProductionInputChange(batch.Keys[i])
		} else {
			r.Program.InvalidateResource(batch.Keys[i])
		}
	}
	return PureResult{}
}

// WatchCancellationMessage renders the same cancellation text as the native CLI.
func (r *Runtime) WatchCancellationMessage(data []byte) PureResult {
	if !r.Watching || r.Program == nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "no active watch session")}
	}
	batch := r.parseWatchBatch(data, false)
	if batch.Result.Code != "" {
		return batch.Result
	}
	message := r.Program.ProductionCancellationMessage(batch.Keys)
	freeWatchKeys(r.Alloc, batch.Keys)
	defer mem.FreeString(r.Alloc, message)
	return PureResult{Text: pureText(r.Alloc, message)}
}

func (r *Runtime) freeWatchHandles() {
	for i := range r.WatchHandles {
		r.WatchHandles[i].Free()
	}
	mem.FreeSlice(nil, r.WatchHandles)
	r.WatchHandles = nil
	r.Watching = false
}
