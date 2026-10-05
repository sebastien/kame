package ast

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

// WriteAST parses text as lang and writes its schema-1 AST JSON to out. It
// returns 0 on success and 1 when the source has errors or encoding fails. The
// JSON is written even when the parse has diagnostics, matching the CLI.
func WriteAST(out io.Writer, lang string, name string, text string) int {
	enc := newASTEncoder(out, lang, name)
	failed := false
	if lang == "expr" {
		result := expr.Parse(mem.System, name, text)
		enc.expr(result.Expr)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "template" {
		result := template.ParseString(mem.System, name, text)
		enc.template(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "rule" {
		result := rule.ParseRule(mem.System, name, text)
		enc.rule(result.Rule)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else {
		var result *script.Script
		var authored *source.Source
		if lang == "kash" {
			result = script.ParseKash(mem.System, name, text)
		} else if lang == "km" {
			authored = source.New(mem.System, name, text)
			result = script.ParseFragment(mem.System, authored, lang, 0, len(text))
		} else {
			result = script.Parse(mem.System, name, text)
		}
		enc.script(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
		if authored != nil {
			authored.Free(mem.System)
		}
	}
	enc.finish()
	if enc.err() != nil {
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

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
	if value.Kind == expr.String || value.Kind == expr.CommandWord {
		if value.Kind == expr.CommandWord {
			e.Str("splice")
			e.Bool(value.Bool)
		}
		e.Str("parts")
		e.BeginArray()
		for i := range value.Parts {
			p := value.Parts[i]
			e.BeginObject()
			e.Str("span")
			e.span(p.Span)
			if p.Expr != nil {
				if value.Kind == expr.CommandWord {
					e.Str("form")
					e.Str(p.Form)
				}
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
	if value.Kind == expr.Symbol || value.Kind == expr.Name || value.Kind == expr.Path || value.Kind == expr.Selector || value.Kind == expr.CommandRedirection || value.Kind == expr.CommandSetup || value.Kind == expr.KashBranch || value.Kind == expr.KashDefinition || value.Kind == expr.KashComment {
		e.Str("text")
		e.Str(value.Text)
	}
	if value.Kind == expr.Reference || value.Kind == expr.EnvironmentReference {
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
	if value.Kind == expr.List || value.Kind == expr.Application || value.Kind == expr.CommandCapture || value.Kind == expr.CommandStage || value.Kind == expr.CommandRedirection || value.Kind == expr.CommandSetup || value.Kind == expr.CommandGraph || value.Kind == expr.ValueRecovery || value.Kind == expr.CommandTest || value.Kind == expr.KashIf || value.Kind == expr.KashMatch || value.Kind == expr.KashBranch || value.Kind == expr.KashDefinition {
		if value.AcceptExit {
			e.Str("acceptExit")
			e.Bool(true)
		}
		if value.Async {
			e.Str("async")
			e.Bool(true)
		}
		if (value.Kind == expr.CommandCapture || value.Kind == expr.CommandGraph) && len(value.Body) != 0 {
			e.Str("fallback")
			e.expr(value.Body[0])
		}
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
	if value.Kind == expr.KashBranch {
		e.Str("body")
		e.exprs(value.Body)
	}
	if value.Kind == expr.KashMatch && len(value.Body) != 0 {
		e.Str("subject")
		e.expr(value.Body[0])
		if len(value.Body) > 1 {
			e.Str("comments")
			e.exprs(value.Body[1:])
		}
	}
	if value.Kind == expr.KashDefinition {
		e.Str("function")
		e.Bool(value.Bool)
	}
	if value.Kind == expr.Lambda || value.Kind == expr.KashDefinition {
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
	if k == expr.EnvironmentReference {
		return "environment-reference"
	}
	if k == expr.KashMatch {
		return "kash-match"
	}
	if k == expr.KashIf {
		return "kash-if"
	}
	if k == expr.KashBranch {
		return "kash-branch"
	}
	if k == expr.KashDefinition {
		return "kash-definition"
	}
	if k == expr.KashComment {
		return "kash-comment"
	}
	if k == expr.CommandTest {
		return "command-test"
	}
	if k == expr.ValueRecovery {
		return "value-recovery"
	}
	if k == expr.CommandCapture {
		return "command-capture"
	}
	if k == expr.CommandGraph {
		return "command-graph"
	}
	if k == expr.CommandWord {
		return "command-word"
	}
	if k == expr.CommandStage {
		return "command-stage"
	}
	if k == expr.CommandRedirection {
		return "command-redirection"
	}
	if k == expr.CommandSetup {
		return "command-setup"
	}
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
	if k == template.Tool {
		return "tool"
	}
	return "selector"
}

func (e *astEncoder) rule(value *rule.Rule) {
	if value == nil {
		e.Null()
		return
	}
	e.node(ruleKind(value.Kind), value.Span)
	if value.Metadata != nil {
		e.Str("metadata")
		e.expr(value.Metadata)
	}
	if value.Always {
		e.Str("always")
		e.Bool(true)
	}
	if len(value.Environment) != 0 {
		e.Str("environment")
		e.BeginArray()
		for i := range value.Environment {
			e.Str(value.Environment[i].Value)
		}
		e.EndArray()
	}
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
	if len(value.Arguments) != 0 {
		e.Str("arguments")
		e.BeginArray()
		for i := range value.Arguments {
			argument := value.Arguments[i]
			e.BeginObject()
			e.Str("name")
			e.Str(argument.Name)
			e.Str("optional")
			e.Bool(argument.Optional)
			if argument.Optional {
				e.Str("default")
				e.Str(argument.Default)
			}
			e.Str("span")
			e.span(argument.Span)
			e.EndObject()
		}
		e.EndArray()
	}
	e.Str("inputs")
	e.BeginArray()
	for i := range value.Inputs {
		in := value.Inputs[i]
		e.BeginObject()
		if in.OrderOnly {
			e.Str("orderOnly")
			e.Bool(true)
		}
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
	if k == rule.InputWildcard {
		return "wildcard"
	}
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
			if item.OptionalInclude {
				e.Str("optional")
				e.Bool(true)
			}
		}
		if item.Kind == script.Generate {
			e.Str("name")
			e.Str(item.GenerateName)
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
	if k == script.When {
		return "when"
	}
	if k == script.Otherwise {
		return "otherwise"
	}
	if k == script.EndWhen {
		return "end-when"
	}
	if k == script.Command {
		return "command"
	}
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
	if k == script.Generate {
		return "generate"
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
	if value.Default {
		e.Str("default")
		e.Bool(true)
	}
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
