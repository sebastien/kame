package wasm

import (
	"bytes"
	"encoding/hex"
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"kame/lang/source"
	"kame/program"
	"path"
	"strings"
)

type declarationRequests struct {
	completed map[declarationRequestKey]core.Completion
	pending   declarationRequestKey
	id        int64
}

type declarationRequestKey struct {
	kind host.RequestKind
	name string
}

func lookupDeclarationFiles(state any, scan string, kinds int) core.Completion {
	r := state.(*Runtime)
	name := scan
	if path.IsAbs(r.Directory) && !path.IsAbs(scan) && !core.IsResourceURIName(scan) {
		name = path.Join(r.Directory, scan)
		defer mem.FreeString(r.Alloc, name)
	}
	key := host.DeclarationDiscoveryKey(r.Alloc, name, kinds)
	defer mem.FreeString(r.Alloc, key)
	return r.lookupDeclarationRequest(declarationRequestKey{kind: host.RequestReadFile, name: key})
}

func lookupDeclarationSource(state any, name string) core.Completion {
	return state.(*Runtime).lookupDeclarationRequest(declarationRequestKey{kind: host.RequestLoadSource, name: name})
}

func (r *Runtime) lookupDeclarationRequest(key declarationRequestKey) core.Completion {
	requests := &r.declarationRequests
	if result, ok := requests.completed[key]; ok {
		result.Value = result.Value.Clone(r.Alloc)
		result.Diagnostic = result.Diagnostic.Clone(r.Alloc)
		return result
	}
	if requests.id == 0 {
		var payload core.Value
		if key.kind == host.RequestLoadSource {
			payload = core.NewString(r.Alloc, key.name)
		} else {
			payload = host.FilePayload(r.Alloc, host.OpDeclarationFiles, key.name)
		}
		requests.id = r.Eval.Requests.Submit(1, 0, 0, key.kind, payload)
		requests.pending = declarationRequestKey{kind: key.kind, name: strings.Clone(key.name)}
		payload.Free(r.Alloc)
	}
	return core.Completion{}
}

func (r *Runtime) saveDeclarationSnapshots(snapshots []program.DeclarationSnapshot) {
	for i := range r.declarationSnapshots {
		r.declarationSnapshots[i].Free(r.Alloc)
	}
	mem.FreeSlice(nil, r.declarationSnapshots)
	r.declarationSnapshots = snapshots
}

// DeclarationStateJSON is also available after registration fails so hosts can
// watch the observations that selected the failing declarations.
func (r *Runtime) DeclarationStateJSON() PureResult {
	var buffer bytes.Buffer
	e := json.NewEncoder(&buffer)
	e.BeginArray()
	for _, snapshot := range r.declarationSnapshots {
		key := snapshot.Key.Name
		e.BeginObject()
		e.Str("kind")
		if snapshot.Key.Kind == core.ResourceDeclarationSource {
			e.Str("declaration-source")
		} else {
			e.Str("discovery")
			var kinds int
			key, kinds = host.DeclarationDiscovery(key)
			e.Str("kindMask")
			e.Int(int64(kinds))
		}
		e.Str("name")
		e.Str(snapshot.Key.Name)
		e.Str("scan")
		e.Str(host.DiscoveryScan(key))
		e.Str("pattern")
		pattern := snapshot.Pattern
		if pattern == "" {
			pattern = host.DiscoveryPattern(key)
		}
		e.Str(pattern)
		e.Str("directory")
		e.Str(snapshot.Directory)
		e.Str("signature")
		e.Str(snapshot.Fingerprint)
		if snapshot.Source != "" {
			e.Str("source")
			e.Str(snapshot.Source)
			e.Str("start")
			e.Int(int64(snapshot.Span.Start))
			e.Str("end")
			e.Int(int64(snapshot.Span.End))
		}
		e.EndObject()
	}
	e.EndArray()
	e.Flush()
	return PureResult{Text: pureText(r.Alloc, buffer.String())}
}

func descriptorCompositionDiscoveries(a mem.Allocator, value core.Value) ([]program.DeclarationSnapshot, bool) {
	var snapshots []program.DeclarationSnapshot
	for _, item := range host.PayloadList(value, "compositionDiscoveries") {
		name, directory := host.PayloadText(item, "name"), host.PayloadText(item, "directory")
		pattern, authored := host.PayloadText(item, "pattern"), host.PayloadText(item, "source")
		fingerprint := host.PayloadText(item, "signature")
		start, end := host.PayloadInt(item, "start"), host.PayloadInt(item, "end")
		if item.Kind != core.Record || name == "" || !path.IsAbs(directory) || pattern == "" || authored == "" || start < 0 || end <= start || end > 2147483647 || fingerprint == "" {
			freeCompositionDiscoveries(a, snapshots)
			return nil, false
		}
		snapshots = append(snapshots, program.DeclarationSnapshot{Key: core.NewResourceKey(a, core.ResourceDiscovery, name), Directory: pureText(a, directory), Fingerprint: pureText(a, fingerprint), Pattern: pureText(a, pattern), Source: pureText(a, authored), Span: source.Span{Start: int(start), End: int(end)}})
	}
	return snapshots, true
}

func freeCompositionDiscoveries(a mem.Allocator, snapshots []program.DeclarationSnapshot) {
	for i := range snapshots {
		snapshots[i].Free(a)
	}
	mem.FreeSlice(nil, snapshots)
}

// DeclarationWatchFingerprint compares new candidates against the registration
// signature. It can be used even when no program compiled successfully.
func DeclarationWatchFingerprint(a mem.Allocator, data []byte) PureResult {
	var value core.Value
	if !CompletionValueFromJSON(a, data, &value) {
		return PureResult{Code: "PARSE_ERR", Message: "invalid declaration fingerprint descriptor"}
	}
	defer value.Free(a)
	name := host.PayloadText(value, "name")
	directory := host.PayloadText(value, "directory")
	var paths core.Value
	for _, field := range value.Record {
		if field.Key == "candidates" {
			paths = field.Value
			break
		}
	}
	key, _ := host.DeclarationDiscovery(name)
	if name == "" || directory == "" || paths.Kind != core.List || strings.ContainsRune(directory, 0) || (strings.ContainsRune(key, 0) && host.DiscoveryScan(key) == key) {
		return PureResult{Code: "PARSE_ERR", Message: "invalid declaration resource"}
	}
	candidates := paths.Clone(a)
	result := program.DeclarationMembership(a, directory, name, candidates)
	defer result.Value.Free(a)
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "" {
		return PureResult{Code: pureText(a, result.Diagnostic.Code), Message: pureText(a, result.Diagnostic.Message)}
	}
	signature := core.ValueSignature(result.Value)
	return PureResult{Text: pureText(a, hex.EncodeToString(signature.Digest[:]))}
}

func (r *Runtime) configureDeclarationRead(options *program.Options) {
	if options.ForwardRequests {
		options.DeclarationRead = lookupDeclarationFiles
		options.DeclarationSource = lookupDeclarationSource
		options.DeclarationHost = r
	}
}

func (r *Runtime) completeDeclarationRead(request host.Request, value core.Value, d diagnostic.Diagnostic) bool {
	requests := &r.declarationRequests
	if requests.id == 0 || request.ID != requests.id || request.Kind != requests.pending.kind || host.PayloadPath(request.Payload) != requests.pending.name {
		return false
	}
	if requests.completed == nil {
		requests.completed = make(map[declarationRequestKey]core.Completion)
	}
	requests.completed[requests.pending] = core.Completion{Value: value, HasValue: d.Code == "", Diagnostic: d}
	requests.id, requests.pending = 0, declarationRequestKey{}
	return true
}

func (r *Runtime) freeDeclarationReads() {
	for _, completion := range r.declarationRequests.completed {
		completion.Value.Free(r.Alloc)
		completion.Diagnostic.Free(r.Alloc)
	}
	r.declarationRequests = declarationRequests{}
}
