package script

import (
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// IncludeForm borrows decoded path storage from its expression. It remains
// valid while the owning Script lives and must not be freed independently.
type IncludeForm struct {
	Path     string
	Optional bool
	OK       bool
	Span     source.Span
}

func formHead(e *expr.Expr) string {
	if e == nil || e.Kind != expr.Application || len(e.Items) == 0 || e.Items[0] == nil || e.Items[0].Kind != expr.Name {
		return ""
	}
	return e.Items[0].Text
}

// CompositionInclude inspects a validated include leaf without allocation.
func CompositionInclude(e *expr.Expr) IncludeForm {
	head := formHead(e)
	if (head != "include" && head != "include?") || len(e.Items) != 2 {
		return IncludeForm{}
	}
	path := e.Items[1]
	if path == nil || path.Pattern != nil {
		return IncludeForm{}
	}
	text := ""
	if path.Kind == expr.Path && len(path.Parts) == 0 {
		text = path.Text
		if !strings.HasPrefix(text, "./") && !strings.HasPrefix(text, "../") && !strings.HasPrefix(text, "/") {
			return IncludeForm{}
		}
	} else if path.Kind == expr.String && !path.Verbatim && len(path.Parts) == 1 && path.Parts[0].Expr == nil {
		text = path.Parts[0].Text
	} else {
		return IncludeForm{}
	}
	if text == "" || strings.ContainsAny(text, "\x00*?[]{}") {
		return IncludeForm{}
	}
	return IncludeForm{Path: text, Optional: head == "include?", OK: true, Span: e.Span}
}

func (s *Script) compositionError(e *expr.Expr, message string) {
	s.Diagnostics = slices.Append(s.Alloc, s.Diagnostics, source.Diagnostic{Code: "EXPR_INVALID", Severity: source.Error, Span: e.Span, Message: message})
}

// ValidateValueExpression rejects source-composition heads anywhere in a value
// tree, without taking ownership of the tree. Diagnostics have static messages;
// the caller owns the returned slice and frees it with slices.Free(a, result).
func ValidateValueExpression(a mem.Allocator, root *expr.Expr) []source.Diagnostic {
	s := Script{Alloc: a}
	s.compositionExpr(root, false)
	return s.Diagnostics
}

func validateValueScript(s *Script) {
	for i := range s.Items {
		item := &s.Items[i]
		if item.Definition != nil {
			s.compositionExpr(item.Definition.Expression, false)
		}
		s.compositionExpr(item.Expression, false)
	}
}

// One traversal visits every authored branch, including inactive branches and
// interpolation expressions. Only if body slots inherit composition placement.
func (s *Script) compositionExpr(e *expr.Expr, allowed bool) bool {
	if e == nil {
		return false
	}
	head := formHead(e)
	include := head == "include" || head == "include?"
	found := include
	if include {
		if !allowed {
			s.compositionError(e, "include/include? are source-composition forms, allowed only at top level or in composition if bodies")
		}
		if !CompositionInclude(e).OK {
			s.error(e.Span.Start, e.Span.End, "include/include? require exactly one literal nonempty path (./, ../, / path atom or noninterpolated quoted string), without globs or NUL")
		}
	}
	for i := range e.Items {
		body := allowed && head == "if" && i >= 2 && (i%2 == 0 || i == len(e.Items)-1)
		if s.compositionExpr(e.Items[i], body) {
			found = true
		}
	}
	for i := range e.Body {
		if s.compositionExpr(e.Body[i], false) {
			found = true
		}
	}
	for i := range e.Fields {
		if s.compositionExpr(e.Fields[i].Value, false) {
			found = true
		}
	}
	for i := range e.Parts {
		if s.compositionExpr(e.Parts[i].Expr, false) {
			found = true
		}
	}
	return found
}

func (s *Script) compositionBodies(e *expr.Expr) {
	if len(e.Items) < 3 {
		s.compositionError(e, "composition if requires a test and body")
	}
	for i := 2; i < len(e.Items); i++ {
		if i%2 != 0 && i != len(e.Items)-1 {
			continue
		}
		body := e.Items[i]
		bodyHead := formHead(body)
		if body != nil && body.Kind != expr.Nil && bodyHead != "if" && bodyHead != "include" && bodyHead != "include?" {
			s.compositionError(body, "composition if bodies must be include/include?, nested if, or :nil")
		}
		if bodyHead == "if" {
			s.compositionBodies(body)
		}
	}
}

func (s *Script) compositionTemplate(value *template.String) {
	if value == nil {
		return
	}
	for i := range value.Parts {
		s.compositionExpr(value.Parts[i].Expr, false)
	}
}

func validateComposition(s *Script) {
	for i := range s.Items {
		item := &s.Items[i]
		if item.Definition != nil {
			s.compositionExpr(item.Definition.Expression, false)
		}
		if item.Rule != nil {
			s.compositionExpr(item.Rule.Metadata, false)
			for j := range item.Rule.Outputs {
				s.compositionTemplate(item.Rule.Outputs[j].Expansion)
			}
			for j := range item.Rule.Inputs {
				s.compositionTemplate(item.Rule.Inputs[j].Template)
			}
			if item.Rule.BodyDoc != nil {
				s.compositionExpr(item.Rule.BodyDoc.Root, false)
			} else {
				for j := range item.Rule.Body {
					s.compositionTemplate(item.Rule.Body[j].Template)
				}
			}
		}
		found := s.compositionExpr(item.Expression, item.Kind == Expression)
		if item.Kind != Expression || !found {
			continue
		}
		leaf := CompositionInclude(item.Expression)
		if leaf.OK {
			item.Kind, item.Include, item.OptionalInclude = Include, leaf.Path, leaf.Optional
		} else if formHead(item.Expression) == "if" {
			s.compositionBodies(item.Expression)
			item.Kind = Composition
		}
	}
}
