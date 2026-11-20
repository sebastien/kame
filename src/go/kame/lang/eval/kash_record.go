package eval

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"slices"
	"strings"
	"unicode/utf8"
)

func closeKashReader(state *kashLoopState) {
	if stage := state.RecordStage; stage != nil {
		stage.borrowedReader, state.RecordStage = nil, nil
	}
	if state.ReaderID != 0 && state.Program.RecordReaderCloser != nil {
		state.Program.RecordReaderCloser.CloseRecordReader(state.ReaderID)
		state.ReaderID = 0
	}
}

func (p *Program) kashRecordSource(scope *Scope, e *expr.Expr, state *kashLoopState, c *Context) Result {
	source := e.Items[0].Items[0]
	stage := c.functionStage
	if source.Kind == expr.Symbol && source.Text == "stdin" {
		if c.InputClosed || (stage == nil && !p.InputSupplied) || (stage != nil && stage.cancelled) {
			state.Ready, state.Done = true, true
			return Result{}
		}
		if stage != nil && c.RecordBody {
			return failure(c.Run, "KASH_DOUBLE_CONSUME", source.Span, "callable stage stdin already has an active record loop")
		}
		state.Stdin = true
	} else {
		r := p.kashRecordFileSource(scope, e, state, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
	}
	stagePort := state.Stdin && stage != nil && !stage.borrowedInput
	if p.RecordReaderCloser == nil && !stagePort {
		return failure(c.Run, "FEATURE_UNSUP", source.Span, "host does not support incremental record readers")
	}
	state.Delimiter = 10
	if e.RecordFrame == ":nul" {
		state.Delimiter = 0
	}
	state.Ready = true
	if stagePort {
		return Result{}
	}
	p.NextRecordReader++
	state.ReaderID = p.NextRecordReader
	if state.Stdin && stage != nil {
		stage.borrowedReader, state.RecordStage = state, stage
	}
	return Result{}
}

func (p *Program) kashRecordFileSource(scope *Scope, e *expr.Expr, state *kashLoopState, c *Context) Result {
	source := e.Items[0].Items[0]
	before, paths := len(c.Effects), len(c.WritePaths)
	previous := c.sequencedOperand
	c.sequencedOperand = true
	r := p.evaluate(c.Engine, scope, source, c)
	c.sequencedOperand = previous
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	name := ""
	if r.Value.IsPathText() {
		name = r.Value.Text
	} else if r.Value.Kind == core.Resource && r.Value.Resource.Kind == core.ResourceFile {
		name = r.Value.Resource.Name
	}
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		r.Free(c.Run)
		return failure(c.Run, "EXPR_INVALID", source.Span, "record source requires one nonempty local path or file resource")
	}
	if core.IsResourceURIName(name) {
		parsed := core.ParseResourceURI(c.Run, name)
		valid := parsed.Error == "" && parsed.URI.Scheme == "file"
		parsed.URI.Free()
		if !valid {
			r.Free(c.Run)
			return failure(c.Run, "EXPR_INVALID", source.Span, "record source requires a local file")
		}
	}
	resolved := c.CanonicalPath(name)
	state.Path = owned(p.Alloc, resolved)
	mem.FreeString(c.Run, resolved)
	r.Free(c.Run)
	if !c.Allows(Read, state.Path) {
		return failure(c.Run, "CAP_DENIED", source.Span, "record source requires read access")
	}
	p.retainControlEffects(state.Control, c, scope, before, paths)
	return Result{}
}

func (p *Program) kashRecordNext(e *expr.Expr, state *kashLoopState, c *Context) Result {
	if state.Stdin && c.functionStage != nil && !c.functionStage.borrowedInput {
		result := c.functionStage.readRecord(c, byte(state.Delimiter), e.Span)
		if result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
		return p.kashRecordValue(e, state, c, result.Value)
	}
	if state.Stdin && c.functionStage != nil && c.functionStage.cancelled && state.RequestID == 0 {
		state.Done = true
		return Result{}
	}
	if !state.Stdin && !demandProcessInput(c, state.Path) {
		return Result{Waiting: true}
	}
	if state.RequestID == 0 {
		payload := host.RecordPayload(c.Run, state.ReaderID, state.Path, state.Delimiter, state.Stdin)
		state.RequestID = c.Submit(host.RequestRecordRead, payload)
		payload.Free(c.Run)
		if state.RequestID == 0 {
			return failure(c.Run, "HOST_FAIL", e.Span, "cannot request record input")
		}
		return Result{Waiting: true}
	}
	if c.Completion().RequestID != state.RequestID {
		return Result{Waiting: true}
	}
	completion := c.TakeCompletion()
	state.RequestID = 0
	if completion.Diagnostic.Code != "" {
		completion.Value.Free(c.Run)
		diagnostic := completion.Diagnostic.Clone(c.Run)
		completion.Diagnostic.Free(c.Run)
		if diagnostic.Source == "" && diagnostic.Span.Start == 0 && diagnostic.Span.End == 0 {
			diagnostic.Span.Start, diagnostic.Span.End = e.Span.Start, e.Span.End
		}
		return Result{Diagnostic: diagnostic}
	}
	return p.kashRecordValue(e, state, c, completion.Value)
}

func (p *Program) kashRecordValue(e *expr.Expr, state *kashLoopState, c *Context, value core.Value) Result {
	defer value.Free(c.Run)
	if value.Kind == core.Nil {
		state.Done = true
		return Result{}
	}
	if value.Kind != core.String || len(value.Text) == 0 || len(value.Text) > host.RecordLimit {
		return failure(c.Run, "KASH_RECORD_LIMIT", e.Span, "record exceeds 64 KiB or host returned an invalid record")
	}
	text := value.Text
	if at := strings.IndexByte(text, byte(state.Delimiter)); at >= 0 && at != len(text)-1 {
		return failure(c.Run, "HOST_FAIL", e.Span, "host returned more than one framed record")
	}
	if text[len(text)-1] == byte(state.Delimiter) {
		text = text[:len(text)-1]
		if state.Delimiter == 10 && len(text) != 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
		}
	}
	if !utf8.Valid([]byte(text)) {
		return failure(c.Run, "KASH_FRAME_ENCODING", e.Span, "record is not valid UTF-8")
	}
	state.Iterable = core.NewString(p.Alloc, text)
	return Result{}
}

// callPathWithin reports whether callPath names path or a nested call below it.
// Call paths use "/" only as a separator, so the prefix includes it.
func callPathWithin(callPath string, path string) bool {
	return callPath == path || strings.HasPrefix(callPath, path+"/")
}

// The completed record is never replayed. Discard its operation progress rather
// than keeping one suspended-application history per input record. Async jobs
// and invocation-owned defers retain their independently owned contexts.
func (p *Program) retireRecordProgress(c *Context) {
	p.retireProgress(c.Engine.NodeID(), c.CallPath, c.Engine)
}

// ReleaseStatement queues execution history for collection at a quiet driver
// boundary. Engine cancellation alone does not acknowledge host reaping.
func (p *Program) ReleaseStatement(node *core.Node) {
	if node == nil {
		return
	}
	if !slices.Contains(p.releasedStatements, node) {
		p.releasedStatements = append(p.releasedStatements, node)
	}
}

func (p *Program) retireStatement(node *core.Node) bool {
	if node.ID == 0 {
		return true
	}
	id := node.ID
	if !p.Engine.Retire(node) {
		return false
	}
	retired := p.retireProgress(id, "", nil)
	for i := len(p.finalizers) - 1; i >= 0; i-- {
		s := p.finalizers[i]
		if !retired[s.NodeID] || s.Stage != 4 {
			continue
		}
		for _, task := range p.Processes {
			if task.Finalizer == s {
				task.Finalizer, task.Context.finalizer = nil, nil
			}
		}
		freeFinalizer(p.Alloc, s)
		p.finalizers = slices.Delete(p.finalizers, i, i+1)
	}
	return true
}

func (p *Program) retireProgress(node int64, path string, engine *core.EngineContext) map[int64]bool {
	p.retireRecordNode(node, path)
	var retired map[int64]bool
	if engine == nil {
		retired = map[int64]bool{node: true}
	}
	// Completed callable/with scopes are not authored retained cleanup. Release
	// their roots, consumer edges, worker nodes and contexts before the next item.
	for pass := 0; pass < MaxEvaluationDepth; pass++ {
		removed := false
		for _, s := range p.DeferScopes {
			if !s.Done || !s.Observed || !callPathWithin(s.CallPath, path) || engine == nil && !retired[s.Owner] {
				continue
			}
			if s.Root != nil {
				p.Engine.Release(s.Root)
				s.Root = nil
			}
			if engine != nil && s.Owner == node && s.Node != nil {
				engine.ForgetDependency(s.Node.Key)
			}
		}
		for i := len(p.DeferScopes) - 1; i >= 0; i-- {
			s := p.DeferScopes[i]
			if !s.Done || !s.Observed || s.Root != nil || !callPathWithin(s.CallPath, path) || engine == nil && !retired[s.Owner] {
				continue
			}
			if s.Node != nil {
				id := s.Node.ID
				if !p.Engine.Retire(s.Node) {
					continue
				}
				p.retireRecordNode(id, path)
				if retired != nil {
					retired[id] = true
				}
				s.Node = nil
				removed = true
			}
			complete := true
			for j := range s.Actions {
				action := s.Actions[j]
				if action.Node != nil {
					id := action.Node.ID
					if p.Engine.Retire(action.Node) {
						action.Node = nil
						p.retireRecordNode(id, path)
						if retired != nil {
							retired[id] = true
						}
						removed = true
					} else {
						complete = false
					}
				}
			}
			if !complete {
				continue
			}
			p.freeDeferScope(s)
			p.DeferScopes = slices.Delete(p.DeferScopes, i, i+1)
			removed = true
		}
		if !removed {
			break
		}
	}
	return retired
}

func (p *Program) retireRecordNode(node int64, path string) {
	for i := len(p.OperationStates) - 1; i >= 0; i-- {
		state := p.OperationStates[i]
		if state.NodeID != node || !callPathWithin(state.CallPath, path) {
			continue
		}
		if state.Free != nil {
			state.Free(p.Alloc, state.Value)
		}
		mem.FreeString(p.Alloc, state.CallPath)
		p.OperationStates = slices.Delete(p.OperationStates, i, i+1)
	}
	for i := len(p.OperationMemos) - 1; i >= 0; i-- {
		state := p.OperationMemos[i]
		if state.NodeID == node && callPathWithin(state.CallPath, path) {
			p.discardArgumentMemo(state)
		}
	}
}
