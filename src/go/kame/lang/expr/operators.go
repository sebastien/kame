package expr

// streamOperatorArgument classifies what a `|(name)` operator admits between its
// name and the closing parenthesis.
type streamOperatorArgument int

const (
	argumentNone        streamOperatorArgument = iota // no argument; policies only
	argumentLineAdapter                               // optional `:lines` adapter
	argumentPredicate                                 // required predicate plus a selected branch
	argumentPath                                      // required destination path
)

// streamOperator is the surface grammar of one Kash stream operator. The parser
// and formatter both read this table, so a new operator changes one row instead
// of several name lists. Host wire kinds are decided by the evaluator, not here.
type streamOperator struct {
	Name     string
	Argument streamOperatorArgument
	Policy   bool // accepts :buffer/:order/:strategy options
	Implicit bool // a following stage may start without an explicit `|`
	FanOut   bool // its following stage is a destination, not a pipe continuation
}

var streamOperators = []streamOperator{
	{Name: "merge", Policy: true, Implicit: true, FanOut: true},
	{Name: "broadcast", Policy: true, Implicit: true, FanOut: true},
	{Name: "balance", Policy: true, Implicit: true, FanOut: true},
	{Name: "frame", Argument: argumentLineAdapter},
	{Name: "encode", Argument: argumentLineAdapter},
	{Name: "filter", Argument: argumentPredicate},
	{Name: "each", Implicit: true},
	{Name: "tee", Argument: argumentPath},
}

func streamOperatorFor(name string) *streamOperator {
	for i := range streamOperators {
		if streamOperators[i].Name == name {
			return &streamOperators[i]
		}
	}
	return nil
}

// StreamTakesArgument reports whether the operator carries a leading argument
// that its lowering must preserve, such as `:lines`, a predicate, or a path.
func StreamTakesArgument(name string) bool {
	operator := streamOperatorFor(name)
	return operator != nil && operator.Argument != argumentNone
}

func streamOperatorImplicitStage(name string) bool {
	operator := streamOperatorFor(name)
	return operator != nil && operator.Implicit
}

func streamOperatorFanOut(name string) bool {
	operator := streamOperatorFor(name)
	return operator != nil && operator.FanOut
}
