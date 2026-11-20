package eval

import (
	"bytes"
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/source"
	"strconv"
)

type preparedCallable struct {
	callee core.Value
	args   []core.Value
	span   source.Span
}

func (f *preparedCallable) clone(a mem.Allocator) preparedCallable {
	out := preparedCallable{callee: borrowValue(a, f.callee), span: f.span, args: make([]core.Value, len(f.args))}
	for i, arg := range f.args {
		out.args[i] = borrowValue(a, arg)
	}
	return out
}

func (f *preparedCallable) free(a mem.Allocator) {
	freeCallables(a, &f.callee)
	f.callee.Free(a)
	freeValuesWithCallables(a, f.args)
}

type functionStage struct {
	context        Context
	call           preparedCallable
	node           *core.Node
	root           *core.Root
	graph          int64
	index          int
	output         []Effect
	writeID        int64
	written        int
	readID         int64
	input          []byte
	inputEOF       bool
	graphInput     bool
	watcher        *core.Node
	watchRoot      *core.Root
	watchID        int64
	watchFailure   diagnostic.Diagnostic
	bodyProcessID  int64
	borrowedInput  bool
	borrowedReader *kashLoopState
	finishID       int64
	result         diagnostic.Diagnostic
	value          core.Value
	effects        []Effect
	paths          []string
	stream         core.Value
	consumer       int64
	finished       bool
	cancelled      bool
}

func freeFunctionStage(a mem.Allocator, value any) {
	stage := value.(*functionStage)
	stage.root, stage.node = nil, nil
	freeExecutionContext(a, &stage.context)
	stage.call.free(a)
	stage.result.Free(a)
	stage.watchFailure.Free(a)
	freeValueWithCallables(a, &stage.value)
	stage.stream.Free(a)
	FreeEffects(a, stage.output)
	FreeEffects(a, stage.effects)
	for _, path := range stage.paths {
		mem.FreeString(a, path)
	}
}

func (p *Program) startCallableStage(c *Context, graph int64, index int, call *preparedCallable, setup core.Value, borrowedInput, graphInput, graphOutput bool) *functionStage {
	stage := &functionStage{context: p.retainExecutionContext(c), call: call.clone(p.Alloc), graph: graph, index: index, borrowedInput: borrowedInput}
	stage.graphInput = graphInput
	stage.context.functionStage = stage
	stage.context.asyncHandle, stage.context.Deferred = nil, nil
	stage.context.commandOutput = nil
	stage.context.CloneCallableAST = true
	stage.context.InputClosed, stage.context.TerminalBackground = !borrowedInput && !graphInput, false
	stage.context.RecordBody = false
	stage.context.Terminal = TerminalAuthority{}
	mem.FreeString(p.Alloc, stage.context.Cwd)
	stage.context.Cwd = owned(p.Alloc, host.PayloadText(setup, host.FieldCwd))
	mem.FreeString(p.Alloc, stage.context.StageDirectory)
	stage.context.StageDirectory = ""
	stage.context.Unit = &ExecutionUnit{directory: stage.context.Cwd}
	stage.context.TimeoutMS = host.PayloadInt(setup, host.FieldTimeout)
	for _, entry := range stage.context.StageEnvironment {
		mem.FreeString(p.Alloc, entry)
	}
	stage.context.StageEnvironment = nil
	for _, entry := range host.PayloadList(setup, host.FieldEnvironment) {
		stage.context.StageEnvironment = append(stage.context.StageEnvironment, owned(p.Alloc, entry.Text))
	}
	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<callable-stage:"+strconv.FormatInt(graph, 10)+":"+strconv.Itoa(index)+">")
	stage.node = p.Engine.AddOwned(key, runCallableStage, stage, freeFunctionStage)
	key.Free(p.Alloc)
	if stage.node == nil {
		freeFunctionStage(p.Alloc, stage)
		return nil
	}
	stage.root = p.Engine.RequestRoot(stage.node)
	if graphOutput {
		key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<callable-watch:"+strconv.FormatInt(graph, 10)+":"+strconv.Itoa(index)+">")
		stage.watcher = p.Engine.Add(key, watchCallableOutput, stage)
		key.Free(p.Alloc)
		if stage.watcher != nil {
			stage.watchRoot = p.Engine.RequestRoot(stage.watcher)
		}
	}
	return stage
}

func (s *functionStage) release() {
	if s.root == nil {
		return
	}
	p := s.context.Program
	if s.watchRoot != nil {
		p.Engine.Release(s.watchRoot)
		s.watchRoot = nil
		p.ReleaseStatement(s.watcher)
	}
	p.Engine.Release(s.root)
	s.root = nil
	p.ReleaseStatement(s.node)
}

func (s *functionStage) enqueue(effect Effect) {
	if len(effect.Data) != 0 && !(s.cancelled && effect.Kind == EffectOut) {
		s.output = append(s.output, effect.clone())
	}
}

func (s *functionStage) cancel() {
	if s.cancelled {
		return
	}
	s.cancelled = true
	if s.borrowedReader != nil {
		closeKashReader(s.borrowedReader)
	}
	if s.node != nil && s.node.HostRequestID == s.bodyProcessID {
		s.context.Program.Engine.CancelRequest(s.node)
	}
}

func watchCallableOutput(engine *core.EngineContext, _ int64) core.ProducerResult {
	stage := engine.Context().(*functionStage)
	if stage.node != nil && stage.finishID == 0 {
		c := stage.context
		c.Run = engine.Allocator()
		c.Resume(engine)
		if stage.watchID == 0 {
			stage.watchID = stage.submit(&c, host.StageIO{Operation: host.StageWatch})
			if stage.watchID != 0 {
				return core.ProducerSubmitted
			}
			stage.watchFailure = failure(c.Run, "HOST_FAIL", stage.call.span, "cannot watch callable stage output").Diagnostic
			stage.cancel()
		} else if c.Completion().RequestID == stage.watchID {
			done := c.TakeCompletion()
			stage.watchFailure = done.Diagnostic
			if stage.watchFailure.Code != "" || done.Value.Kind == core.Nil {
				stage.cancel()
			}
			done.Value.Free(c.Run)
		}
	}
	engine.Publish(core.Value{})
	return core.ProducerCompleted
}

func (s *functionStage) submit(c *Context, io host.StageIO) int64 {
	io.Graph, io.Index = s.graph, s.index
	payload := host.StageIOPayload(c.Run, io)
	id := c.Submit(host.RequestProcessStageIO, payload)
	payload.Free(c.Run)
	return id
}

func (s *functionStage) readRecord(c *Context, delimiter byte, span source.Span) Result {
	if s.readID != 0 {
		if c.Completion().RequestID != s.readID {
			return Result{Waiting: true}
		}
		done := c.TakeCompletion()
		s.readID = 0
		defer done.Value.Free(c.Run)
		if done.Diagnostic.Code != "" {
			return Result{Diagnostic: done.Diagnostic}
		}
		switch done.Value.Kind {
		case core.Nil:
			s.inputEOF = true
		case core.Bytes:
			if len(done.Value.Bytes) == 0 || len(done.Value.Bytes) > host.StageChunkLimit {
				return failure(c.Run, "HOST_FAIL", span, "host returned an invalid stage input chunk")
			}
			s.input = append(s.input, done.Value.Bytes...)
		default:
			return failure(c.Run, "HOST_FAIL", span, "host returned non-byte stage input")
		}
	}
	end := bytes.IndexByte(s.input, delimiter) + 1
	if end == 0 && s.inputEOF {
		end = len(s.input)
	}
	if end > host.RecordLimit || (end == 0 && len(s.input) > host.RecordLimit) {
		s.input = nil
		return failure(c.Run, "KASH_RECORD_LIMIT", span, "record exceeds 64 KiB")
	}
	if end > 0 {
		record := core.NewString(c.Run, string(s.input[:end]))
		s.input = s.input[end:]
		return Result{Value: record}
	}
	if s.inputEOF {
		return Result{}
	}
	frame := ":lines"
	if delimiter == 0 {
		frame = ":nul"
	}
	s.readID = s.submit(c, host.StageIO{Operation: host.StageRead, Frame: frame})
	if s.readID == 0 {
		return failure(c.Run, "HOST_FAIL", span, "cannot read callable stage input")
	}
	return Result{Waiting: true}
}

func (s *functionStage) flush(c *Context) Result {
	if s.writeID != 0 {
		if c.Completion().RequestID != s.writeID {
			return Result{Waiting: true}
		}
		done := c.TakeCompletion()
		defer done.Value.Free(c.Run)
		s.writeID = 0
		if done.Diagnostic.Code != "" {
			return Result{Diagnostic: done.Diagnostic}
		}
		if done.Value.Kind == core.Nil {
			s.cancel()
		}
		if s.cancelled || s.written == len(s.output[0].Data) {
			s.output[0].Free(c.Run)
			s.output = s.output[1:]
			s.written = 0
		}
	}
	for len(s.output) != 0 && s.cancelled && s.output[0].Kind == EffectOut {
		s.output[0].Free(c.Run)
		s.output = s.output[1:]
	}
	if len(s.output) == 0 || c.Engine.Submitted() || c.Completion().RequestID != 0 {
		return Result{}
	}
	effect := &s.output[0]
	op := host.StageStdout
	if effect.Kind == EffectErr {
		op = host.StageStderr
	}
	end := min(len(effect.Data), s.written+host.StageChunkLimit)
	s.writeID = s.submit(c, host.StageIO{Operation: op, Bytes: effect.Data[s.written:end]})
	s.written = end
	if s.writeID == 0 {
		return failure(c.Run, "HOST_FAIL", s.call.span, "cannot send callable stage output")
	}
	return Result{Waiting: true}
}

func runCallableStage(engine *core.EngineContext, _ int64) core.ProducerResult {
	stage := engine.Context().(*functionStage)
	p := stage.context.Program
	c := stage.context
	c.Engine, c.Run, c.Requests = engine, engine.Allocator(), p.Requests
	c.Resume(engine)
	if stage.finishID != 0 {
		if c.Completion().RequestID != stage.finishID {
			return core.ProducerWaiting
		}
		done := c.TakeCompletion()
		done.Value.Free(c.Run)
		if stage.result.Code == "" {
			stage.result = done.Diagnostic
		} else {
			done.Diagnostic.Free(c.Run)
		}
		engine.Publish(core.Value{})
		return core.ProducerCompleted
	}
	if r := stage.flush(&c); r.Waiting {
		return core.ProducerWaiting
	} else if r.Diagnostic.Code != "" {
		stage.result, stage.finished = r.Diagnostic, true
	}
	if !stage.finished {
		r := stage.advance(&c)
		p.streamControlOutput(&c, 0)
		if r.Waiting || r.Yielded {
			waiting := r.Waiting
			r.Free(c.Run)
			FreeEffects(c.Run, c.Effects)
			for _, path := range c.WritePaths {
				mem.FreeString(c.Run, path)
			}
			if waiting {
				return core.ProducerWaiting
			}
			return core.ProducerActive
		}
		attachSource(&r, &c)
		stage.result, stage.finished = r.Diagnostic, true
		r.Diagnostic = diagnostic.Diagnostic{}
		stage.value = borrowValue(p.Alloc, r.Value)
		r.Free(c.Run)
		stage.effects, stage.paths = c.Effects, c.WritePaths
		if r := stage.flush(&c); r.Waiting {
			return core.ProducerWaiting
		} else if r.Diagnostic.Code != "" {
			stage.result.Free(c.Run)
			stage.result = r.Diagnostic
		}
	}
	end := host.StageIO{Operation: host.StageFinish, Cancelled: stage.cancelled && stage.watchFailure.Code == ""}
	if !end.Cancelled && stage.result.Code != "" {
		end.Status = 1
		if stage.result.Exit.Requested {
			end.Status = stage.result.Exit.Status
		}
		if stage.result.Cause.HasStatus {
			end.Status = stage.result.Cause.Status
		}
		if stage.result.Cause.HasSignal {
			end.Signal = stage.result.Cause.Signal
			end.Status = 128 + end.Signal
		}
	}
	if stage.watchFailure.Code != "" {
		end.Status, end.Signal = 1, 0
	}
	stage.finishID = stage.submit(&c, end)
	if stage.finishID == 0 {
		engine.Fail(failure(c.Run, "HOST_FAIL", stage.call.span, "cannot close callable stage").Diagnostic)
		return core.ProducerFailed
	}
	return core.ProducerSubmitted
}

func (s *functionStage) advance(c *Context) Result {
	p := c.Program
	if s.stream.Kind == core.Nil {
		args := make([]core.Value, len(s.call.args))
		for i, arg := range s.call.args {
			args[i] = borrowValue(c.Run, arg)
		}
		result := p.callValues(s.call.callee.Callable.(*Function), args, c, s.call.span)
		freeValues(c.Run, args)
		if result.Value.Kind != core.Stream || result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
		s.stream = result.Value.Clone(p.Alloc)
		result.Free(c.Run)
		p.NextScriptGroup++
		s.consumer = p.NextScriptGroup
	}
	g := streamGenerator(s.stream)
	if s.cancelled {
		return g.stop(c)
	}
	return g.pull(c, s.consumer)
}

func (s *captureState) joinCallableStages(c *Context) (core.Completion, bool) {
	if s.GraphTerminal == nil {
		if c.Completion().RequestID != s.ID {
			return core.Completion{}, false
		}
		terminal := c.TakeCompletion()
		s.GraphTerminal = &terminal
		if terminal.Diagnostic.Code != "" || host.EffectiveStatus(terminal.Value) != 0 {
			for _, stage := range s.CallableStages {
				if stage.finishID == 0 {
					stage.cancel()
				}
			}
		}
	}
	for _, stage := range s.CallableStages {
		if !c.Engine.TryDependency(stage.node.Key) {
			return core.Completion{}, false
		}
	}
	terminal := *s.GraphTerminal
	s.GraphTerminal = nil
	for _, stage := range s.CallableStages {
		for _, observation := range stage.node.Observations {
			c.Engine.ObserveAspect(observation.Key, observation.Aspect, observation.Signature)
		}
		// Security and phase diagnostics cannot become ordinary pipe exits.
		if terminal.Diagnostic.Code == "" && stage.watchFailure.Code != "" {
			terminal.Diagnostic = stage.watchFailure.Clone(c.Run)
			freeValueWithCallables(c.Run, &terminal.Value)
		}
		if d := stage.result; terminal.Diagnostic.Code == "" && d.Code != "" && !stage.cancelled &&
			(!functionOutcomeAllowed(d) || d.Code == "FEATURE_UNSUP") {
			terminal.Diagnostic = d.Clone(c.Run)
			if !d.Exit.Requested {
				freeValueWithCallables(c.Run, &terminal.Value)
			}
		}
		stages := host.PayloadStages(terminal.Value)
		if stage.index < len(stages) && stages[stage.index].Kind == core.Record {
			record := &stages[stage.index]
			status := "completed"
			problem := core.Value{}
			value := core.Value{}
			outcome := host.ProcessOutcome(host.PayloadInt(*record, "outcome"))
			if stage.cancelled || outcome == host.ProcessCancelled {
				status = "cancelled"
			} else if host.EffectiveStatus(*record) != 0 || outcome != host.ProcessExited {
				status = "failed"
				if stage.result.Code != "" && !stage.result.ExitOnly() {
					problem = outcomeErrorRecord(c.Run, stage.result)
				}
			} else if stage.result.Code == "" {
				value = borrowValue(c.Run, stage.value)
			}
			record.Record = append(record.Record,
				core.RecordField{Key: owned(c.Run, "state"), Value: core.NewSymbol(c.Run, status)},
				core.RecordField{Key: owned(c.Run, "error"), Value: problem},
				core.RecordField{Key: owned(c.Run, "value"), Value: value},
			)
		}
		for _, effect := range stage.effects {
			s.Effects = append(s.Effects, effect.clone())
			c.Effects = append(c.Effects, effect.clone())
		}
		for _, path := range stage.paths {
			s.WritePaths = append(s.WritePaths, owned(c.Program.Alloc, path))
			c.WritePaths = append(c.WritePaths, owned(c.Run, path))
		}
		c.Engine.ForgetDependency(stage.node.Key)
		stage.release()
	}
	s.CallableStages = nil
	return terminal, true
}
