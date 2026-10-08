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
	Target                string
	Key                   core.ResourceKey
	Rule                  *rule.Rule
	Generator             string
	GeneratorDependencies []string
	RuleSpan              diagnostic.Span
	Body                  []rule.RecipeLine
	Captures              []template.CaptureValue
	Arguments             []ArgumentValue
	Configuration         []string
	Tools                 []Tool
	Inputs                []string
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
	OrderOnly   bool
	SequenceEnd bool
	Display     string
	Key         core.ResourceKey
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
	mem.FreeString(a, p.Generator)
	freeStrings(a, p.GeneratorDependencies)
	if p.Key.Name != "" {
		p.Key.Free(a)
	}
	for i := range p.Captures {
		mem.FreeString(a, p.Captures[i].Name)
		mem.FreeString(a, p.Captures[i].Text)
	}
	freeArguments(a, p.Arguments)
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
	freeTools(a, p.Tools)
	freeStrings(a, p.Configuration)
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
	ServiceState
	TargetReason
)

type Event struct {
	Decision string
	Reason string
	Message string
	Aspect string
	Kind             EventKind
	Target           string
	Key              core.ResourceKey
	NodeID           int64
	Generation       int64
	Attempt          int64
	RequestID        int64
	DependencyID     int64
	DependencyKey    core.ResourceKey
	Effect           string
	State            string
	Program          string
	Argv             []string
	RuntimeMS        int64
	HasRuntime       bool
	// Native presentation timing; not part of the structured event schema.
	MonotonicNS      int64
	HasMonotonic     bool
	DisplayTruncated bool
	Span             diagnostic.Span
	Data             []byte
	Value            core.Value
	Diagnostic       diagnostic.Diagnostic
	Cached           bool
	Truncated        bool
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
	valueRevision int64
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
	mem.FreeString(a, e.Decision)
	mem.FreeString(a, e.Reason)
	mem.FreeString(a, e.Message)
	mem.FreeString(a, e.Aspect)
	mem.FreeString(a, e.Target)
	if e.Key.Name != "" {
		e.Key.Free(a)
	}
	if e.DependencyKey.Name != "" {
		e.DependencyKey.Free(a)
	}
	mem.FreeString(a, e.Program)
	freeStrings(a, e.Argv)
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
	Host            host.ProgramHost
	Directory       string
	Shell           []string
	Environment     []string
	Defines         []string
	Parameters      []string
	ToolOverrides   []string
	RemoteExecutors []host.ExecutorDescriptor
	DryRun          bool
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
	Name        string
	Path        string
	Resolved    bool
	Declarative bool
}

type Program struct {
	Alloc             mem.Allocator
	Engine            *core.Engine
	Eval              *eval.Program
	Parsed            *script.Script
	ParsedOwned       bool
	Host              host.ProgramHost
	Options           Options
	Rules             []registeredRule
	GeneratedRules    []*rule.Rule
	ResolvedRules     []*rule.Rule
	GeneratedRuleMeta []generatedRuleMeta
	Tools             []Tool
	Configuration     []string
	ParameterDefinitions []string
	SourceSignature   core.Signature
	Instances         []instance
	Events            []Event
	// Optional observation around actual scheduler dispatch, never a work request.
	ObserveWork       func(*core.Node, string, bool)
	nextRequest       int64
	Pending           []pendingRequest
	epoch             int64
	// Forwarding mirrors Options.ForwardRequests; Outbound holds requests an
	// embedding host must service and complete.
	InspectionWaiting bool
	Forwarding        bool
	// SessionPolicy makes legacy recipe launches inherit runner capability grants.
	SessionPolicy bool
	Outbound      []host.Request
}

type pendingRequest struct {
	Capture          bool
	Stream           bool
	Program          string
	Argv             []string
	DisplayTruncated bool
	Started          bool
	StartedNS        int64
	ID               int64
	NodeID           int64
	Generation       int64
	Attempt          int64
	Retries          int
}

// ServiceConfig owns the validated, bounded lifecycle policy for one service
// rule. Empty probe argv means that probe is disabled.
type ServiceConfig struct {
	ReadyArgv       []string
	ReadyInterval   int64
	ReadyTimeout    int64
	HealthArgv      []string
	HealthInterval  int64
	HealthFailures  int64
	RestartAttempts int64
	RestartBackoff  int64
	StopGrace       int64
	LogBytes        int64
}

func (c *ServiceConfig) Free(a mem.Allocator) {
	freeStrings(a, c.ReadyArgv)
	freeStrings(a, c.HealthArgv)
	*c = ServiceConfig{}
}

type registeredRule struct{ Rule *rule.Rule }
type generatedRuleMeta struct {
	Rule         *rule.Rule
	Name         string
	Dependencies []string
}
type instance struct {
	ReasonIdentities []reasonIdentity
	Service                ServiceConfig
	ServiceState           string
	ServiceProcessID       int64
	ServiceReady           bool
	ServiceReadyDeadline   int64
	ServiceNextProbe       int64
	ServiceProbeID         int64
	ServiceProbeHealth     bool
	ServiceClockID         int64
	ServiceTimerID         int64
	ServiceNextHealth      int64
	ServiceHealthFailures  int64
	ServiceRestartCount    int64
	ServiceRestartPending  bool
	ServiceRestartDeadline int64
	ServiceRestartTimerID  int64
	Shell                  []string
	Kash                   bool
	Executor               string
	ExecutorVersion        string
	ExecutionInputs        []host.ExecutionArtifact
	ExecutionOutputs       []string
	ExecutionKey           [32]byte
	MetadataEnvironment    []string
	SettingsDependencies   []string
	SettingsDiagnostic     diagnostic.Diagnostic
	KashRunning            bool
	KashContext            *eval.Context
	KashPrepared           bool
	KashPreparing          bool
	// Environment owns the resolved child-process assignments.
	Environment         []string
	EnvironmentClaimed  bool
	ScopedEnvironment   bool
	EnvironmentConflict bool
	NewerInputs         *newerInputState
	FileContext         *fileContextState
	FileContextReady    bool
	PreflightChecked    bool
	FileContextKey      [32]byte
	AcceptedRecord      core.SignatureRecord
	Rule                *rule.Rule
	Captures            []template.CaptureValue
	Node                *core.Node
	Plan                Plan
	// Inspection instances resolve inputs for span --expand only. They must not
	// satisfy normal target lookup or participate in build execution.
	Inspection           bool
	inspectionRoot       *core.Root
	ForwardEffects       *forwardEffectsState
	VerifyOutputs        bool
	VerifyPending        bool
	VerifyIndex          int
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
	cacheWaitingLock     bool
	cacheLockHeld        bool
	cacheLockStripe      int
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
	Rule       *rule.Rule
	Captures   []template.CaptureValue
	Arguments  []ArgumentValue
	Target     string
	Diagnostic diagnostic.Diagnostic
	Ambiguous  bool
}

// ArgumentValue is a bound named target argument. It remains separate from
// file-pattern captures throughout planning and instance identity.
type ArgumentValue struct {
	Name  string
	Value string
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
