package script

import (
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

type assignmentBinding struct {
	previous   *assignmentBinding
	let        *expr.Expr
	declared   int
	invocation int
	mutable    bool
	captured   bool
}

type assignmentScope struct {
	parent     *assignmentScope
	invocation int
	bindings   map[string]*assignmentBinding
	sessionLet func(string) (exists, captured bool)
}

func (s *assignmentScope) child(invocation bool) *assignmentScope {
	child := &assignmentScope{parent: s, invocation: s.invocation, bindings: make(map[string]*assignmentBinding)}
	if invocation {
		child.invocation++
	}
	return child
}

func (s *assignmentScope) bind(name string, declared int, mutable bool) {
	s.bindings[name] = &assignmentBinding{declared: declared, mutable: mutable, invocation: s.invocation}
}

func (s *assignmentScope) bindLet(e *expr.Expr) {
	previous := s.bindings[e.Text]
	s.bind(e.Text, e.Span.End, true)
	b := s.bindings[e.Text]
	b.let = e
	if previous != nil && previous.let != nil {
		b.previous = previous
		for _, declaration := range []*expr.Expr{previous.let, e} {
			if declaration.LetBinding == nil {
				declaration.LetBinding = &expr.LetBinding{Start: declaration.Span.Start}
			}
		}
	}
}

func (s *assignmentScope) lookup(name string, position int) *assignmentBinding {
	for ; s != nil; s = s.parent {
		if b := s.bindings[name]; b != nil {
			for b.previous != nil && b.declared > position {
				b = b.previous
			}
			return b
		}
		if s.sessionLet != nil {
			if exists, captured := s.sessionLet(name); exists {
				s.bind(name, 0, true)
				s.bindings[name].captured = captured
				return s.bindings[name]
			}
		}
	}
	return nil
}

func (s *assignmentScope) reference(e *expr.Expr, name string) {
	e.LetBinding = nil
	if b := s.lookup(name, e.Span.Start); b != nil {
		if b.invocation < s.invocation {
			b.captured = true
		}
		if b.let != nil {
			e.LetBinding = b.let.LetBinding
		}
	}
}

type checkedAssignment struct {
	expression *expr.Expr
	binding    *assignmentBinding
}

type assignmentCheck struct {
	script      *Script
	interactive bool
	assignments []checkedAssignment
}

// ValidateAssignments checks lexical assignments after the full source unit
// has been assembled. Capture checks need to see code after each assignment.
func (s *Script) ValidateAssignments() []string {
	return s.ValidateSessionAssignments(nil)
}

// ValidateSessionAssignments also resolves lets retained by an interactive
// session and returns captures to retain with its top-level bindings.
func (s *Script) ValidateSessionAssignments(sessionLet func(string) (exists, captured bool)) []string {
	for _, d := range s.Diagnostics {
		if d.Severity == source.Error {
			return nil
		}
	}
	root := &assignmentScope{bindings: make(map[string]*assignmentBinding), sessionLet: sessionLet}
	for _, item := range s.Items {
		if d := item.Definition; d != nil {
			if d.Eager {
				root.bindLet(d.Expression)
			} else {
				root.bind(d.Name, d.Span.End, false)
			}
		}
		if item.Kind == ModuleFrom {
			for _, binding := range item.Module.Names {
				name := binding.Name
				if binding.Alias != "" {
					name = binding.Alias
				}
				root.bind(name, item.Span.End, false)
			}
		}
	}
	check := assignmentCheck{script: s, interactive: sessionLet != nil}
	for _, item := range s.Items {
		check.walk(item.Expression, root)
		if d := item.Definition; d != nil {
			scope := root
			if !d.Eager && d.Expression != nil && d.Expression.Kind != expr.KashFunction && d.Expression.Kind != expr.ValueBlock {
				scope = root.child(true)
				for _, parameter := range d.Parameters {
					scope.bind(parameter.Name, 0, false)
				}
			}
			check.walk(d.Expression, scope)
		}
		if item.Rule != nil {
			check.walk(item.Rule.BodyExpression, root.child(true))
		}
	}
	for _, assignment := range check.assignments {
		span := assignment.expression.Span
		switch {
		case assignment.binding == nil:
			s.error(span.Start, span.End, "set requires an existing let in the same invocation")
		case assignment.binding.captured:
			s.error(span.Start, span.End, "set cannot assign a captured let")
		}
	}
	var captures []string
	for name, b := range root.bindings {
		if b.mutable && b.captured {
			captures = append(captures, name)
		}
	}
	return captures
}

func (c *assignmentCheck) block(body []*expr.Expr, scope *assignmentScope) {
	for _, statement := range body {
		if statement == nil {
			continue
		}
		switch statement.Kind {
		case expr.KashLet:
			scope.bindLet(statement)
		case expr.KashDefinition:
			scope.bind(statement.Text, statement.Span.End, false)
		}
	}
	for _, statement := range body {
		c.walk(statement, scope)
	}
}

func (c *assignmentCheck) walk(e *expr.Expr, scope *assignmentScope) {
	if e == nil {
		return
	}
	switch e.Kind {
	case expr.Name:
		scope.reference(e, e.Text)
	case expr.Reference:
		if len(e.Items) == 0 && len(e.Reference) != 0 {
			scope.reference(e, e.Reference[0].Text)
		}
	case expr.CommandStage:
		if head := expr.CommandHead(e); head != nil && len(head.Parts) == 1 && head.Parts[0].Expr == nil {
			name := head.Parts[0].Text
			if !c.interactive && expr.InteractiveCommand(name) && scope.lookup(name, head.Span.Start) == nil {
				c.script.error(head.Span.Start, head.Span.End, "command requires an interactive session")
			}
			scope.reference(head, name)
		}
	case expr.KashSet:
		if !strings.HasPrefix(e.Text, "env/") {
			e.LetBinding = nil
			b := scope.lookup(e.Text, e.Span.Start)
			if b != nil && (!b.mutable || b.invocation != scope.invocation || b.declared > e.Span.Start) {
				b = nil
			}
			if b != nil && b.let != nil {
				e.LetBinding = b.let.LetBinding
			}
			c.assignments = append(c.assignments, checkedAssignment{expression: e, binding: b})
		}
	case expr.Lambda, expr.Section, expr.KashFunction, expr.ValueBlock:
		child := scope.child(true)
		for _, parameter := range e.Parameters {
			child.bind(parameter.Name, 0, false)
		}
		c.block(e.Body, child)
		return
	case expr.KashDefinition:
		value := e.Items[0]
		if value.Kind != expr.KashFunction && value.Kind != expr.ValueBlock {
			scope = scope.child(true)
			for _, parameter := range e.Parameters {
				scope.bind(parameter.Name, 0, false)
			}
		}
	case expr.KashFor, expr.KashRecords:
		branch := e.Items[0]
		for _, operand := range branch.Items {
			c.walk(operand, scope)
		}
		child := scope.child(false)
		child.bind(e.Text, 0, false)
		if e.Kind == expr.KashFor {
			iterable := expr.Ungroup(branch.Items[0])
			if iterable != nil && iterable.Kind == expr.Path && iterable.Pattern != nil && iterable.Pattern.References == 0 {
				for _, name := range iterable.Pattern.Names {
					if name != "" {
						child.bind(name, 0, false)
					}
				}
			}
		}
		c.block(branch.Body, child)
		return
	case expr.KashMatch:
		for _, subject := range e.Body {
			c.walk(subject, scope)
		}
		for _, arm := range e.Items {
			c.branch(arm, scope, true)
		}
		return
	case expr.KashBranch, expr.MatchArm, expr.KashWith:
		c.branch(e, scope, e.Kind == expr.MatchArm)
		return
	}
	for _, operand := range e.Items {
		c.walk(operand, scope)
	}
	for _, statement := range e.Body {
		c.walk(statement, scope)
	}
	for _, field := range e.Fields {
		c.walk(field.Value, scope)
	}
	for _, part := range e.Parts {
		c.walk(part.Expr, scope)
	}
	c.walk(e.CapturePolicy, scope)
}

func (c *assignmentCheck) branch(e *expr.Expr, scope *assignmentScope, match bool) {
	child := scope.child(false)
	if match && len(e.Items) > 0 && e.Items[0].Pattern != nil {
		for _, name := range e.Items[0].Pattern.Names {
			child.bind(name, 0, false)
		}
	}
	for _, operand := range e.Items {
		c.walk(operand, scope)
	}
	c.block(e.Body, child)
}
