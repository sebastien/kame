package host

// Framing says how an operator changes the framing of its output.
type Framing int

const (
	FramesKeep Framing = iota
	FramesFrame
	FramesUnframe
)

// InputFraming says what an operator requires of its input framing.
type InputFraming int

const (
	InputsAny InputFraming = iota
	InputsUnframed
	InputsFramed
	InputsRequireFrames
)

// OperatorKind is the host-side contract of one native stream operator. A
// process request carries these names in its unordered "kinds" list, and the C
// and JavaScript hosts receive the same names and wire codes, so Kind and Code
// are wire contract rather than an internal detail. This table is the single
// source of truth for the Go side's classification, cardinality bounds,
// framing and accepted policies; a host that validates independently must keep
// the same row semantics.
//
// Kind "" is a plain executable stage and Kind "each" is a worker template:
// both launch a command through Argv. Every other row is implemented by the
// host itself and must not carry argv.
type OperatorKind struct {
	Kind        string
	Code        int    // wire code shared with the native and JavaScript hosts
	Argv        bool   // launches a command, so argv is mandatory
	MinIn       int    // argument and stdin edges
	MaxIn       int    // -1 is unbounded
	MinOut      int    // outgoing edges
	MaxOut      int    // -1 is unbounded
	Ordered     bool   // accepts :order
	Queue       bool   // accepts :buffer
	Lossy       bool   // accepts :overflow
	OutputFile  bool   // may own a side output file beside its presentation edge
	Frames      Framing      // framing emitted to the next connection
	InputFrames InputFraming // framing required from the preceding connection
}

// operatorKinds lists every wire kind once. "copy" is the generic 1:1
// pass-through: it forwards the stream unchanged and, unlike the other
// pass-throughs, may own a side output file next to its presentation edge.
// Kash `|(tee PATH)` reuses it for exactly that shape.
var operatorKinds = []OperatorKind{
	{Kind: "", Code: 0, Argv: true, MaxIn: 1, MaxOut: 1, Frames: FramesKeep, InputFrames: InputsUnframed},
	{Kind: "merge", Code: 1, MinIn: 2, MaxIn: -1, MinOut: 1, MaxOut: 1, Ordered: true, Queue: true, Frames: FramesKeep, InputFrames: InputsAny},
	{Kind: "broadcast", Code: 2, MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: -1, Queue: true, Lossy: true, Frames: FramesKeep, InputFrames: InputsAny},
	{Kind: "frame", Code: 3, MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 1, Frames: FramesFrame, InputFrames: InputsUnframed},
	{Kind: "encode", Code: 4, MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 1, Frames: FramesUnframe, InputFrames: InputsFramed},
	{Kind: "filter", Code: 5, MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 2, Frames: FramesFrame, InputFrames: InputsFramed},
	{Kind: "copy", Code: 6, MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 1, OutputFile: true, Frames: FramesKeep, InputFrames: InputsAny},
	{Kind: "balance", Code: 7, MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: -1, Queue: true, Frames: FramesKeep, InputFrames: InputsRequireFrames},
	{Kind: "each", Code: 8, Argv: true, MinIn: 1, MaxIn: 1, MaxOut: 1, Frames: FramesUnframe, InputFrames: InputsRequireFrames},
	{Kind: "out", Code: 9, MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0, Frames: FramesKeep, InputFrames: InputsAny},
	{Kind: "err", Code: 10, MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0, Frames: FramesKeep, InputFrames: InputsAny},
}

// LookupOperatorKind returns the contract for a wire kind, or nil for an
// unknown operator, which hosts reject rather than guess.
func LookupOperatorKind(kind string) *OperatorKind {
	for i := range operatorKinds {
		if operatorKinds[i].Kind == kind {
			return &operatorKinds[i]
		}
	}
	return nil
}
