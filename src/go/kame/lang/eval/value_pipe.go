package eval

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"slices"
)

// The parser cannot produce this name. It binds a stage's input without
// making the binding visible to authored expressions in either scope.
const pipeInputName = "\x00pipe"

type pipeInput struct {
	expression *expr.Expr
	result     Result
	ready      bool
	effects    []Effect
	writePaths []string
	attempt    int64
}

func (p *Program) pipeInputValue(scope *Scope, c *Context) Result {
	input := scope.pipeInput
	if input.ready {
		if c.Engine != nil && input.attempt != c.Engine.Attempt() {
			for _, effect := range input.effects {
				if !slices.ContainsFunc(c.Effects, func(current Effect) bool {
					return current.replayID == effect.replayID
				}) {
					c.Effects = append(c.Effects, effect.clone())
				}
			}
			for _, path := range input.writePaths {
				if !slices.Contains(c.WritePaths, path) {
					c.WritePaths = append(c.WritePaths, owned(c.Run, path))
				}
			}
			input.attempt = c.Engine.Attempt()
		}
		result := input.result
		result.Value = borrowValue(c.Run, result.Value)
		result.Diagnostic = result.Diagnostic.Clone(c.Run)
		return result
	}
	before, beforePaths := len(c.Effects), len(c.WritePaths)
	r := p.evaluate(c.Engine, scope.Parent, input.expression, c)
	if !r.Waiting && !r.Completed && r.Stream == nil {
		input.result = r
		input.result.Value = borrowValue(scope.Alloc, r.Value)
		// Each resumption reads the input again; share it so reads are O(1).
		input.result.Value.Share(scope.Alloc)
		input.result.Diagnostic = r.Diagnostic.Clone(scope.Alloc)
		input.ready = true
		if c.Engine != nil {
			input.attempt = c.Engine.Attempt()
			for i := before; i < len(c.Effects); i++ {
				if c.Effects[i].replayID == nil {
					c.Effects[i].replayID = new(byte)
				}
				input.effects = append(input.effects, c.Effects[i].clone())
			}
			for _, path := range c.WritePaths[beforePaths:] {
				input.writePaths = append(input.writePaths, owned(scope.Alloc, path))
			}
		}
	}
	return r
}

func substitutePipeInput(operand, input *expr.Expr) *expr.Expr {
	if operand.Kind == expr.Placeholder {
		return input
	}
	if (operand.Kind == expr.ListSpread || operand.Kind == expr.Reference) && len(operand.Items) == 1 {
		if child := substitutePipeInput(operand.Items[0], input); child != operand.Items[0] {
			copy := *operand
			copy.Items = []*expr.Expr{child}
			return &copy
		}
	}
	return operand
}

func freePipeScope(_ mem.Allocator, value any) { value.(*Scope).Free() }

func (p *Program) valuePipe(scope *Scope, e *expr.Expr, c *Context) Result {
	stageIndex := len(e.Items) - 1
	if stageIndex == 0 {
		return p.evaluate(c.Engine, scope, e.Items[0], c)
	}
	stage := e.Items[stageIndex]
	// Keep this activation separate from the operation at the same authored
	// span. It survives a wait between two uses of the input.
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = -e.Span.Start-1, stage.Span.End
	child, _ := c.OperationState().(*Scope)
	if child == nil {
		alloc := c.Run
		if c.Engine != nil {
			alloc = p.Alloc
		}
		input := e.Items[0]
		if stageIndex > 1 {
			prefix := *e
			prefix.Items = e.Items[:stageIndex]
			prefix.Span.End = prefix.Items[stageIndex-1].Span.End
			input = &prefix
		}
		child = newScope(alloc, scope)
		child.pipeInput = mem.Alloc[pipeInput](alloc)
		child.pipeInput.expression = expr.Clone(alloc, input)
		if c.Engine == nil {
			defer child.Free()
		} else {
			c.SetOperationState(child, freePipeScope)
		}
	}
	c.operationStart, c.operationEnd = start, end

	input := expr.Expr{Kind: expr.Name, Text: pipeInputName, Span: child.pipeInput.expression.Span,
		SourceOrigin: child.pipeInput.expression.SourceOrigin, EntrySource: child.pipeInput.expression.EntrySource}
	var result Result
	if value := substitutePipeInput(stage, &input); value != stage {
		result = p.evaluate(c.Engine, child, value, c)
	} else {
		args := []*expr.Expr{stage}
		if stage.Kind == expr.Application {
			args = stage.Items
		}
		items := make([]*expr.Expr, 0, len(args)+1)
		substituted := false
		for _, operand := range args {
			value := substitutePipeInput(operand, &input)
			substituted = substituted || value != operand
			items = append(items, value)
		}
		if !substituted {
			items = append(items, &input)
		}
		call := *stage
		call.Kind, call.Items = expr.Application, items
		result = p.evaluate(c.Engine, child, &call, c)
		mem.FreeSlice(nil, items)
	}
	if !result.Waiting {
		c.operationStart, c.operationEnd = -e.Span.Start-1, stage.Span.End
		c.ClearOperationState()
		c.operationStart, c.operationEnd = start, end
	}
	return result
}
