package program

import (
 "kame/lang/expr"
 "kame/lang/script"
 "kame/lang/template"
 "solod.dev/so/mem"
 "solod.dev/so/slices"
 "solod.dev/so/strings"
)

func (p *Program) declareExpressionTools(value *expr.Expr) {
 if value == nil { return }
 if value.Kind == expr.Application && len(value.Items) == 2 && value.Items[0].Kind == expr.Name && value.Items[0].Text == "tool" {
  argument := value.Items[1]
  if argument.Kind == expr.Symbol || argument.Kind == expr.Path { p.declareTool(argument.Text) }
  if argument.Kind == expr.String {
   if len(argument.Parts) == 0 { p.declareTool(argument.Text) } else {
    builder := strings.NewBuilder(p.Alloc)
    literal := true
    for i := range argument.Parts { if argument.Parts[i].Expr != nil { literal = false; break }; builder.WriteString(argument.Parts[i].Text) }
    if literal { p.declareTool(builder.String()) }
    builder.Free()
   }
  }
 }
 for i := range value.Items { p.declareExpressionTools(value.Items[i]) }
 for i := range value.Body { p.declareExpressionTools(value.Body[i]) }
 for i := range value.Fields { p.declareExpressionTools(value.Fields[i].Value) }
 for i := range value.Parts { p.declareExpressionTools(value.Parts[i].Expr) }
}

func (p *Program) declareTemplateTools(value *template.String) {
 if value == nil { return }
 for i := range value.Parts { p.declareExpressionTools(value.Parts[i].Expr) }
}

func (p *Program) declareBuildTools(parsed *script.Script) {
 for i := range parsed.Items {
  item := parsed.Items[i]
  p.declareExpressionTools(item.Expression)
  if item.Definition != nil {
   p.declareExpressionTools(item.Definition.Expression)
   p.declareTemplateTools(item.Definition.Template)
   for j := range item.Definition.Words { p.declareTemplateTools(item.Definition.Words[j].Template) }
  }
  if item.Rule != nil {
   for j := range item.Rule.Inputs { p.declareTemplateTools(item.Rule.Inputs[j].Template) }
   for j := range item.Rule.Body { p.declareTemplateTools(item.Rule.Body[j].Template) }
  }
 }
}

func (p *Program) plannedTools() []Tool {
 var tools []Tool
 for i := range p.Tools {
  if !p.Tools[i].Declarative { continue }
  p.toolPath(p.Tools[i].Name)
  tools = slices.Append(p.Alloc, tools, Tool{Name: cloneText(p.Alloc, p.Tools[i].Name), Path: cloneText(p.Alloc, p.Tools[i].Path)})
 }
 return tools
}

func freeTools(a mem.Allocator, tools []Tool) {
 for i := range tools { mem.FreeString(a, tools[i].Name); mem.FreeString(a, tools[i].Path) }
 slices.Free(a, tools)
}

func cloneTools(a mem.Allocator, tools []Tool) []Tool {
 var cloned []Tool
 for i := range tools { cloned = slices.Append(a, cloned, Tool{Name: cloneText(a, tools[i].Name), Path: cloneText(a, tools[i].Path)}) }
 return cloned
}
