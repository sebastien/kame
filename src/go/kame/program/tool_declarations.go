package program

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"

	"strings"
)

func (p *Program) declareExpressionTools(value *expr.Expr) {
	visitExpressionTools(value, func(name string, _ source.Span, _ bool) { p.declareTool(name) })
}

func visitExpressionTools(value *expr.Expr, visit func(name string, span source.Span, directExpansion bool)) {
	if value == nil {
		return
	}
	if value.Kind == expr.Name && strings.HasPrefix(value.Text, "x/") {
		visit(value.Text[2:], value.Span, value.Expansion != "")
	}
	if value.Kind == expr.CommandStage {
		if _, name := literalStageTool(value); name != "" {
			visit(name, value.Span, false)
		}
	}
	value = expr.Ungroup(value)
	if value.Kind == expr.Application && len(value.Items) > 1 && value.Items[0].Kind == expr.Name {
		switch value.Items[0].Text {
		case "tool":
			if len(value.Items) == 2 {
				visitLiteralTool(value.Items[1], visit)
			}
		case "run!", "capture!":
			head := 1
			if value.Items[0].Text == "capture!" {
				head++
			}
			for head < len(value.Items) && value.Items[head].Kind == expr.Symbol {
				head += 2
			}
			if head < len(value.Items) {
				visitLiteralTool(value.Items[head], visit)
			}
		}
	}
	for i := range value.Items {
		visitExpressionTools(value.Items[i], visit)
	}
	for i := range value.Body {
		visitExpressionTools(value.Body[i], visit)
	}
	for i := range value.Fields {
		visitExpressionTools(value.Fields[i].Value, visit)
	}
	for i := range value.Parts {
		visitExpressionTools(value.Parts[i].Expr, visit)
	}
}

func visitLiteralTool(value *expr.Expr, visit func(string, source.Span, bool)) {
	switch value.Kind {
	case expr.Symbol:
		visit(value.Text, value.Span, false)
	case expr.String, expr.Path:
		if value.Pattern != nil {
			return
		}
		if len(value.Parts) == 0 {
			visit(value.Text, value.Span, false)
			return
		}
		var text strings.Builder
		for _, part := range value.Parts {
			if part.Expr != nil {
				return
			}
			text.WriteString(part.Text)
		}
		visit(text.String(), value.Span, false)
	}
}

func visitTemplateTools(value *template.String, visit func(string, source.Span, bool)) {
	if value == nil {
		return
	}
	for _, part := range value.Parts {
		if part.Kind == template.Tool {
			visit(part.Text, part.Span, true)
		}
		visitExpressionTools(part.Expr, visit)
	}
}

func (p *Program) visitRuleTools(r *rule.Rule, shell *expr.Expr, visit func(string, source.Span, bool)) {
	visitExpressionTools(r.BodyExpression, visit)
	visitExpressionTools(r.Metadata, visit)
	for _, input := range r.Inputs {
		visitTemplateTools(input.Template, visit)
	}
	for _, output := range r.Outputs {
		visitTemplateTools(output.Expansion, visit)
	}
	if ruleSelectsKash(r, shell) {
		p.visitKashRecipeExpressions(r, func(value *expr.Expr) { visitExpressionTools(value, visit) })
	}
	if r.BodyDoc != nil {
		visitExpressionTools(r.BodyDoc.Root, visit)
		return
	}
	for _, line := range r.Body {
		visitTemplateTools(line.Template, visit)
	}
}

func visitRecipeToolReferences(r *rule.Rule, visit func(name string, span source.Span)) {
	if r.BodyDoc != nil {
		visitExpressionTools(r.BodyDoc.Root, func(name string, span source.Span, directExpansion bool) {
			if directExpansion {
				visit(name, span)
			}
		})
		return
	}
	for _, line := range r.Body {
		if line.Template == nil {
			continue
		}
		for _, part := range line.Template.Parts {
			if part.Kind == template.Tool {
				visit(part.Text, part.Span)
			}
		}
	}
}

func (p *Program) declareBuildTools(parsed *script.Script) {
	for i := range parsed.Items {
		item := parsed.Items[i]
		p.declareExpressionTools(item.Expression)
		if item.Definition != nil {
			p.declareExpressionTools(item.Definition.Expression)
		}
		if item.Rule != nil {
			p.declareRuleTools(item.Rule, p.buildShell, false)
		}
	}
}

func (p *Program) declareRuleTools(r *rule.Rule, shell *expr.Expr, registered bool) {
	p.visitRuleTools(r, shell, func(name string, _ source.Span, directExpansion bool) {
		if !directExpansion {
			p.declareTool(name)
		} else if registered && name != "" && p.toolIndex(name) < 0 {
			p.Tools = append(p.Tools, Tool{Name: cloneText(p.Alloc, name)})
		}
	})
}

func authoredShell(parsed *script.Script) *expr.Expr {
	for i := range parsed.Items {
		definition := parsed.Items[i].Definition
		if definition != nil && definition.Name == "SHELL" && definition.Expression != nil {
			return definition.Expression
		}
	}
	return nil
}

func ruleSelectsKash(rule *rule.Rule, shell *expr.Expr) bool {
	globalKash := true
	if shell != nil {
		globalKash = shell.Kind == expr.Name && shell.Text == "kash"
	}

	if rule.Metadata != nil {
		for i := range rule.Metadata.Fields {
			field := rule.Metadata.Fields[i]
			if field.Key != "shell" {
				continue
			}
			return field.Value != nil && field.Value.Kind == expr.Name && field.Value.Text == "kash"
		}
	}
	return globalKash
}

func (p *Program) plannedTools() []Tool {
	var tools []Tool
	for i := range p.Tools {
		if !p.Tools[i].Declarative {
			continue
		}
		p.toolPath(p.Tools[i].Name)
		tools = append(tools, Tool{Name: cloneText(p.Alloc, p.Tools[i].Name), Path: cloneText(p.Alloc, p.Tools[i].Path)})
	}
	return tools
}

func freeTools(a mem.Allocator, tools []Tool) {
	for i := range tools {
		mem.FreeString(a, tools[i].Name)
		mem.FreeString(a, tools[i].Path)
	}
	mem.FreeSlice(nil, tools)
}

func cloneTools(a mem.Allocator, tools []Tool) []Tool {
	var cloned []Tool
	for i := range tools {
		cloned = append(cloned, Tool{Name: cloneText(a, tools[i].Name), Path: cloneText(a, tools[i].Path)})
	}
	return cloned
}
