// Package program compiles LittleMake scripts and materializes their targets.
package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
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
	Target                 string
	Key                    core.ResourceKey
	Rule                   *rule.Rule
	RuleSpan               diagnostic.Span
	Body                   []rule.RecipeLine
	Captures               []template.CaptureValue
	Inputs                 []string
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
}

func (p *Plan) Free(a mem.Allocator) {
	if p == nil {
		return
	}
	if p.Target != "" {
		mem.FreeString(a, p.Target)
	}
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
	for i := range p.ResourceInputs {
		if p.ResourceInputs[i].Display != "" {
			mem.FreeString(a, p.ResourceInputs[i].Display)
		}
		p.ResourceInputs[i].Key.Free(a)
	}
	for i := range p.ResolvedInputs {
		mem.FreeString(a, p.ResolvedInputs[i])
	}
	for i := range p.ResolvedResourceInputs {
		if p.ResolvedResourceInputs[i].Display != "" {
			mem.FreeString(a, p.ResolvedResourceInputs[i].Display)
		}
		p.ResolvedResourceInputs[i].Key.Free(a)
	}
	for i := range p.Outputs {
		mem.FreeString(a, p.Outputs[i])
	}
	if len(p.Captures) != 0 {
		slices.Free(a, p.Captures)
	}
	if len(p.Inputs) != 0 {
		slices.Free(a, p.Inputs)
	}
	if len(p.ResourceInputs) != 0 {
		slices.Free(a, p.ResourceInputs)
	}
	if len(p.ResolvedInputs) != 0 {
		slices.Free(a, p.ResolvedInputs)
	}
	if len(p.ResolvedResourceInputs) != 0 {
		slices.Free(a, p.ResolvedResourceInputs)
	}
	if len(p.Outputs) != 0 {
		slices.Free(a, p.Outputs)
	}
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
	if e.Target != "" {
		mem.FreeString(a, e.Target)
	}
	if e.Key.Name != "" {
		e.Key.Free(a)
	}
	if e.DependencyKey.Name != "" {
		e.DependencyKey.Free(a)
	}
	if len(e.Data) != 0 {
		slices.Free(a, e.Data)
	}
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
	}
	if h.Target != "" {
		mem.FreeString(p.Alloc, h.Target)
	}
	*h = Handle{}
	mem.Free(p.Alloc, h)
}

type Options struct {
	Directory   string
	Shell       []string
	Environment []string
	DryRun      bool
	// Force bypasses file freshness checks and cached-task lookup for this run.
	Force            bool
	RetainBytes      int
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
}

type Program struct {
	Alloc       mem.Allocator
	Engine      *core.Engine
	Eval        *eval.Program
	Parsed      *script.Script
	ParsedOwned bool
	Host        *posix.Host
	Options     Options
	Rules       []registeredRule
	Instances   []instance
	Events      []Event
	nextRequest int64
	Pending     []pendingRequest
	epoch       int64
}

type pendingRequest struct {
	ID         int64
	NodeID     int64
	Generation int64
	Attempt    int64
	Retries    int
}

type registeredRule struct{ Rule *rule.Rule }
type instance struct {
	Rule                 *rule.Rule
	Captures             []template.CaptureValue
	Node                 *core.Node
	Plan                 Plan
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
}

func (r *CompileResult) Free(a mem.Allocator) {
	for i := range r.Diagnostics {
		r.Diagnostics[i].Free(a)
	}
	slices.Free(a, r.Diagnostics)
	*r = CompileResult{}
}
