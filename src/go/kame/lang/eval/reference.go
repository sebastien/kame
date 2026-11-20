package eval

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"

	"unicode/utf8"
)

func (p *Program) reference(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Reference) == 0 {
		return failure(context.Run, "REF_MISSING", expression.Span, "empty reference")
	}
	var result Result
	firstPart := 1
	if expression.Kind == expr.EnvironmentReference {
		result = p.readEnvironment(scope, expression.Items, context, expression.Span)
	} else if len(expression.Items) != 0 {
		result = p.evaluate(context.Engine, scope, expression.Items[0], context)
		firstPart = 0
	} else {
		result = name(scope, expression.Reference[0].Text, expression.LetBinding, expression.Reference[0].Span, context)
	}
	if result.Waiting || result.Diagnostic.Code != "" {
		return result
	}
	for i := firstPart; i < len(expression.Reference); i++ {
		part := expression.Reference[i]
		next := p.referencePart(result.Value, part, context)
		// Selection borrows independent wrappers, so every source leaf can go.
		freeCallables(context.Run, &result.Value)
		result.Value.Free(context.Run)
		result = next
		if result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
	}
	return result
}

func (p *Program) readEnvironment(scope *Scope, arguments []*expr.Expr, context *Context, span source.Span) Result {
	operation := p.Registry.lookup("env")
	if operation == nil {
		return failure(context.Run, "OP_UNKNOWN", span, "environment operation unavailable")
	}
	return p.operation(scope, operation, arguments, context, span)
}

func bareEnvironmentName(name string) bool {
	if len(name) == 0 || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (p *Program) referencePart(value core.Value, part expr.ReferencePart, context *Context) Result {
	_ = p
	if value.Kind == core.Module {
		if part.Kind != expr.ReferenceName {
			return failure(context.Run, "EXPR_INVALID", part.Span, "module supports only named field selection")
		}
		selected := context.ModuleField(value, part.Text, part.Span)
		if selected.Found {
			return selected.Result
		}
		return failure(context.Run, "REF_MISSING", part.Span, "module field not found")
	}
	if part.Kind == expr.ReferenceName {
		if value.Kind != core.Record {
			return failure(context.Run, "REF_MISSING", part.Span, "record field not found")
		}
		for i := range value.Record {
			if value.Record[i].Key == part.Text {
				return Result{Value: borrowValue(context.Run, value.Record[i].Value)}
			}
		}
		return failure(context.Run, "REF_MISSING", part.Span, "record field not found")
	}
	if part.Kind == expr.ReferenceIndex {
		index, ok := parseIndex(part.Text)
		if !ok {
			return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "invalid index")
		}
		if value.Kind == core.List {
			index = normalizedIndex(index, len(value.List))
			if index < 0 || index >= len(value.List) {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "index out of range")
			}
			return Result{Value: borrowValue(context.Run, value.List[index])}
		}
		if value.Kind == core.String {
			length := utf8.RuneCountInString(value.Text)
			index = normalizedIndex(index, length)
			if index < 0 || index >= length {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "index out of range")
			}
			start := runeOffset(value.Text, index)
			end := runeOffset(value.Text, index+1)
			if start < 0 || end < 0 {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "index out of range")
			}
			return Result{Value: core.NewString(context.Run, value.Text[start:end])}
		}
		return failure(context.Run, "REF_MISSING", part.Span, "index needs a list or string")
	}
	return failure(context.Run, "REF_MISSING", part.Span, "invalid reference")
}

func parseIndex(text string) (int, bool) {
	if len(text) == 0 {
		return 0, false
	}
	negative := false
	if text[0] == '-' {
		negative = true
		text = text[1:]
	}
	if len(text) == 0 {
		return 0, false
	}
	index := uint(0)
	limit := ^uint(0) >> 1
	if negative {
		limit++
	}
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		digit := uint(text[i] - '0')
		if index > (limit-digit)/10 {
			return 0, false
		}
		index = index*10 + digit
	}
	if negative {
		if index == 0 {
			return 0, true
		}
		return -int(index-1) - 1, true
	}
	return int(index), true
}

func normalizedIndex(index int, length int) int {
	if index < 0 {
		return length + index
	}
	return index
}
func runeOffset(text string, index int) int {
	if index < 0 {
		return -1
	}
	offset := 0
	for current := 0; current < index; current++ {
		if offset == len(text) {
			return -1
		}
		_, width := utf8.DecodeRuneInString(text[offset:])
		offset += width
	}
	return offset
}

// contextList provides the list bound by the active rule or invocation.
func (c *Context) contextList(name string, span source.Span) Result {
	var values []core.Value
	var available bool
	if name == "args" {
		values, available = c.Args, c.HasArgs
	} else {
		if name == "inputs" {
			values = c.Inputs
		} else {
			values = c.Outputs
		}
		available = values != nil
		if len(c.RuleFrames) != 0 {
			frame := c.RuleFrames[len(c.RuleFrames)-1]
			if name == "inputs" {
				values = frame.Inputs
			} else {
				values = frame.Outputs
			}
			available = true
		}
	}
	if !available {
		result := failure(c.Run, "SEL_NO_CONTEXT", span, name+" requires an active rule or invocation context")
		result.Diagnostic.Tips = append(result.Diagnostic.Tips, owned(c.Run, "Use "+name+" where it is bound, or pass the list explicitly."))
		return result
	}
	return Result{Value: borrowValue(c.Run, core.Value{Kind: core.List, List: values})}
}

func (c *Context) changedInputs(span source.Span) Result {
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		return failure(c.Run, "PHASE_INVALID", span, "changed-inputs is available only while rendering a file recipe")
	}
	if len(c.RuleFrames) == 0 || !c.RuleFrames[len(c.RuleFrames)-1].FileRule {
		return failure(c.Run, "SEL_NO_CONTEXT", span, "changed-inputs requires a file rule")
	}
	return Result{Value: core.NewList(c.Run, c.RuleFrames[len(c.RuleFrames)-1].NewerInputs)}
}
