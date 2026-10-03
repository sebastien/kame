// Package program compiles Kame scripts and materializes their targets.
package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Freshness int

const (
	Fresh Freshness = iota
	Stale
	Unknown
)

type Plan struct {
	Target   string
	Key      core.ResourceKey
	Rule     *rule.Rule
	RuleSpan diagnostic.Span
	Body     []rule.RecipeLine
	Captures []template.CaptureValue
	Inputs   []string
	// StaticInputs are literal and template inputs as authored.
	StaticInputs []string
	// DynamicInputs are the values resolved from expression-form rule inputs.
	// They remain separate so inspection can distinguish authored edges from
	// resources discovered by evaluating definitions.
	DynamicInputs          []string
	ResourceInputs         []PlanInput
	ResolvedInputs         []string
	ResolvedResourceInputs []PlanInput
	Resolved               bool
	Outputs                []string
	Freshness              Freshness
}

type PlanInput struct {
	Display string
	Key     core.ResourceKey
}

type PlanResult struct {
	Plan       Plan
	Diagnostic diagnostic.Diagnostic
	Waiting    bool
}

func (p *Plan) Free(a mem.Allocator) {
	if p == nil {
		return
	}
	mem.FreeString(a, p.Target)
	if p.Key.Name != "" {
		p.Key.Free(a)
	}
	for i := range p.Captures {
		mem.FreeString(a, p.Captures[i].Name)
		mem.FreeString(a, p.Captures[i].Text)
	}
	for i := range p.Inputs {
		mem.FreeString(a, p.Inputs[i])
	}
	for i := range p.StaticInputs {
		mem.FreeString(a, p.StaticInputs[i])
	}
	for i := range p.DynamicInputs {
		mem.FreeString(a, p.DynamicInputs[i])
	}
	for i := range p.ResourceInputs {
		mem.FreeString(a, p.ResourceInputs[i].Display)
		p.ResourceInputs[i].Key.Free(a)
	}
	for i := range p.ResolvedInputs {
		mem.FreeString(a, p.ResolvedInputs[i])
	}
	for i := range p.ResolvedResourceInputs {
		mem.FreeString(a, p.ResolvedResourceInputs[i].Display)
		p.ResolvedResourceInputs[i].Key.Free(a)
	}
	for i := range p.Outputs {
		mem.FreeString(a, p.Outputs[i])
	}
	slices.Free(a, p.Captures)
	slices.Free(a, p.Inputs)
	slices.Free(a, p.StaticInputs)
	slices.Free(a, p.DynamicInputs)
	slices.Free(a, p.ResourceInputs)
	slices.Free(a, p.ResolvedInputs)
	slices.Free(a, p.ResolvedResourceInputs)
	slices.Free(a, p.Outputs)
	*p = Plan{}
}

type EventKind int

const (
	TargetStarted EventKind = iota
	ProcessStarted
	ProcessExited
	Stdout
	Stderr
	DependencyDiscovered
	Effect
	TargetValue
	TargetCompleted
	TargetFailed
	TargetCancelled
	CacheWarning
)

type Event struct {
	Kind          EventKind
	Target        string
	Key           core.ResourceKey
	NodeID        int64
	Generation    int64
	Attempt       int64
	RequestID     int64
	DependencyID  int64
	DependencyKey core.ResourceKey
	Effect        string
	Span          diagnostic.Span
	Data          []byte
	Value         core.Value
	Diagnostic    diagnostic.Diagnostic
	Cached        bool
	Truncated     bool
}

type EventResult struct {
	Event Event
	OK    bool
}

type Handle struct {
	Program    *Program
	Root       *core.Root
	Node       *core.Node
	Target     string
	Definition bool
}

type HandleResult struct {
	Result Result
	Done   bool
}
type HandleStart struct {
	Handle     *Handle
	Diagnostic diagnostic.Diagnostic
}

func (e *Event) Free(a mem.Allocator) {
	mem.FreeString(a, e.Target)
	if e.Key.Name != "" {
		e.Key.Free(a)
	}
	if e.DependencyKey.Name != "" {
		e.DependencyKey.Free(a)
	}
	slices.Free(a, e.Data)
	e.Value.Free(a)
	e.Diagnostic.Free(a)
	*e = Event{}
}

func (h *Handle) Free() {
	if h == nil || h.Program == nil {
		return
	}
	p := h.Program
	if h.Root != nil {
		p.Engine.Release(h.Root)
		// A caller may drop its final handle without issuing Cancel. Forward the
		// resulting engine cancellation immediately so process groups do not wait
		// for another runtime tick that may never occur.
		p.drainCancellations()
	}
	mem.FreeString(p.Alloc, h.Target)
	*h = Handle{}
	mem.Free(p.Alloc, h)
}

type Options struct {
	// Host executes recipe scripts and services the runtime's filesystem and
	// clock. Compile transfers ownership to the Program, which releases it
	// during Program.Free.
	Host        host.ProgramHost
	Directory   string
	Shell       []string
	Environment []string
	DryRun      bool
	// Force bypasses file freshness checks and cached-task lookup for this run.
	Force            bool
	RetainBytes      int
	CaptureLimit     int
	CacheRetainBytes int
	CacheDisabled    bool
	// CacheManifestMax is the encoded fingerprint cap in bytes. Zero selects the
	// 16 MiB spec default. Tests inject a smaller cap to pin overflow boundaries.
	CacheManifestMax int
	TimeoutMS        int64
	RetryCount       int
	Verbose          bool
	Jobs             int
	Grants           []eval.Grant
	// ForwardRequests routes evaluator host requests to an embedding host
	// instead of servicing them locally. The wasm runtime sets it so the
	// JavaScript host can service filesystem, environment, and process work
	// asynchronously. Native callers leave it false.
	ForwardRequests bool
	// ResolveTool returns an allocator-owned executable path when a reached
	// recipe references a tool. A nil callback uses paths supplied by the host.
	ResolveTool func(mem.Allocator, string, string, []string) string
}

// Tool records a globally declared command and its resolved executable path.
type Tool struct {
	Name string
	Path string
}

type Program struct {
	Alloc       mem.Allocator
	Engine      *core.Engine
	Eval        *eval.Program
	Parsed      *script.Script
	ParsedOwned bool
	Host        host.ProgramHost
	Options     Options
	Rules       []registeredRule
	Tools       []Tool
	Instances   []instance
	Events      []Event
	nextRequest int64
	Pending     []pendingRequest
	epoch       int64
	// Forwarding mirrors Options.ForwardRequests; Outbound holds requests an
	// embedding host must service and complete.
	InspectionWaiting bool
	Forwarding bool
	// SessionPolicy makes legacy recipe launches inherit runner capability grants.
	SessionPolicy bool
	Outbound   []host.Request
}

type pendingRequest struct {
	Capture    bool
	Stream     bool
	ID         int64
	NodeID     int64
	Generation int64
	Attempt    int64
	Retries    int
}

type registeredRule struct{ Rule *rule.Rule }
type instance struct {
	Rule     *rule.Rule
	Captures []template.CaptureValue
	Node     *core.Node
	Plan     Plan
	// Inspection instances resolve inputs for span --expand only. They must not
	// satisfy normal target lookup or participate in build execution.
	Inspection           bool
	inspectionRoot       *core.Root
	ForwardEffects       *forwardEffectsState
 VerifyOutputs bool
 VerifyPending bool
 VerifyIndex int
	Script               string
	LineSpans            []diagnostic.Span
	Operations           []string
	CacheFingerprint     [32]byte
	CacheManifest        []byte
	CacheStdout          []byte
	CacheStderr          []byte
	CacheStdoutTruncated bool
	CacheStderrTruncated bool
	CacheReady           bool
	cacheStartedAt       int64
	cachePending         bool
	retryCount           int
	started              bool
	startedGeneration    int64
	terminalEmitted      bool
	terminalGeneration   int64
	valueRevision        int64
	satisfiedEpoch       int64
	runEpoch             int64
}
type selection struct {
	Rule      *rule.Rule
	Captures  []template.CaptureValue
	Ambiguous bool
}
type planResolverState struct {
	Program   *Program
	Resolving []string
}

type CompileResult struct {
	Program     *Program
	Diagnostics []diagnostic.Diagnostic
}
type CompileSource struct {
	Name string
	Text string
	// Offset is the byte position of Text in its original source file.
	Offset int
}

func (r *CompileResult) Free(a mem.Allocator) {
	for i := range r.Diagnostics {
		r.Diagnostics[i].Free(a)
	}
	slices.Free(a, r.Diagnostics)
	*r = CompileResult{}
}
