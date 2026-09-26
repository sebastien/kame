// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/lang/expr"
	"littlemake/lang/source"
	"solod.dev/so/slices"
	"solod.dev/so/unicode/utf8"
)

func (p *Program) reference(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Reference) == 0 {
		return failure(context.Run, "REF_MISSING", expression.Span, "empty reference")
	}
	result := name(scope, expression.Reference[0].Text, expression.Reference[0].Span, context)
	if result.Waiting || result.Diagnostic.Code != "" {
		return result
	}
	for i := 1; i < len(expression.Reference); i++ {
		part := expression.Reference[i]
		next := p.referencePart(result.Value, part, context)
		result.Value.Free(context.Run)
		result = next
		if result.Diagnostic.Code != "" {
			return result
		}
	}
	return result
}

func (p *Program) referencePart(value core.Value, part expr.ReferencePart, context *Context) Result {
	_ = p
	if part.Kind == expr.ReferenceName {
		if value.Kind != core.Record {
			return failure(context.Run, "REF_MISSING", part.Span, "record field not found")
		}
		for i := range value.Record {
			if value.Record[i].Key == part.Text {
				return Result{Value: value.Record[i].Value.Clone(context.Run)}
			}
		}
		return failure(context.Run, "REF_MISSING", part.Span, "record field not found")
	}
	if part.Kind == expr.ReferenceSelection {
		if value.Kind != core.Record {
			return failure(context.Run, "REF_MISSING", part.Span, "selection needs a record")
		}
		var fields []core.RecordField
		start := 0
		for i := 0; i <= len(part.Text); i++ {
			if i != len(part.Text) && part.Text[i] != ',' {
				continue
			}
			key := part.Text[start:i]
			found := false
			for j := range value.Record {
				if value.Record[j].Key == key {
					fields = slices.Append(context.Run, fields, core.RecordField{Key: key, Value: value.Record[j].Value.Clone(context.Run)})
					found = true
					break
				}
			}
			if !found {
				// Shallow: fields share callable storage with the source record,
				// which still owns those scopes.
				freeRecord(context.Run, fields)
				return failure(context.Run, "REF_MISSING", part.Span, "record field not found")
			}
			start = i + 1
		}
		result := Result{Value: core.NewRecord(context.Run, fields)}
		// Shallow: NewRecord shallow-cloned callables shared with the source;
		// the source and the result each own one logical share.
		freeRecord(context.Run, fields)
		return result
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
			return Result{Value: value.List[index].Clone(context.Run)}
		}
		if value.Kind == core.String {
			index = normalizedIndex(index, utf8.RuneCountInString(value.Text))
			start := runeOffset(value.Text, index)
			end := runeOffset(value.Text, index+1)
			if start < 0 || end < 0 {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "index out of range")
			}
			return Result{Value: core.NewString(context.Run, value.Text[start:end])}
		}
		return failure(context.Run, "REF_MISSING", part.Span, "index needs a list or string")
	}
	if part.Kind == expr.ReferenceSlice {
		bounds := parseSlice(part.Text)
		if !bounds.OK {
			return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "invalid slice")
		}
		start, end := bounds.Start, bounds.End
		if value.Kind == core.List {
			bounds = normalizedSlice(start, end, len(value.List))
			if !bounds.OK {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "slice out of range")
			}
			start, end = bounds.Start, bounds.End
			return Result{Value: core.NewList(context.Run, value.List[start:end])}
		}
		if value.Kind == core.String {
			bounds = normalizedSlice(start, end, utf8.RuneCountInString(value.Text))
			if !bounds.OK {
				return failure(context.Run, "SEL_INDEX_INVALID", part.Span, "slice out of range")
			}
			start, end = bounds.Start, bounds.End
			return Result{Value: core.NewString(context.Run, value.Text[runeOffset(value.Text, start):runeOffset(value.Text, end)])}
		}
		return failure(context.Run, "REF_MISSING", part.Span, "slice needs a list or string")
	}
	return failure(context.Run, "REF_MISSING", part.Span, "invalid reference")
}

func parseIndex(text string) (int, bool) {
	if len(text) == 0 {
		return 0, false
	}
	negative, index := false, 0
	if text[0] == '-' {
		negative = true
		text = text[1:]
	}
	if len(text) == 0 {
		return 0, false
	}
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		index = index*10 + int(text[i]-'0')
	}
	if negative {
		index = -index
	}
	return index, true
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

type sliceBounds struct {
	Start int
	End   int
	OK    bool
}

func parseSlice(text string) sliceBounds {
	cut := -1
	for i := 0; i+1 < len(text); i++ {
		if text[i] == '.' && text[i+1] == '.' {
			cut = i
			break
		}
	}
	if cut < 0 {
		return sliceBounds{}
	}
	start, end, ok := 0, -2147483648, true
	if cut != 0 {
		start, ok = parseIndex(text[:cut])
	}
	if !ok {
		return sliceBounds{}
	}
	if cut+2 != len(text) {
		end, ok = parseIndex(text[cut+2:])
	}
	return sliceBounds{Start: start, End: end, OK: ok}
}

func normalizedSlice(start int, end int, length int) sliceBounds {
	if start < 0 {
		start = length + start
	}
	if end == -2147483648 {
		end = length
	} else if end < 0 {
		end = length + end
	}
	if start < 0 || end < start || end > length {
		return sliceBounds{}
	}
	return sliceBounds{Start: start, End: end, OK: true}
}

func (p *Program) selector(text string, span source.Span, context *Context) Result {
	_ = p
	values := context.Args
	offset := 1
	present := context.HasArgs
	if len(text) >= 2 && (text[1] == '<' || text[1] == '>') {
		present = false
		if len(context.RuleFrames) != 0 {
			frame := context.RuleFrames[len(context.RuleFrames)-1]
			if text[1] == '<' {
				values = frame.Inputs
			} else {
				values = frame.Outputs
			}
			present = true
		} else if text[1] == '<' {
			values = context.Inputs
			present = context.Inputs != nil
		} else {
			values = context.Outputs
			present = context.Outputs != nil
		}
		offset = 2
	}
	if !present {
		return failure(context.Run, "SEL_NO_CONTEXT", span, "selector has no context")
	}
	suffix := text[offset:]
	if suffix == "*" {
		return Result{Value: core.NewList(context.Run, values)}
	}
	if suffix == "#" {
		return Result{Value: core.Value{Kind: core.Int, Int: int64(len(values))}}
	}
	if suffix == "" || suffix == "_" {
		if len(values) == 0 {
			return failure(context.Run, "SEL_INDEX_INVALID", span, "selector index is empty")
		}
		return Result{Value: values[0].Clone(context.Run)}
	}
	if bounds := parseSlice(suffix); bounds.OK {
		bounds = normalizedSlice(bounds.Start, bounds.End, len(values))
		if !bounds.OK {
			return failure(context.Run, "SEL_INDEX_INVALID", span, "selector slice out of range")
		}
		return Result{Value: core.NewList(context.Run, values[bounds.Start:bounds.End])}
	}
	index, ok := parseIndex(suffix)
	if !ok {
		return failure(context.Run, "SEL_INDEX_INVALID", span, "invalid selector index")
	}
	index = normalizedIndex(index, len(values))
	if index < 0 || index >= len(values) {
		return failure(context.Run, "SEL_INDEX_INVALID", span, "selector index out of range")
	}
	return Result{Value: values[index].Clone(context.Run)}
}
