package ast

import (
	"io"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"strings"
)

// WriteAST parses text as lang and writes its schema-2 AST JSON to out. It
// returns 0 on success and 1 when the source has errors or encoding fails. The
// JSON is written even when the parse has diagnostics, matching the CLI.
// Rule captures use pattern. Quoted operands use template for their decoded
// string and interpolation AST.
func WriteAST(out io.Writer, lang string, name string, text string) int {
	lang, composition := strings.CutPrefix(lang, "composition:")
	label := lang
	if lang == script.KameFileLanguage {
		label = "km"
	}
	enc := newASTEncoder(out, label, name)
	failed := false
	if lang == "expr" {
		result := expr.Parse(mem.System, name, text)
		enc.expr(result.Expr)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "template" {
		result := template.ParseDocument(mem.System, name, text, "auto")
		enc.templateDocument(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "kash" || lang == "km" || lang == script.KameFileLanguage {
		var result *script.Script
		var authored *source.Source
		if lang == "kash" && !composition {
			result = script.ParseKash(mem.System, name, text)
		} else {
			authored = source.New(mem.System, name, text)
			result = script.ParseFragment(mem.System, authored, lang, 0, len(text))
			if !composition {
				result.ValidateAssignments()
				script.InspectKashRecipes(result)
			}
		}
		enc.script(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
		if authored != nil {
			authored.Free(mem.System)
		}
	} else {
		enc.Null()
		failed = enc.diagnostics([]source.Diagnostic{{Code: "OPT_VALUE_INVALID", Severity: source.Error, Message: "invalid language: " + lang}})
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
	enc          json.Encoder
	out          io.Writer
	processDepth int
}

func newASTEncoder(out io.Writer, lang string, name string) astEncoder {
	e := astEncoder{enc: json.NewEncoder(out), out: out}
	e.BeginObject()
	e.Str("schema")
	e.Int(2)
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
	kind := exprKind(value.Kind)
	if value.Kind == expr.Path && value.PathGlob {
		kind = "path-pattern"
	}
	e.node(kind, value.Span)
	if value.RuleSyntax != nil {
		e.Str("declaration")
		e.rule(value.RuleSyntax.Declaration.(*rule.Rule), nil)
	}
	if value.Kind == expr.CommandMux {
		e.Str("operator")
		e.Str(value.Text)
		if value.Int != 0 {
			e.Str("bufferBytes")
			e.Int(value.Int)
		}
		if value.StreamOverflow != 0 {
			e.Str("overflow")
			e.Str(expr.StreamOverflowName(value.StreamOverflow))
			e.Str("bufferItems")
			e.Int(int64(value.BufferItems))
		}
		if value.StreamOrder != 0 {
			e.Str("order")
			e.Str("round-robin")
		}
		if value.StreamFailure != 0 {
			e.Str("failure")
			e.Str(expr.StreamFailureName(value.StreamFailure))
		}
		if len(value.Body) != 0 {
			e.Str("selected")
			e.expr(value.Body[0])
		}
	}
	if value.Exit != "" {
		e.Str("rejoin")
		e.Str(value.Exit)
	}
	if value.Kind == expr.KashRecords {
		e.Str("frame")
		e.Str(value.RecordFrame)
	}
	process := value.Kind == expr.CommandGraph || value.Kind == expr.CommandCapture
	if value.ActionPrefix != "" {
		e.Str("actionPrefix")
		e.Str(value.ActionPrefix)
	}
	if value.Infix != "" {
		e.Str("operator")
		e.Str(value.Infix)
	}
	if value.Kind == expr.Group {
		e.Str("expression")
		e.expr(value.Items[0])
	}
	if value.Kind == expr.Match {
		e.Str("subject")
		if len(value.Body) == 0 {
			e.Null()
		} else {
			e.expr(value.Body[0])
		}
		e.Str("arms")
		e.exprs(value.Items)
	}
	if value.Kind == expr.MatchArm {
		if len(value.Items) != 0 {
			e.Str("pattern")
			e.expr(value.Items[0])
		}
		e.Str("result")
		if len(value.Body) == 0 {
			e.Null()
		} else {
			e.expr(value.Body[0])
		}
	}
	if value.Kind == expr.Conditional {
		e.Str("items")
		e.BeginArray()
		for _, part := range value.Items {
			e.expr(part)
		}
		e.EndArray()
	}
	if process {
		if e.processDepth == 0 {
			for i := range value.Items {
				if value.Items[i].Kind == expr.CommandMux || value.Items[i].Kind == expr.CommandGroup || value.Items[i].Kind == expr.CommandReader || value.Items[i].Kind == expr.CommandWriter {
					e.processTopology(value)
					break
				}
			}
		}
		e.processDepth++
	}
	if value.Kind == expr.ValuePipe {
		e.Str("items")
		e.BeginArray()
		for i := range value.Items {
			e.expr(value.Items[i])
		}
		e.EndArray()
	}
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
	if value.Kind == expr.String || value.Kind == expr.CommandWord || (value.Kind == expr.Path && len(value.Parts) != 0) {
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
				if value.Kind != expr.CommandWord {
					e.Str("form")
					e.Str(p.Form)
				}
			} else {
				e.Str("text")
				e.Str(p.Text)
			}
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.Symbol || value.Kind == expr.Name || value.Kind == expr.Path || value.Kind == expr.CommandRedirection || value.Kind == expr.CommandSetup || value.Kind == expr.KashBranch || value.Kind == expr.KashDefinition || value.Kind == expr.KashLet || value.Kind == expr.KashSet || value.Kind == expr.KashComment {
		e.Str("text")
		e.Str(value.Text)
	}
	if value.Kind == expr.CommandStream || value.Kind == expr.CommandReader || value.Kind == expr.CommandWriter {
		e.Str("channel")
		e.Str(value.Text)
	}
	if value.Kind == expr.CommandOperand {
		e.Str("sourceKind")
		if len(value.Body) != 0 {
			e.Str("producer")
		} else if value.Text == "" {
			e.Str("current")
		} else {
			e.Str("named")
		}
		e.Str("source")
		if len(value.Body) != 0 {
			e.expr(value.Body[0])
		} else if value.Text == "" {
			e.Str("current")
		} else {
			e.Str(value.Text)
		}
	}
	if value.Kind == expr.Reference || value.Kind == expr.EnvironmentReference {
		if value.Kind == expr.Reference && len(value.Items) != 0 {
			e.Str("base")
			e.expr(value.Items[0])
		}
		e.Str("parts")
		e.BeginArray()
		for i := range value.Reference {
			p := value.Reference[i]
			e.BeginObject()
			e.Str("kind")
			e.Str(referenceKind(p.Kind))
			e.Str("text")
			e.Str(p.Text)
			if p.Quoted {
				e.Str("quoted")
				e.Bool(true)
			}
			e.Str("span")
			e.span(p.Span)
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.List || value.Kind == expr.ListSpread || value.Kind == expr.ValueReturn || value.Kind == expr.KashSet || value.Kind == expr.KashYield || value.Kind == expr.KashExit || value.Kind == expr.KashLet || value.Kind == expr.Application || value.Kind == expr.CommandCapture || value.Kind == expr.CommandStage || value.Kind == expr.CommandGroup || value.Kind == expr.CommandMux || value.Kind == expr.CommandRedirection || value.Kind == expr.CommandSetup || value.Kind == expr.CommandGraph || value.Kind == expr.CommandList || value.Kind == expr.ValueRecovery || value.Kind == expr.CommandTest || expr.IsKashControl(value.Kind) || value.Kind == expr.KashBranch || value.Kind == expr.KashDefinition || value.Kind == expr.KashDefer {
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
		if value.Kind == expr.CommandList {
			e.Str("operators")
			e.BeginArray()
			for i := range value.CommandOps {
				e.BeginObject()
				e.Str("text")
				e.Str(value.CommandOps[i].Text)
				e.Str("span")
				e.span(value.CommandOps[i].Span)
				e.Str("afterNewline")
				e.Bool(value.CommandOps[i].AfterNewline)
				e.EndObject()
			}
			e.EndArray()
			e.Str("capture")
			e.Bool(value.CommandListCapture)
			e.Str("condition")
			e.Bool(value.CommandCondition)
		}
	}
	if value.Kind == expr.Record {
		e.Str("fields")
		e.BeginArray()
		for i := range value.Fields {
			f := value.Fields[i]
			e.BeginObject()
			e.Str("key")
			e.Str(f.Key)
			if f.Shorthand {
				e.Str("shorthand")
				e.Bool(true)
			}
			if f.Spread {
				e.Str("spread")
				e.Bool(true)
			}
			if f.Quoted {
				e.Str("quoted")
				e.Bool(true)
			}
			e.Str("span")
			e.span(f.Span)
			e.Str("value")
			e.expr(f.Value)
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == expr.KashBranch && value.Bool {
		e.Str("inline")
		e.Bool(true)
	}
	if value.Kind == expr.KashBranch || value.Kind == expr.KashWith {
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
	if value.Kind == expr.KashFunction {
		e.Str("name")
		e.Str(value.Text)
		if value.Bool {
			e.Str("generator")
			e.Bool(true)
		}
	}
	if value.Kind == expr.Lambda || value.Kind == expr.KashDefinition || value.Kind == expr.KashFunction {
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
	if value.Kind == expr.ValueBlock {
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
	if process {
		e.processDepth--
	}
	e.EndObject()
}

func (e *astEncoder) pattern(value *expr.Expr) {
	kind := "pattern"
	if value.Kind == expr.Path {
		kind = "path-pattern"
		if value.Pattern.References != 0 {
			kind = "path-template"
		}
	}
	e.node(kind, value.Span)
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
		if p.Kind == expr.PatternLiteral || p.Kind == expr.PatternGlob {
			e.Str("text")
			e.Str(p.Text)
		}
		if p.Kind == expr.PatternInterpolation {
			e.Str("index")
			e.Int(int64(p.Index))
			next := 0
			for j := range value.Parts {
				if value.Parts[j].Expr == nil {
					continue
				}
				if next == p.Index {
					e.Str("expr")
					e.expr(value.Parts[j].Expr)
					break
				}
				next++
			}
		}
		if p.Kind == expr.PatternMatcher {
			e.Str("pattern")
			e.Str(p.Pattern)
			e.Str("regex")
			e.Bool(p.Regex)
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
	if k == expr.PatternInterpolation {
		return "interpolation"
	}
	if k == expr.PatternLiteral {
		return "literal"
	}
	if k == expr.PatternMatcher {
		return "matcher"
	}
	if k == expr.PatternGlob {
		return "glob"
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
	if k == expr.KashSet {
		return "set"
	}
	if k == expr.RuleValue {
		return "rule-value"
	}
	if k == expr.KashYield {
		return "yield"
	}
	if k == expr.ValueBlock {
		return "value-block"
	}
	if k == expr.ValueReturn {
		return "return"
	}
	if k == expr.ListSpread {
		return "list-spread"
	}
	if k == expr.KashFunction {
		return "kash-function"
	}
	if k == expr.KashLet {
		return "kash-let"
	}
	if k == expr.KashExit {
		return "kash-exit"
	}
	if k == expr.KashWith {
		return "kash-with"
	}
	if k == expr.KashBreak {
		return "kash-break"
	}
	if k == expr.KashContinue {
		return "kash-continue"
	}
	if k == expr.KashRecords {
		return "kash-records"
	}
	if k == expr.CommandOperand {
		return "stream-operand"
	}
	if k == expr.CommandStream {
		return "channel-declaration"
	}
	if k == expr.CommandReader {
		return "channel-reader"
	}
	if k == expr.CommandWriter {
		return "channel-writer"
	}
	if k == expr.CommandInput {
		return "command-input"
	}
	if k == expr.CommandGroup {
		return "command-group"
	}
	if k == expr.CommandMux {
		return "command-mux"
	}
	if k == expr.KashDefer {
		return "kash-defer"
	}
	if k == expr.ValuePipe {
		return "value-pipe"
	}
	if k == expr.EnvironmentReference {
		return "environment-reference"
	}
	if k == expr.KashMatch {
		return "kash-match"
	}
	if k == expr.KashIf {
		return "kash-if"
	}
	if k == expr.KashFor {
		return "kash-for"
	}
	if k == expr.KashWhile {
		return "kash-while"
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
	if k == expr.Group {
		return "group"
	}
	if k == expr.Match {
		return "match"
	}
	if k == expr.MatchArm {
		return "match-arm"
	}
	if k == expr.Conditional {
		return "conditional"
	}
	if k == expr.CommandGraph {
		return "command-graph"
	}
	if k == expr.CommandList {
		return "command-list"
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
	return "invalid"
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

func (e *astEncoder) templateDocument(value *template.Document) {
	e.node("template", value.Root.Span)
	e.Str("body")
	e.expr(value.Root)
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
	return "tool"
}

func (e *astEncoder) rule(value *rule.Rule, body *script.Script) {
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
		if o.Expansion != nil {
			e.Str("template")
			e.template(o.Expansion)
		}
		if o.TargetForm != nil {
			offset := o.Span.Start
			if len(o.Text) >= 2 && o.Text[0] == '"' && o.Text[len(o.Text)-1] == '"' {
				offset++
			}
			e.Str("pattern")
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
		e.Str("sequenceEnd")
		e.Bool(in.SequenceEnd)
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
			e.Str("pattern")
			e.target(in.TargetForm, in.Span.Start)
		}
		e.EndObject()
	}
	e.EndArray()
	if value.BodyExpression != nil {
		e.Str("bodyExpression")
		e.expr(value.BodyExpression)
	}
	if body != nil {
		e.Str("bodyStatements")
		e.scriptItems(body.Items)
	}
	if value.BodyDoc != nil {
		e.Str("bodyDocument")
		e.templateDocument(value.BodyDoc)
	}
	e.Str("body")
	e.BeginArray()
	for i := range value.Body {
		line := value.Body[i]
		e.BeginObject()
		e.Str("text")
		e.Str(line.Text)
		e.Str("span")
		e.span(line.Span)
		if value.BodyDoc == nil {
			e.Str("template")
			e.template(line.Template)
		}
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
	if k == rule.TargetExpression {
		return "expression"
	}
	if k == rule.TargetString {
		return "string"
	}
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
			e.Str("regex")
			e.Bool(p.Regex != nil)
		}
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
}

func (e *astEncoder) script(value *script.Script) {
	e.node("script", source.Span{Start: 0, End: len(value.Source.Text)})
	e.Str("items")
	e.scriptItems(value.Items)
	e.EndObject()
}

func (e *astEncoder) scriptItems(items []script.ScriptItem) {
	e.BeginArray()
	for _, item := range items {
		e.BeginObject()
		e.Str("kind")
		e.Str(scriptKind(item.Kind))
		e.Str("span")
		e.span(item.Span)
		if item.Kind == script.Comment || item.Kind == script.SessionCommand {
			e.Str("text")
			e.Str(item.Text)
		}
		if item.Kind == script.Include {
			if item.Expression == nil {
				e.Str("path")
				e.Str(item.Include)
			}
			if item.OptionalInclude {
				e.Str("optional")
				e.Bool(true)
			}
			if item.BatchInclude {
				e.Str("batch")
				e.Bool(true)
			}
		}
		if item.Module != nil {
			e.moduleDeclaration(item.Module)
		}
		if item.Definition != nil {
			e.Str("definition")
			e.definition(item.Definition)
		}
		if item.Rule != nil {
			e.Str("rule")
			e.rule(item.Rule, item.KashBody)
		}
		if item.Kind == script.Composition {
			e.Str("branches")
			e.BeginArray()
			for _, branch := range item.Branches {
				e.BeginObject()
				e.Str("span")
				e.span(branch.Span)
				if branch.Condition != nil {
					e.Str("condition")
					e.expr(branch.Condition)
				}
				e.Str("items")
				e.scriptItems(branch.Items)
				e.EndObject()
			}
			e.EndArray()
		}
		if item.Loop != nil {
			e.Str("binding")
			e.Str(item.Loop.Name)
			e.Str("bindingSpan")
			e.span(item.Loop.NameSpan)
			e.Str("items")
			e.scriptItems(item.Loop.Items)
		}
		if item.Expression != nil {
			e.Str("expression")
			e.expr(item.Expression)
		}
		e.EndObject()
	}
	e.EndArray()
}

func (e *astEncoder) moduleDeclaration(module *script.ModuleDeclaration) {
	if module.Path != "" {
		e.Str("path")
		e.Str(module.Path)
		e.Str("pathSpan")
		e.span(module.PathSpan)
	}
	if module.Alias != "" {
		e.Str("alias")
		e.Str(module.Alias)
		e.Str("aliasSpan")
		e.span(module.AliasSpan)
	}
	if len(module.Names) != 0 {
		e.Str("names")
		e.BeginArray()
		for _, name := range module.Names {
			e.BeginObject()
			e.Str("name")
			e.Str(name.Name)
			e.Str("span")
			e.span(name.Span)
			if name.Alias != "" {
				e.Str("alias")
				e.Str(name.Alias)
				e.Str("aliasSpan")
				e.span(name.AliasSpan)
			}
			e.EndObject()
		}
		e.EndArray()
	}
}

func scriptKind(k script.ScriptItemKind) string {
	switch k {
	case script.ModuleImport:
		return "import"
	case script.ModuleFrom:
		return "from"
	case script.ModuleExport:
		return "export"
	case script.DeclarationFor:
		return "for"
	case script.DeclarationLet:
		return "let"
	}
	if k == script.SessionCommand {
		return "session-command"
	}
	if k == script.Composition {
		return "composition"
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
	e.Str("function")
	e.Bool(value.Function)
	if value.Eager {
		e.Str("eager")
		e.Bool(true)
	}
	if value.Default {
		e.Str("default")
		e.Bool(true)
	}
	if value.Contribution {
		e.Str("contribution")
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
	if value.Expression != nil {
		e.Str("expression")
		e.expr(value.Expression)
	}
	e.EndObject()
}
