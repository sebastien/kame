package eval

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/unicode/utf8"
)

// Stream operator statuses are reserved for native nodes, never interpreted for
// executable stages. Both hosts report these through ordinary per-node results.
const (
	StreamEncoding = 120
	StreamRecordLimit = 121
	StreamPredicateEffect = 122
	StreamPredicateBoolean = 123
	StreamPredicateFailure = 124
)

// EvaluateStreamPredicate evaluates an authored AST, not source supplied by a
// stream. The caller owns scope; each record gets an isolated item binding.
func (p *Program) EvaluateStreamPredicate(expression *expr.Expr, scope *Scope, raw []byte) int {
	if !utf8.Valid(raw) { return -StreamEncoding }
	end := len(raw)
	if end != 0 && raw[end-1] == '\n' { end--; if end != 0 && raw[end-1] == '\r' { end-- } }
	child := newScope(p.Alloc, scope)
	defer child.Free()
	item := core.NewString(p.Alloc, string(raw[:end]))
	child.setValue("item", item)
	item.Free(p.Alloc)
	c := Context{Program: p, Run: p.Alloc, Scope: child, PurePredicate: true, DisableMemo: true, FinalizationForbidden: true}
	r := p.evaluate(nil, child, expression, &c)
	if r.Diagnostic.Code == "" && !r.Waiting && r.Value.Kind == core.Callable {
		argument := core.NewString(p.Alloc, string(raw[:end]))
		called := p.callValues(r.Value.Callable.(*Function), []core.Value{argument}, &c, expression.Span)
		argument.Free(p.Alloc)
		r.Free(p.Alloc)
		r = called
	}
	defer r.Free(p.Alloc)
	if c.phaseInvalid || r.Diagnostic.Code == "KASH_PREDICATE_EFFECT" { return -StreamPredicateEffect }
	if r.Waiting || r.Stream != nil || r.Diagnostic.Code != "" { return -StreamPredicateFailure }
	if r.Value.Kind != core.Bool { return -StreamPredicateBoolean }
	if r.Value.Bool { return 1 }
	return 0
}

// StreamItem is synchronous and host-free. Native workers call it against
// their fork snapshot; the Node host calls the same evaluator through WASM.
// Negative node IDs validate a framer's UTF-8 without executing a predicate.
func (p *Program) StreamItem(requestID int64, node int, raw []byte) int {
	if !utf8.Valid(raw) { return -StreamEncoding }
	if node < 0 { return 1 }
	for i := range p.StreamStates {
		state := p.StreamStates[i]
		if state.ID != requestID || state.Done { continue }
		if node >= len(state.Predicates) || state.Predicates[node] == nil { return -StreamPredicateFailure }
		return p.EvaluateStreamPredicate(state.Predicates[node], state.PredicateScope, raw)
	}
	return -StreamPredicateFailure
}

func StreamItemCallback(state any, requestID int64, node int, raw []byte) int {
	return state.(*Program).StreamItem(requestID, node, raw)
}

func streamFailure(c *Context, status int, span source.Span) Result {
	switch status {
	case StreamEncoding: return failure(c.Run, "KASH_FRAME_ENCODING", span, "line frame is not valid UTF-8")
	case StreamRecordLimit: return failure(c.Run, "KASH_RECORD_LIMIT", span, "line frame exceeds the 64 KiB record limit")
	case StreamPredicateEffect: return failure(c.Run, "KASH_PREDICATE_EFFECT", span, "stream predicate attempted an effect or suspension")
	case StreamPredicateBoolean: return failure(c.Run, "KASH_PREDICATE_BOOLEAN", span, "stream predicate must return a Boolean")
	}
	return failure(c.Run, "KASH_PREDICATE_FAILED", span, "stream predicate evaluation failed")
}
