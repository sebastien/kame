package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/source"
	"strings"
)

// InspectKashRecipes parses recipes whose interpreter is known from this source.
// Source composition and computed shell selectors are resolved during execution.
func InspectKashRecipes(s *Script) {
	if len(s.Diagnostics) != 0 {
		return
	}
	defaultKash, builtinKash := true, true
	var globalShell *expr.Expr
	for _, item := range s.Items {
		if item.Kind == Definition {
			switch item.Definition.Name {
			case "SHELL":
				defaultKash = false
				if !item.Definition.Default && !item.Definition.Contribution {
					globalShell = item.Definition.Expression
				}
			case "kash":
				builtinKash = false
			}
		}
	}
	if mayLoadBindings(s.Items) {
		defaultKash, builtinKash = false, false
	}
	defaultKash = defaultKash || builtinKash && isKashName(globalShell)
	s.inspectKashRecipes(s.Items, defaultKash, builtinKash)
}

func mayLoadBindings(items []ScriptItem) bool {
	for _, item := range items {
		if item.Kind == Include || item.Kind == ModuleImport || item.Kind == ModuleFrom {
			return true
		}
		for _, branch := range item.Branches {
			if mayLoadBindings(branch.Items) {
				return true
			}
		}
		if item.Loop != nil && mayLoadBindings(item.Loop.Items) {
			return true
		}
	}
	return false
}

func (s *Script) inspectKashRecipes(items []ScriptItem, defaultKash, builtinKash bool) {
	for i := range items {
		item := &items[i]
		if item.Rule != nil {
			item.KashBody = inspectKashRuleBody(s.Alloc, s.Source, item.Rule, defaultKash, builtinKash)
			if item.KashBody != nil {
				s.Diagnostics = append(s.Diagnostics, item.KashBody.Diagnostics...)
			}
		}
		for _, branch := range item.Branches {
			s.inspectKashRecipes(branch.Items, defaultKash, builtinKash)
		}
		if item.Loop != nil {
			s.inspectKashRecipes(item.Loop.Items, defaultKash, builtinKash)
		}
	}
}

// InspectStandaloneKashRule assumes the default interpreter for a single rule.
func InspectStandaloneKashRule(a mem.Allocator, authored *source.Source, r *rule.Rule) *Script {
	return inspectKashRuleBody(a, authored, r, true, true)
}

func isKashName(value *expr.Expr) bool {
	value = expr.Ungroup(value)
	return value != nil && value.Kind == expr.Name && value.Text == "kash"
}

func inspectKashRuleBody(a mem.Allocator, authored *source.Source, r *rule.Rule, defaultKash, builtinKash bool) *Script {
	if !r.Declaration || len(r.Body) == 0 || !usesKashForInspection(r, defaultKash, builtinKash) {
		return nil
	}
	start := r.Body[0].Span.Start
	text := authored.Text[:r.Span.End]
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	s := mem.Alloc[Script](a)
	s.Source = source.Borrow(a, authored.Name, text)
	s.Source.StructuralIndent = text[lineStart:start]
	s.Alloc, s.Kash, s.RuleBody, s.BorrowedSource, s.start = a, true, true, true, lineStart
	p := kashParser{Script: s, Pos: start, RuleBody: true}
	s.Items = p.sequence(0)
	return s
}

func usesKashForInspection(r *rule.Rule, defaultKash, builtinKash bool) bool {
	metadata := expr.Ungroup(r.Metadata)
	if metadata == nil {
		return defaultKash
	}
	if metadata.Kind != expr.Record {
		return false
	}
	for _, field := range metadata.Fields {
		if field.Spread {
			return false
		}
	}
	for _, field := range metadata.Fields {
		if field.Key == "shell" {
			return builtinKash && isKashName(field.Value)
		}
	}
	return defaultKash
}

// FormatInspectedRule canonicalizes Kash bodies and preserves opaque recipe text.
func FormatInspectedRule(a mem.Allocator, r *rule.Rule, body *Script, indent string) string {
	if body == nil {
		return rule.FormatRuleWithIndent(a, r, indent)
	}
	header := *r
	header.Body = nil
	formattedHeader := rule.FormatRuleWithIndent(a, &header, indent)
	formattedBody := FormatWithIndent(a, body, indent)
	var b strings.Builder
	b.WriteString(formattedHeader)
	for line := range strings.SplitSeq(strings.TrimSuffix(formattedBody, "\n"), "\n") {
		b.WriteByte('\n')
		if line != "" {
			b.WriteString(indent)
			b.WriteString(line)
		}
	}
	mem.FreeString(a, formattedHeader)
	mem.FreeString(a, formattedBody)
	return owned(a, b.String())
}
