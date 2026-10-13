package eval

// OperationCategory describes the operation itself, not its name or operands.
// Callback and template evaluation may propagate additional effects.
type OperationCategory int

const (
	UnspecifiedOperation OperationCategory = iota
	PureOperation
	ObservationOperation
	OutputOperation
	ProcessOperation
	ControlOperation
	VolatileOperation
)

type PhaseMask int

const (
	EvaluationAllowed PhaseMask = 1 << iota
	PlanningAllowed
	ResolutionAllowed
	RenderingAllowed
	AllPhases = EvaluationAllowed | PlanningAllowed | ResolutionAllowed | RenderingAllowed
	RuntimePhases = EvaluationAllowed | RenderingAllowed
)

// These are possible behaviors, not authority. The existing request, callback,
// service, dry-run and publication checks still apply at the effect boundary.
type OperationBehavior int

const (
	CallsCallable OperationBehavior = 1 << iota
	ReadsHost
	TracksDependency
	EmitsOutput
	PublishesFile
	StartsProcess
	RaisesFailure
	ReadsClock
)

// HasContract distinguishes explicitly described standard operations from
// legacy/custom registrations that retain their existing runtime checks.
func (o *Operation) HasContract() bool { return o.Category != UnspecifiedOperation }

// ValidContract rejects partial/invalid descriptions, without requiring custom
// embedders to adopt standard-library metadata in this migration step.
func (o *Operation) ValidContract() bool {
	if !o.HasContract() {
		return o.Phases == 0 && o.Behavior == 0 && len(o.ConditionalCapabilities) == 0
	}
	if o.Category < PureOperation || o.Category > VolatileOperation || o.Phases == 0 || o.Phases & ^PhaseMask(AllPhases) != 0 {
		return false
	}
	if o.Behavior & ^OperationBehavior(CallsCallable | ReadsHost | TracksDependency | EmitsOutput | PublishesFile | StartsProcess | RaisesFailure | ReadsClock) != 0 {
		return false
	}
	if (o.Category == OutputOperation || o.Category == ProcessOperation || o.Category == VolatileOperation) && o.Phases & (PlanningAllowed | ResolutionAllowed) != 0 {
		return false
	}
	switch o.Category {
	case PureOperation:
		if o.Behavior & ^OperationBehavior(CallsCallable) != 0 { return false }
	case ObservationOperation:
		if o.Behavior & TracksDependency == 0 || o.Behavior & ^OperationBehavior(ReadsHost | TracksDependency | CallsCallable) != 0 { return false }
	case OutputOperation:
		if o.Behavior & (EmitsOutput | PublishesFile) == 0 || o.Behavior & ^OperationBehavior(EmitsOutput | PublishesFile) != 0 { return false }
	case ProcessOperation:
		if o.Behavior & StartsProcess == 0 || o.Behavior & ^OperationBehavior(StartsProcess | TracksDependency) != 0 { return false }
	case ControlOperation:
		if o.Behavior != RaisesFailure { return false }
	case VolatileOperation:
		if o.Behavior != ReadsClock { return false }
	}
	for i := range o.Capabilities {
		if o.Capabilities[i] < Read || o.Capabilities[i] > Env { return false }
	}
	for i := range o.ConditionalCapabilities {
		if o.ConditionalCapabilities[i] < Read || o.ConditionalCapabilities[i] > Env { return false }
	}
	return true
}

// AllowsPhase is an upper bound: host requests and reached callbacks can impose
// narrower restrictions. In particular inline render/env snapshot evaluation
// does not gain permission to submit host work during pure planning.
func (o *Operation) AllowsPhase(phase Phase) bool {
	if phase < EvaluatePhase || phase > RenderingPhase { return false }
	return o.Phases & (1 << phase) != 0
}
