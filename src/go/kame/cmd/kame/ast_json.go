package main

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
)

type astEncoder struct {
	enc json.Encoder
	out io.Writer
}

func newASTEncoder(out io.Writer, lang string, name string) astEncoder {
	e := astEncoder{enc: json.NewEncoder(out), out: out}
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("lang")
	e.Str(lang)
	e.Str("source")
	e.Str(name)
	e.Str("ast")
	return e
}

func (e *astEncoder) BeginObject()        { e.enc.BeginObject() }
func (e *astEncoder) EndObject()          { e.enc.EndObject() }
func (e *astEncoder) BeginArray()         { e.enc.BeginArray() }
func (e *astEncoder) EndArray()           { e.enc.EndArray() }
func (e *astEncoder) Str(value string)    { e.enc.Str(value) }
func (e *astEncoder) Int(value int64)     { e.enc.Int(value) }
func (e *astEncoder) Float(value float64) { e.enc.Float(value) }
func (e *astEncoder) Bool(value bool)     { e.enc.Bool(value) }
func (e *astEncoder) Null()               { e.enc.Null() }
func (e *astEncoder) finish()             { e.EndObject(); e.enc.Flush(); io.WriteString(e.out, "\n") }
func (e *astEncoder) err() error          { return e.enc.Err() }

func (e *astEncoder) span(s source.Span) {
	e.BeginObject()
	e.Str("start")
	e.Int(int64(s.Start))
	e.Str("end")
	e.Int(int64(s.End))
	e.EndObject()
}
func (e *astEncoder) node(kind string, span source.Span) {
	e.BeginObject()
	e.Str("kind")
	e.Str(kind)
	e.Str("span")
	e.span(span)
}

func (e *astEncoder) diagnostics(diags []source.Diagnostic) bool {
	failed := false
	e.Str("diagnostics")
	e.BeginArray()
	for i := range diags {
		d := diags[i]
		e.BeginObject()
		e.Str("code")
		e.Str(d.Code)
		e.Str("severity")
		if d.Severity == source.Warning {
			e.Str("warning")
		} else {
			e.Str("error")
			failed = true
		}
		e.Str("message")
		e.Str(d.Message)
		e.Str("span")
		e.span(d.Span)
		e.EndObject()
	}
	e.EndArray()
	return failed
}

func (e *astEncoder) expr(value *expr.Expr) {
	if value == nil {
		e.Null()
		return
	}
	if value.Pattern != nil {
		e.pattern(value)
		return
	}
	e.node(exprKind(value.Kind), value.Span)
	if value.Kind == expr.Boolean {
		e.Str("value")
		e.Bool(value.Bool)
	}
	if value.Kind == expr.Integer {
		e.Str("value")
		e.Int(value.Int)
	}
	if value.Kind == expr.Float {
		e.Str("value")
		e.Float(value.Float)
	}
	if value.Kind == expr.String {
		e.Str("parts")
		e.BeginArray()
		for i := range value.Parts {
			p := value.Parts[i]
			e.BeginObject()
			e.Str("span")
			e.span(p.Span)
			if p.Expr != nil {
				e.Str("expression")
				e.expr(p.Expr)
				e.Str("brace")
				e.Bool(p.Brace)
			} else {
				e.Str("text")
				e.Str(p.Text)
			}
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.Symbol || value.Kind == expr.Name || value.Kind == expr.Path || value.Kind == expr.Selector {
		e.Str("text")
		e.Str(value.Text)
	}
	if value.Kind == expr.Reference {
		e.Str("parts")
		e.BeginArray()
		for i := range value.Reference {
			p := value.Reference[i]
			e.BeginObject()
			e.Str("kind")
			e.Str(referenceKind(p.Kind))
			e.Str("text")
			e.Str(p.Text)
			e.Str("span")
			e.span(p.Span)
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.List || value.Kind == expr.Application {
		e.Str("items")
		e.exprs(value.Items)
	}
	if value.Kind == expr.Record {
		e.Str("fields")
		e.BeginArray()
		for i := range value.Fields {
			f := value.Fields[i]
			e.BeginObject()
			e.Str("key")
			e.Str(f.Key)
			e.Str("span")
			e.span(f.Span)
			e.Str("value")
			e.expr(f.Value)
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.Lambda {
		e.Str("parameters")
		e.BeginArray()
		for i := range value.Parameters {
			p := value.Parameters[i]
			e.BeginObject()
			e.Str("name")
			e.Str(p.Name)
			e.Str("span")
			e.span(p.Span)
			e.Str("rest")
			e.Bool(p.Rest)
			e.EndObject()
		}
		e.EndArray()
		e.Str("body")
		e.exprs(value.Body)
	}
	if value.Kind == expr.Section {
		e.Str("arity")
		e.Int(int64(len(value.Parameters)))
		e.Str("body")
		e.exprs(value.Body)
	}
	if value.Kind == expr.Placeholder {
		e.Str("index")
		e.Int(value.Int)
	}
	e.EndObject()
}

func (e *astEncoder) pattern(value *expr.Expr) {
	e.node("pattern", value.Span)
	e.Str("text")
	e.Str(value.Text)
	e.Str("parts")
	e.BeginArray()
	for i := range value.Pattern.Parts {
		p := value.Pattern.Parts[i]
		e.BeginObject()
		e.Str("kind")
		e.Str(patternPartKind(p.Kind))
		e.Str("span")
		e.span(p.Span)
		if p.Kind == expr.PatternLiteral {
			e.Str("text")
			e.Str(p.Text)
		}
		if p.Kind == expr.PatternMatcher {
			e.Str("pattern")
			e.Str(p.Pattern)
			if p.Text != "" {
				e.Str("name")
				e.Str(p.Text)
			}
			e.Str("index")
			e.Int(int64(p.Index))
		}
		if p.Kind == expr.PatternReference {
			if p.Text != "" {
				e.Str("name")
				e.Str(p.Text)
			} else {
				e.Str("index")
				e.Int(int64(p.Index))
			}
		}
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}

func patternPartKind(k expr.PatternPartKind) string {
	if k == expr.PatternLiteral {
		return "literal"
	}
	if k == expr.PatternMatcher {
		return "matcher"
	}
	return "reference"
}

func (e *astEncoder) exprs(values []*expr.Expr) {
	e.BeginArray()
	for i := range values {
		e.expr(values[i])
	}
	e.EndArray()
}
func exprKind(k expr.Kind) string {
	if k == expr.Boolean {
		return "boolean"
	}
	if k == expr.Nil {
		return "nil"
	}
	if k == expr.Integer {
		return "integer"
	}
	if k == expr.Float {
		return "float"
	}
	if k == expr.String {
		return "string"
	}
	if k == expr.Symbol {
		return "symbol"
	}
	if k == expr.Name {
		return "name"
	}
	if k == expr.Path {
		return "path"
	}
	if k == expr.Selector {
		return "selector"
	}
	if k == expr.Reference {
		return "reference"
	}
	if k == expr.List {
		return "list"
	}
	if k == expr.Record {
		return "record"
	}
	if k == expr.Application {
		return "application"
	}
	if k == expr.Lambda {
		return "lambda"
	}
	if k == expr.Placeholder {
		return "placeholder"
	}
	if k == expr.Section {
		return "section"
	}
	return "invalid"
}
func referenceKind(k expr.ReferenceKind) string {
	if k == expr.ReferenceName {
		return "name"
	}
	if k == expr.ReferenceIndex {
		return "index"
	}
	if k == expr.ReferenceSlice {
		return "slice"
	}
	return "selection"
}

func (e *astEncoder) template(value *template.String) {
	if value == nil {
		e.Null()
		return
	}
	e.node("template", value.Span)
	e.Str("parts")
	e.BeginArray()
	for i := range value.Parts {
		p := value.Parts[i]
		e.BeginObject()
		e.Str("kind")
		e.Str(templateKind(p.Kind))
		e.Str("span")
		e.span(p.Span)
		if p.Expr != nil {
			e.Str("expression")
			e.expr(p.Expr)
		} else {
			e.Str("text")
			e.Str(p.Text)
		}
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}
func templateKind(k template.PartKind) string {
	if k == template.Literal {
		return "literal"
	}
	if k == template.Expression {
		return "expression"
	}
	if k == template.Reference {
		return "reference"
	}
	if k == template.Tool { return "tool" }
	return "selector"
}

func (e *astEncoder) rule(value *rule.Rule) {
	if value == nil {
		e.Null()
		return
	}
	e.node(ruleKind(value.Kind), value.Span)
	e.Str("header")
	e.span(value.Header)
	e.Str("outputs")
	e.BeginArray()
	for i := range value.Outputs {
		o := value.Outputs[i]
		e.BeginObject()
		e.Str("kind")
		e.Str(targetKind(o.Kind))
		e.Str("text")
		e.Str(o.Text)
		e.Str("span")
		e.span(o.Span)
		if o.TargetForm != nil {
			e.Str("template")
			offset := o.Span.Start
			if len(o.Text) >= 2 && o.Text[0] == '"' && o.Text[len(o.Text)-1] == '"' {
				offset++
			}
			e.target(o.TargetForm, offset)
		}
		e.EndObject()
	}
	e.EndArray()
	e.Str("inputs")
	e.BeginArray()
	for i := range value.Inputs {
		in := value.Inputs[i]
		e.BeginObject()
		e.Str("kind")
		e.Str(inputKind(in.Kind))
		e.Str("text")
		e.Str(in.Text)
		e.Str("span")
		e.span(in.Span)
		if in.Template != nil {
			e.Str("template")
			e.template(in.Template)
		}
		if in.TargetForm != nil {
			e.Str("template")
			e.target(in.TargetForm, in.Span.Start)
		}
		e.EndObject()
	}
	e.EndArray()
	e.Str("body")
	e.BeginArray()
	for i := range value.Body {
		line := value.Body[i]
		e.BeginObject()
		e.Str("text")
		e.Str(line.Text)
		e.Str("span")
		e.span(line.Span)
		e.Str("template")
		e.template(line.Template)
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}
func ruleKind(k rule.Kind) string {
	if k == rule.FileRule {
		return "file"
	}
	if k == rule.TaskRule {
		return "task"
	}
	if k == rule.CachedTaskRule {
		return "cached-task"
	}
	return "service"
}
func targetKind(k rule.TargetKind) string {
	if k == rule.TargetName {
		return "name"
	}
	if k == rule.TargetPath {
		return "path"
	}
	return "template"
}
func inputKind(k rule.InputKind) string {
	if k == rule.InputName {
		return "name"
	}
	if k == rule.InputPath {
		return "path"
	}
	if k == rule.InputTemplate {
		return "template"
	}
	if k == rule.InputString {
		return "string"
	}
	return "expression"
}

func (e *astEncoder) target(value *template.Target, offset int) {
	e.node("target-template", source.Span{Start: offset, End: offset + len(value.Source.Text)})
	e.Str("parts")
	e.BeginArray()
	for i := range value.Parts {
		p := value.Parts[i]
		e.BeginObject()
		e.Str("span")
		e.span(source.Span{Start: p.Span.Start + offset, End: p.Span.End + offset})
		if p.Kind == template.TargetLiteral {
			e.Str("kind")
			e.Str("literal")
			e.Str("text")
			e.Str(p.Text)
		} else {
			e.Str("kind")
			e.Str("capture")
			e.Str("name")
			e.Str(p.Name)
			e.Str("pattern")
			e.Str(p.Pattern)
		}
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}

func (e *astEncoder) script(value *script.Script) {
	e.node("script", source.Span{Start: 0, End: len(value.Source.Text)})
	e.Str("items")
	e.BeginArray()
	for i := range value.Items {
		item := value.Items[i]
		e.BeginObject()
		e.Str("kind")
		e.Str(scriptKind(item.Kind))
		e.Str("span")
		e.span(item.Span)
		if item.Kind == script.Comment {
			e.Str("text")
			e.Str(item.Text)
		}
		if item.Kind == script.Include {
			e.Str("path")
			e.Str(item.Include)
		}
		if item.Definition != nil {
			e.Str("definition")
			e.definition(item.Definition)
		}
		if item.Rule != nil {
			e.Str("rule")
			e.rule(item.Rule)
		}
		if item.Expression != nil {
			e.Str("expression")
			e.expr(item.Expression)
		}
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}
func scriptKind(k script.ScriptItemKind) string {
	if k == script.Comment {
		return "comment"
	}
	if k == script.Definition {
		return "definition"
	}
	if k == script.Rule {
		return "rule"
	}
	if k == script.Include {
		return "include"
	}
	return "expression"
}

func (e *astEncoder) definition(value *definition.Definition) {
	if value == nil {
		e.Null()
		return
	}
	e.node("definition", value.Span)
	e.Str("name")
	e.Str(value.Name)
	e.Str("nameSpan")
	e.span(value.NameSpan)
	e.Str("parameters")
	e.BeginArray()
	for i := range value.Parameters {
		p := value.Parameters[i]
		e.BeginObject()
		e.Str("name")
		e.Str(p.Name)
		e.Str("span")
		e.span(p.Span)
		e.Str("rest")
		e.Bool(p.Rest)
		e.EndObject()
	}
	e.EndArray()
	e.Str("valueKind")
	e.Str(definitionKind(value.ValueKind))
	if value.Expression != nil {
		e.Str("expression")
		e.expr(value.Expression)
	}
	if value.Template != nil {
		e.Str("template")
		e.template(value.Template)
	}
	if len(value.Words) != 0 {
		e.Str("words")
		e.BeginArray()
		for i := range value.Words {
			word := value.Words[i]
			e.BeginObject()
			e.Str("text")
			e.Str(word.Text)
			e.Str("span")
			e.span(word.Span)
			e.Str("template")
			e.template(word.Template)
			e.EndObject()
		}
		e.EndArray()
	}
	e.EndObject()
}
func definitionKind(k definition.ValueKind) string {
	if k == definition.ValueWords {
		return "words"
	}
	if k == definition.ValueExpression {
		return "expression"
	}
	return "template"
}
