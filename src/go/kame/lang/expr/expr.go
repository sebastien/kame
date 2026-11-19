// Package expr parses and formats Kame expressions.
package expr

import (
	"kame/internal/mem"
	"kame/lang/source"
	"slices"
	"strconv"
	"strings"
)

type Kind int

const (
	Invalid Kind = iota
	Boolean
	Nil
	Integer
	Float
	String
	Symbol
	Name
	Path
	Selector
	Reference
	List
	Record
	Application
	Lambda
	Placeholder
	Section
	CommandCapture
	CommandWord
	CommandStage
	CommandRedirection
	CommandSetup
	CommandGraph
	CommandList
	ValueRecovery
	CommandTest
	KashIf
	KashBranch
	KashDefinition
	KashComment
	KashMatch
	EnvironmentReference
	// CommandResult is the runtime lowering of expression-level run/pipe.
	CommandResult
	InvocationJoin
	// ValuePipe retains the authored subject and stages.
	ValuePipe
	CommandGroup
	CommandMux
	CommandInput
	CommandStream
	CommandReader
	CommandWriter
	CommandOperand
	KashDefer
	KashFor
	KashWhile
	ListSpread
	KashFunction
	KashLet
	KashExit
	KashWith
	KashBreak
	KashContinue
	KashRecords
	Group
	Conditional
	Match
	MatchArm
	ValueBlock
	ValueReturn
	KashSet
	KashYield
)

// Ungroup returns the expression inside any authored parentheses.
func Ungroup(e *Expr) *Expr {
	for e != nil && e.Kind == Group && len(e.Items) == 1 {
		e = e.Items[0]
	}
	return e
}

func IsKashControl(kind Kind) bool {
	return kind == KashIf || kind == KashMatch || kind == KashFor || kind == KashWhile || kind == KashWith || kind == KashRecords
}

type ReferenceKind int

const (
	ReferenceName ReferenceKind = iota
	ReferenceIndex
)

type ReferencePart struct {
	Kind     ReferenceKind
	Text     string
	Spelling string
	Span     source.Span
	Quoted   bool
}

type Field struct {
	Key       string
	Spelling  string
	Span      source.Span
	Value     *Expr
	Quoted    bool
	Shorthand bool
	Spread    bool
}

type Parameter struct {
	Name string
	Span source.Span
	Rest bool
}

type StringPart struct {
	Text string
	Span source.Span
	Expr *Expr
	// Form retains the authored interpolation or Kash word boundary.
	Form string
	// Spelling retains quoted source bytes that differ from rendered text.
	Spelling string
}

// Expr is immutable during evaluation; the compiler first attaches SourceOrigin.
// All child storage is owned by Result.Alloc.
type Expr struct {
	// SourceOrigin is compiler loading metadata (one-based), not authored syntax.
	// Generated trees retain zero even if their byte spans overlap a source file.
	SourceOrigin int
	// EntrySource is shared interactive provenance, retained by Go's GC rather
	// than a program-wide source table. Cloned/escaping ASTs keep it alive.
	EntrySource   *source.Source `json:"-"`
	CommentIndent int            // Authored Kash comment depth; comments do not close blocks.
	// Topology and Edges are used only by prepared process graphs. Authored
	// syntax retains groups and connections until graph validation/lowering.
	Topology       bool
	Edges          []CommandEdge
	CommandOps     []CommandOperator
	StreamKinds    []int
	InputRoot      int    // Lowered topology only: unique main input root, or -1.
	RecordFrame    string // Literal :lines or :nul for Kash record iteration.
	StreamOverflow int    // 0 block, 1 drop-new, 2 drop-old, 3 latest
	StreamOrder    int    // 0 ready, 1 strict framed round-robin (merge only)
	StreamFailure  int    // 0 inherited, 1 explicit fail-fast, 2 collect external exit statuses
	Each           bool   // prepared command template: one process per framed stdin record
	TypedCapture   bool   // capture! result: selected streams are exact bytes, not text
	CapturePolicy  *Expr  // Lowered capture! policy word; owned by this expression
	BufferItems    int
	Exit           string
	Channels       []string
	Async          bool
	Kash           bool
	// AcceptExit applies terminal Kash ? to the complete process graph.
	AcceptExit         bool
	CommandListCapture bool
	ActionPrefix       string
	CommandCondition   bool
	Kind               Kind
	Span               source.Span
	Text               string
	TextOwned          bool
	NamesOwned         bool
	Bool               bool
	Rest               bool
	Juxtaposed         bool
	Infix              string
	Arrow              bool
	Int                int64
	Float              float64
	Raw                bool
	Verbatim           bool
	VerbatimLen        int
	Parts              []StringPart
	Reference          []ReferencePart
	Items              []*Expr
	Fields             []Field
	Parameters         []Parameter
	// Body holds callable expressions, or one right-associative process fallback.
	Body         []*Expr
	Pattern      *Pattern
	LiteralPath  bool
	Expansion    string
	trivia       *expressionTrivia
	wordSpelling []string
	ExplicitDef  bool
}

type CommandEdge struct {
	From     int
	To       int
	Argument int
}

type CommandOperator struct {
	Text         string
	Span         source.Span
	AfterNewline bool
}

type Result struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Expr        *Expr
	Diagnostics []source.Diagnostic
}

// Prefix is an expression parsed from an existing Source. The caller owns the
// diagnostics slice and the expression tree; the Source remains caller-owned.
type Prefix struct {
	Expr        *Expr
	End         int
	Diagnostics []source.Diagnostic
	Closed      bool
}

func (r *Result) Free() {
	if r == nil {
		return
	}
	a := r.Alloc
	freeExpr(a, r.Expr)
	mem.FreeSlice(nil, r.Diagnostics)
	r.Source.Free(a)
	*r = Result{}
	mem.Free(a, r)
}

func freeExpr(a mem.Allocator, e *Expr) {
	if e == nil {
		return
	}
	for _, text := range e.wordSpelling {
		mem.FreeString(a, text)
	}
	mem.FreeSlice(nil, e.wordSpelling)
	if e.TextOwned {
		mem.FreeString(a, e.Text)
	}
	if e.trivia != nil {
		mem.FreeString(a, e.trivia.text)
		mem.FreeString(a, e.trivia.indent)
		mem.FreeSlice(nil, e.trivia.spans)
		mem.Free(a, e.trivia)
	}
	e.Pattern.Free(a)
	freeExpr(a, e.CapturePolicy)
	for i := range e.Parts {
		mem.FreeString(a, e.Parts[i].Text)
		mem.FreeString(a, e.Parts[i].Spelling)
		freeExpr(a, e.Parts[i].Expr)
	}
	for i := range e.Items {
		freeExpr(a, e.Items[i])
	}
	for i := range e.Fields {
		freeExpr(a, e.Fields[i].Value)
		mem.FreeString(a, e.Fields[i].Spelling)
		if e.NamesOwned || e.Fields[i].Quoted {
			mem.FreeString(a, e.Fields[i].Key)
		}
	}
	for i := range e.Body {
		freeExpr(a, e.Body[i])
	}
	// Section parameters are synthesized ("_0", ...) and allocator-owned;
	// lambda parameters borrow source text and stay unfreed.
	if e.Kind == Section || e.NamesOwned {
		for i := range e.Parameters {
			mem.FreeString(a, e.Parameters[i].Name)
		}
	}
	for i := range e.Reference {
		mem.FreeString(a, e.Reference[i].Spelling)
		if e.NamesOwned || e.Reference[i].Quoted {
			mem.FreeString(a, e.Reference[i].Text)
		}
	}
	mem.FreeSlice(nil, e.Parts)
	mem.FreeSlice(nil, e.Reference)
	mem.FreeSlice(nil, e.Items)
	mem.FreeSlice(nil, e.Fields)
	mem.FreeSlice(nil, e.Parameters)
	mem.FreeSlice(nil, e.Body)
	mem.FreeSlice(nil, e.Edges)
	mem.FreeSlice(nil, e.CommandOps)
	mem.FreeSlice(nil, e.StreamKinds)
	for i := range e.Channels {
		mem.FreeString(a, e.Channels[i])
	}
	mem.FreeSlice(nil, e.Channels)
	mem.Free(a, e)
}

// Free releases an expression retained by another language AST.
func Free(a mem.Allocator, e *Expr) { freeExpr(a, e) }

// Clone returns an independent expression tree for runtime-owned definitions.
func Clone(a mem.Allocator, e *Expr) *Expr { return cloneExpr(a, e) }

type parser struct {
	a                 mem.Allocator
	s                 *source.Source
	pos               int
	diags             []source.Diagnostic
	triviaSpans       []source.Span
	kash              bool
	commandGroup      bool
	delimitedValue    bool
	stopAtRecordFrame bool
	// One-based root position: only a top-level Kash path stops at semicolons.
	valuePathStart int
	// Rule indentation is structural, including inside multiline string data.
	indent string
}

func Parse(a mem.Allocator, name string, text string) *Result {
	p := parser{a: a, s: source.New(a, name, text)}
	p.skipSpace()
	e := p.valueExpression()
	p.skipSpace()
	if e != nil && p.pos != len(p.s.Text) {
		p.error(p.pos, p.pos+1, "unexpected trailing input")
	}
	if len(p.diags) == 0 {
		p.checkPlaceholders(e)
	}
	p.retainTrivia(e)
	r := mem.Alloc[Result](a)
	r.Alloc, r.Source, r.Expr, r.Diagnostics = a, p.s, e, p.diags
	return r
}

func ParsePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	e := p.expression()
	if len(p.diags) == 0 {
		p.checkPlaceholders(e)
	}
	p.retainTrivia(e)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseValuePrefix reads one complete value expression from an authored source.
func ParseValuePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	return parseValuePrefix(a, s, start, false)
}

// ParseKashValueExpressionPrefix parses a value expression in a Kash block.
func ParseKashValueExpressionPrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	return parseValuePrefix(a, s, start, true)
}

// ParseKashRecordSourcePrefix leaves the literal framing operand for the loop parser.
func ParseKashRecordSourcePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start, indent: s.StructuralIndent, kash: true, stopAtRecordFrame: true}
	return p.valuePrefix()
}

func parseValuePrefix(a mem.Allocator, s *source.Source, start int, kash bool) Prefix {
	p := parser{a: a, s: s, pos: start, indent: s.StructuralIndent, kash: kash}
	return p.valuePrefix()
}

func (p *parser) valuePrefix() Prefix {
	e := p.valueExpression()
	if len(p.diags) == 0 {
		p.checkPlaceholders(e)
	}
	p.retainTrivia(e)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseRange parses exactly one expression, retaining authored source offsets.
// A nonempty structural indent is stripped from multiline string content.
// The caller owns the expression and diagnostics; source text stays borrowed.
func ParseRange(a mem.Allocator, s *source.Source, start int, end int, indent string) Prefix {
	if indent == "" {
		indent = s.StructuralIndent
	}
	bounded := source.Borrow(a, s.Name, s.Text[:end])
	defer mem.Free(a, bounded)
	p := parser{a: a, s: bounded, pos: start, indent: indent}
	p.skipSpace()
	e := p.valueExpression()
	p.skipSpace()
	if e != nil && p.pos != end {
		p.error(p.pos, p.pos+1, "unexpected trailing input; expected one Kame expression")
	}
	if len(p.diags) == 0 {
		p.checkPlaceholders(e)
	}
	p.retainTrivia(e)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseQuotedTemplatePrefix decodes a quoted template without classifying its
// literal text as an expression pattern. Nested expressions keep normal grammar.
func ParseQuotedTemplatePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	if start < 0 || start >= len(s.Text) || s.Text[start] != '"' {
		p.error(start, start, "expected quoted string")
		return Prefix{End: start, Diagnostics: p.diags}
	}
	e := p.quoted(false)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseExpansionPrefix reads a template expansion. End includes the closing
// delimiter; malformed openers can remain literal in document text.
func ParseExpansionPrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	closer := byte('}')
	var e *Expr
	if strings.HasPrefix(s.Text[start:], "@(") {
		p.pos++
		closer = ')'
		e = p.paren()
		if e != nil {
			e.Expansion = "@("
		}
	} else {
		e = p.interpolation()
	}
	closed := p.pos > start+2 && s.Text[p.pos-1] == closer
	for i := range p.diags {
		if strings.HasPrefix(p.diags[i].Message, "unclosed") {
			closed = false
		}
	}
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags, Closed: closed}
}

func ParseKashExpressionPrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start, kash: true}
	e := p.expression()
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

func (p *parser) error(start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = append(p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) node(kind Kind, start int) *Expr {
	e := mem.Alloc[Expr](p.a)
	e.Kind, e.Span, e.Kash = kind, source.Span{Start: start, End: p.pos}, p.kash
	return e
}

func (p *parser) skipSpace() int {
	start := p.pos
	lineStart := -1
	for p.pos < len(p.s.Text) {
		next := source.ContinuationEnd(p.s.Text, p.pos, len(p.s.Text))
		if next != p.pos {
			p.pos = next
			lineStart = -1
			continue
		}
		b := p.s.Text[p.pos]
		if b == '#' {
			p.lineComment()
			lineStart = -1
			continue
		}
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			break
		}
		if b == '\n' {
			if lineStart >= 0 {
				p.triviaSpans = append(p.triviaSpans, source.Span{Start: lineStart, End: p.pos})
			}
			lineStart = p.pos + 1
		}
		p.pos++
	}
	return p.pos - start
}

func (p *parser) lineComment() {
	start := p.pos
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != '\r' {
		p.pos++
	}
	p.triviaSpans = append(p.triviaSpans, source.Span{Start: start, End: p.pos})
}

func (p *parser) expression() *Expr {
	base := p.atom()
	if base == nil || p.pos == len(p.s.Text) || p.s.Text[p.pos] != '.' {
		return base
	}
	e := p.node(Reference, base.Span.Start)
	e.Items = append(e.Items, base)
	p.referenceFields(e)
	return e
}

func (p *parser) inlineSpace() {
	for p.pos < len(p.s.Text) {
		next := source.ContinuationEnd(p.s.Text, p.pos, len(p.s.Text))
		if next != p.pos {
			p.pos = next
		} else if p.s.Text[p.pos] == ' ' || p.s.Text[p.pos] == '\t' {
			p.pos++
		} else {
			break
		}
	}
}

func (p *parser) valueSpace() {
	if p.delimitedValue {
		p.skipSpace()
	} else {
		p.inlineSpace()
	}
}

func (p *parser) operatorSpace(start int) {
	p.valueSpace()
	if p.delimitedValue {
		return
	}
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '#' {
		p.lineComment()
	}
	next := p.pos
	text := p.s.Text
	if strings.HasPrefix(text[next:], "\r\n") {
		next += 2
	} else if next < len(text) && text[next] == '\n' {
		next++
	} else {
		return
	}
	i := next
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	base := strings.LastIndexByte(text[:start], '\n') + 1
	end := base
	for end < start && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	if Columns(text[next:i], 0) > Columns(text[base:end], 0) {
		p.pos = i
	}
}

func (p *parser) takeKeyword(keyword string) bool {
	start := p.pos
	if strings.HasPrefix(p.s.Text[start:], keyword) && p.name().Text == keyword {
		return true
	}
	p.pos = start
	return false
}

func (p *parser) branchKeyword() bool {
	start := p.pos
	name := p.name().Text
	p.pos = start
	return name == "then" || name == "elif" || name == "else"
}

func (p *parser) valueExpression() *Expr {
	start := p.pos
	if p.branchKeyword() {
		p.error(start, start+1, "expected value before branch keyword")
		return nil
	}
	if !p.takeKeyword("if") {
		if lambda := p.arrowLambda(); lambda != nil {
			return lambda
		}
		return p.valuePipeTail(p.infix(1))
	}
	e := p.node(Conditional, start)
	for {
		if !p.conditionalOperand(e) || !p.takeKeyword("then") {
			p.error(p.pos, p.pos, "expected 'then' after condition")
			break
		}
		if !p.conditionalOperand(e) {
			break
		}
		if p.takeKeyword("elif") {
			continue
		}
		if p.takeKeyword("else") {
			p.conditionalOperand(e)
		} else {
			p.error(p.pos, p.pos, "expected 'elif' or 'else' after branch")
		}
		break
	}
	return e
}

func (p *parser) conditionalOperand(e *Expr) bool {
	p.valueSpace()
	before := len(p.diags)
	operand := p.valueExpression()
	if operand == nil {
		return false
	}
	e.Items = append(e.Items, operand)
	e.Span.End = operand.Span.End
	p.valueSpace()
	return len(p.diags) == before
}

func (p *parser) applicationArgument() *Expr {
	if strings.HasPrefix(p.s.Text[p.pos:], "...") {
		return p.spread(p.expression, "argument")
	}
	return p.expression()
}

func (p *parser) spread(parse func() *Expr, subject string) *Expr {
	start := p.pos
	p.pos += 3
	spread := p.node(ListSpread, start)
	if p.pos == len(p.s.Text) || strings.ContainsRune(" \t\r\n,)]}#", rune(p.s.Text[p.pos])) {
		p.error(start, p.pos, subject+" spread requires an adjacent expression")
		return spread
	}
	if operand := parse(); operand != nil {
		spread.Items = []*Expr{operand}
		spread.Span.End = operand.Span.End
	}
	return spread
}

func (p *parser) application() *Expr {
	head := p.expression()
	if head == nil || len(p.diags) != 0 {
		return head
	}
	var call *Expr
	for {
		end := p.pos
		p.valueSpace()
		operator, _ := p.binaryOperator()
		if p.pos == end || p.pos == len(p.s.Text) || operator != "" || p.branchKeyword() || strings.ContainsRune("\r\n,)]}#;=|", rune(p.s.Text[p.pos])) {
			p.pos = end
			break
		}
		if p.stopAtRecordFrame && !p.delimitedValue && p.atRecordFrame() {
			break
		}
		if strings.HasPrefix(p.s.Text[p.pos:], "()") {
			start := p.pos
			p.pos += 2
			if call != nil {
				p.error(start, p.pos, "() must be the sole argument")
				break
			}
			call = p.node(Application, head.Span.Start)
			call.Juxtaposed, call.Items = true, []*Expr{head}
			break
		}
		arg := p.applicationArgument()
		if arg == nil {
			break
		}
		if call == nil {
			call = p.node(Application, head.Span.Start)
			call.Juxtaposed, call.Items = true, []*Expr{head}
		}
		call.Items = append(call.Items, arg)
		call.Span.End = p.pos
		if len(p.diags) != 0 {
			break
		}
	}
	if call != nil {
		return call
	}
	return head
}

func (p *parser) atRecordFrame() bool {
	rest := p.s.Text[p.pos:]
	for _, frame := range []string{":lines", ":nul"} {
		if tail, ok := strings.CutPrefix(rest, frame); ok {
			tail = strings.TrimLeft(tail, " \t\r")
			if tail == "" || strings.ContainsRune("\n#;", rune(tail[0])) {
				return true
			}
		}
	}
	return false
}

func (p *parser) binaryOperator() (spelling, operation string) {
	text := p.s.Text
	rest := text[p.pos:]
	if len(rest) == 0 {
		return "", ""
	}
	switch rest[0] {
	case '?':
		if strings.HasPrefix(rest, "??") {
			return "??", "??"
		}
	case 'o', 'a':
		keyword := "and"
		if rest[0] == 'o' {
			keyword = "or"
		}
		if strings.HasPrefix(rest, keyword) && (len(rest) == len(keyword) ||
			(!isNameContinue(rest[len(keyword)]) && !strings.ContainsRune("?!/", rune(rest[len(keyword)])))) {
			return keyword, keyword
		}
	case '=', '!', '<', '>':
		if len(rest) > 1 && rest[1] == '=' {
			return rest[:2], rest[:2]
		}
		if rest[0] == '<' || rest[0] == '>' {
			return rest[:1], rest[:1]
		}
	case '+':
		return "+", "add"
	case '*':
		return "*", "mul"
	case '%':
		return "%", "mod"
	case '-', '/':
		if p.pos > 0 && len(rest) > 1 && strings.ContainsRune(" \t\r\n", rune(text[p.pos-1])) &&
			(strings.ContainsRune(" \t\r\n", rune(rest[1])) || source.ContinuationEnd(text, p.pos+1, len(text)) != p.pos+1) {
			if rest[0] == '-' {
				return "-", "sub"
			}
			return "/", "div"
		}
	}
	return "", ""
}

const comparisonPrecedence = 4

func infixPrecedence(operator string) int {
	switch operator {
	case "??":
		return 1
	case "or":
		return 2
	case "and":
		return 3
	case "==", "!=", "<", ">", "<=", ">=":
		return comparisonPrecedence
	case "+", "-":
		return 5
	case "*", "/", "%":
		return 6
	}
	return 0
}

func (p *parser) infix(minimum int) *Expr {
	start := p.pos
	return p.infixTail(p.application(), minimum, start)
}

func (p *parser) infixTail(left *Expr, minimum int, start int) *Expr {
	comparison := false
	for left != nil && len(p.diags) == 0 {
		end := p.pos
		p.operatorSpace(start)
		spelling, operation := p.binaryOperator()
		precedence := infixPrecedence(spelling)
		if precedence < minimum {
			p.pos = end
			break
		}
		opStart := p.pos
		p.pos += len(spelling)
		if comparison && precedence == comparisonPrecedence {
			p.error(opStart, p.pos, "comparisons do not chain")
			break
		}
		comparison = precedence == comparisonPrecedence
		head := p.node(Name, opStart)
		head.Text = operation
		p.skipSpace()
		next := precedence + 1
		if spelling == "??" {
			next = precedence
		}
		right := p.infixTail(p.application(), next, start)
		if right == nil {
			freeExpr(p.a, head)
			break
		}
		call := p.node(Application, left.Span.Start)
		call.Infix, call.Items = spelling, []*Expr{head, left, right}
		left = call
	}
	return left
}

func (p *parser) valuePipeTail(left *Expr) *Expr {
	var pipe *Expr
	for left != nil && len(p.diags) == 0 {
		end := p.pos
		p.operatorSpace(left.Span.Start)
		if !strings.HasPrefix(p.s.Text[p.pos:], "|>") {
			p.pos = end
			break
		}
		p.pos += 2
		p.skipSpace()
		stage := p.application()
		if stage == nil {
			break
		}
		switch stage.Kind {
		case Name, Reference, EnvironmentReference:
			p.pipePlaceholder(stage)
		case Application:
			if !stage.Juxtaposed {
				p.error(stage.Span.Start, stage.Span.End, "expected a pipe stage name or application")
			}
			for _, item := range stage.Items {
				p.pipePlaceholder(item)
			}
		default:
			p.error(stage.Span.Start, stage.Span.End, "expected a pipe stage name or application")
		}
		if pipe == nil {
			pipe = p.node(ValuePipe, left.Span.Start)
			pipe.Items = []*Expr{left}
		}
		pipe.Items = append(pipe.Items, stage)
		pipe.Span.End = p.pos
	}
	if pipe != nil {
		return pipe
	}
	return left
}

func (p *parser) pipePlaceholder(e *Expr) {
	if (e.Kind == ListSpread || e.Kind == Reference) && len(e.Items) == 1 {
		p.pipePlaceholder(e.Items[0])
		return
	}
	if e.Kind != Name || placeholderIndex(e.Text) < 0 {
		return
	}
	if e.Text != "_" {
		p.error(e.Span.Start, e.Span.End, "only '_' is valid at the top level of a pipe stage")
		return
	}
	e.Kind = Placeholder
}

func (p *parser) atom() *Expr {
	if p.pos == len(p.s.Text) {
		p.error(p.pos, p.pos, "expected expression")
		return nil
	}
	start := p.pos
	switch p.s.Text[p.pos] {
	case '(':
		return p.paren()
	case '[':
		return p.list()
	case '{':
		return p.braceRecord()
	case '"':
		return p.stringOrVerbatim()
	case '\'':
		return p.rawString()
	case ':':
		return p.symbol()
	case '@':
		if strings.HasPrefix(p.s.Text[p.pos:], "@{") {
			return p.path()
		}
		return p.selector()
	case '$':
		if actionPrefix(p.s.Text[p.pos:]) != "" {
			return p.commandCapture()
		}
	}
	if isNameStart(p.s.Text[p.pos]) {
		name := p.name()
		switch name.Text {
		case "true", "false":
			e := p.node(Boolean, start)
			e.Bool = name.Text == "true"
			return e
		case "match":
			return p.match(start)
		case "nil":
			return p.node(Nil, start)
		}
		if reservedWord(name.Text) {
			p.error(start, p.pos, "expected a value expression")
			return nil
		}
		if placeholderIndex(name.Text) >= 0 {
			e := p.node(Name, start)
			e.Text = name.Text
			return e
		}
		return p.referenceAfterName(name, true)
	}
	rest := p.s.Text[p.pos:]
	if rest[0] == '/' || strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../") || strings.HasPrefix(rest, "~/") {
		return p.path()
	}
	if p.s.Text[p.pos] == '-' || isDigit(p.s.Text[p.pos]) {
		return p.number()
	}
	p.error(start, start+1, "expected expression")
	return nil
}

func (p *parser) path() *Expr {
	e := p.pathAtom()
	p.classifyPathPattern(e, false)
	return e
}

func (p *parser) pathAtom() *Expr {
	start := p.pos
	e := p.node(Path, start)
	partStart := start
	patternDepth, pathParens := 0, 0
	classStart := -1
	for p.pos < len(p.s.Text) && source.ContinuationEnd(p.s.Text, p.pos, len(p.s.Text)) == p.pos {
		b := p.s.Text[p.pos]
		if b == '@' && strings.HasPrefix(p.s.Text[p.pos:], "@@") {
			p.pos += 2
			continue
		}
		if b == '@' && strings.HasPrefix(p.s.Text[p.pos:], "@{") {
			if patternDepth != 0 {
				p.error(p.pos, p.pos+2, "pattern groups must be fully authored; interpolation inside a group is invalid")
			}
			if partStart < p.pos {
				e.Parts = append(e.Parts, StringPart{Text: sourceText(p.a, p.s.Text[partStart:p.pos]), Span: source.Span{Start: partStart, End: p.pos}})
			}
			open := p.pos
			inner := p.interpolation()
			if inner == nil {
				break
			}
			e.Parts = append(e.Parts, StringPart{Expr: inner, Form: "@{", Span: source.Span{Start: open, End: p.pos}})
			partStart = p.pos
			if open == start && (p.pos == len(p.s.Text) || p.s.Text[p.pos] != '/') {
				p.error(start, p.pos, "a leading path expansion requires / after it")
				break
			}
			continue
		}
		if b == '\\' && p.pos+1 < len(p.s.Text) {
			p.pos += 2
			continue
		}
		if classStart >= 0 {
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
				break
			}
			if b == ']' {
				class := p.s.Text[classStart+1 : p.pos]
				if class == "" || class == "!" || !validGlobClass(class) {
					p.error(classStart, p.pos+1, "invalid glob character class")
				}
				classStart = -1
			}
			p.pos++
			continue
		}
		if b == '[' && patternDepth == 0 {
			classStart = p.pos
			p.pos++
			continue
		}
		if (p.valuePathStart == start+1 || p.kash && !p.delimitedValue) && b == ';' && patternDepth == 0 {
			break
		}
		if b == '{' {
			patternDepth++
			p.pos++
			continue
		}
		if b == '}' && patternDepth > 0 {
			patternDepth--
			p.pos++
			continue
		}
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			break
		}
		if patternDepth == 0 {
			if pathParens == 0 && (b == ',' || b == ')' || b == ']' || b == '}') {
				break
			}
			if b == '(' {
				pathParens++
			} else if b == ')' {
				pathParens--
			}
		}
		p.pos++
	}
	if classStart >= 0 {
		p.error(classStart, p.pos, "unclosed glob character class")
	}
	if len(e.Parts) != 0 && partStart < p.pos {
		e.Parts = append(e.Parts, StringPart{Text: sourceText(p.a, p.s.Text[partStart:p.pos]), Span: source.Span{Start: partStart, End: p.pos}})
	}
	e.Span.End = p.pos
	e.Text = p.s.Text[start:p.pos]
	return e
}

func (p *parser) stringOrVerbatim() *Expr {
	// A run of 3+ quotes opens a verbatim literal.
	n := 0
	for p.pos+n < len(p.s.Text) && p.s.Text[p.pos+n] == '"' {
		n++
	}
	if n >= 3 {
		return p.verbatim(n)
	}
	return p.quoted(false)
}

func (p *parser) rawString() *Expr {
	start := p.pos
	p.pos++
	end := strings.IndexByte(p.s.Text[p.pos:], '\'')
	if end < 0 {
		p.pos = len(p.s.Text)
		p.error(start, p.pos, "unclosed raw string")
		return nil
	}
	content := source.Span{Start: p.pos, End: p.pos + end}
	p.pos = content.End + 1
	e := p.node(String, start)
	e.Raw = true
	value := p.s.Text[content.Start:content.End]
	if p.indent != "" {
		value = strings.ReplaceAll(value, "\n"+p.indent, "\n")
	}
	e.Parts = []StringPart{{Text: sourceText(p.a, value), Span: content}}
	return e
}

// verbatim parses a raw multi-line string literal delimited by N>=3 quotes.
// Content is raw: no escapes, no interpolation, no directive recognition.
// Closing run must be exactly N quotes.
func (p *parser) verbatim(n int) *Expr {
	start := p.pos
	p.pos += n
	contentStart := p.pos
	text := p.s.Text
	for p.pos < len(text) {
		if text[p.pos] != '"' {
			p.pos++
			continue
		}
		run := 0
		for p.pos+run < len(text) && text[p.pos+run] == '"' {
			run++
		}
		if run == n {
			content := text[contentStart:p.pos]
			e := p.node(String, start)
			e.Verbatim, e.VerbatimLen = true, n
			if len(content) != 0 {
				var value string
				if p.indent != "" {
					value = strings.ReplaceAll(content, "\n"+p.indent, "\n")
				} else {
					value = sourceText(p.a, content)
				}
				e.Parts = append(e.Parts, StringPart{Text: value, Span: source.Span{Start: contentStart, End: p.pos}})
			}
			p.pos += n
			e.Span.End = p.pos
			return e
		}
		// A run of different length is literal content.
		p.pos += run
	}
	p.tplError(start, p.pos, "unclosed verbatim literal")
	e := p.node(String, start)
	e.Verbatim, e.VerbatimLen = true, n
	return e
}

func (p *parser) tplError(start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = append(p.diags, source.Diagnostic{Code: "TPL_PARSE", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

// classifyPathPattern assigns the role of each group in the parsed path.
func (p *parser) classifyPathPattern(e *Expr, matchArm bool) {
	if e.Text == "" || strings.IndexByte(e.Text, '{') < 0 {
		return
	}
	parts := e.Parts
	if len(parts) == 0 {
		parts = []StringPart{{Text: e.Text, Span: e.Span}}
	}
	parsed := ParsePathPatternParts(p.a, parts)
	if len(parsed.Diagnostics) != 0 {
		p.diags = append(p.diags, parsed.Diagnostics...)
		parsed.Pattern.Free(p.a)
		mem.FreeSlice(nil, parsed.Diagnostics)
		return
	}
	if !HasGroups(parsed.Pattern) {
		parsed.Pattern.Free(p.a)
		mem.FreeSlice(nil, parsed.Diagnostics)
		return
	}
	mem.FreeSlice(nil, parsed.Diagnostics)
	if parsed.Pattern.References != 0 && (matchArm || parsed.Pattern.Matchers != 0) {
		if span, ok := parsed.Pattern.promoteNamedCaptures(); !ok {
			p.error(span.Start, span.End, "positional reference is not valid in a path pattern")
			parsed.Pattern.Free(p.a)
			return
		}
	}
	e.Pattern = parsed.Pattern
	if e.Pattern.Matchers == 0 && (len(e.Parts) != 0 || strings.HasPrefix(e.Text, "/") || strings.HasPrefix(e.Text, "./") || strings.HasPrefix(e.Text, "../") || strings.HasPrefix(e.Text, "~/")) {
		p.pathReferences(e)
	}
}

// Reference-only value paths lower to ordinary path interpolation operands,
// retaining authored forms while sharing evaluation, suspension and escaping.
func (p *parser) pathReferences(e *Expr) {
	previous := e.Parts
	var parts []StringPart
	next := 0
	for i := range e.Pattern.Parts {
		part := e.Pattern.Parts[i]
		if part.Kind == PatternInterpolation {
			for next < len(previous) && previous[next].Expr == nil {
				next++
			}
			parts = append(parts, previous[next])
			next++
		} else if part.Kind == PatternReference {
			name := mem.Alloc[Expr](p.a)
			name.Kind, name.Span, name.Text = Name, part.Span, p.s.Text[part.Span.Start+1:part.Span.End-1]
			parts = append(parts, StringPart{Expr: name, Form: "{", Span: part.Span})
		} else {
			parts = append(parts, StringPart{Text: sourceText(p.a, p.s.Text[part.Span.Start:part.Span.End]), Span: part.Span})
		}
	}
	for i := range previous {
		if previous[i].Expr == nil {
			mem.FreeString(p.a, previous[i].Text)
		}
	}
	mem.FreeSlice(nil, previous)
	e.Parts = parts
	e.Pattern.Free(p.a)
	e.Pattern = nil
}

func containsRegexGroupMarker(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] != '{' {
			continue
		}
		j := i + 1
		if j < len(text) && text[j] == '~' {
			return true
		}
		for j < len(text) && isNameContinue(text[j]) {
			j++
		}
		if j+1 < len(text) && text[j] == ':' && text[j+1] == '~' {
			return true
		}
	}
	return false
}

func isNameStart(b byte) bool    { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isNameContinue(b byte) bool { return isNameStart(b) || isDigit(b) || b == '-' }
func isDigit(b byte) bool        { return b >= '0' && b <= '9' }

type nameResult struct {
	Text string
	Span source.Span
	OK   bool
}

func (p *parser) name() nameResult {
	start := p.pos
	if p.pos == len(p.s.Text) || !isNameStart(p.s.Text[p.pos]) {
		return nameResult{}
	}
	p.pos++
	for p.pos < len(p.s.Text) && isNameContinue(p.s.Text[p.pos]) {
		p.pos++
	}
	if p.pos < len(p.s.Text) && (p.s.Text[p.pos] == '?' || p.s.Text[p.pos] == '!') &&
		!strings.HasPrefix(p.s.Text[p.pos:], "??") && !strings.HasPrefix(p.s.Text[p.pos:], "!=") {
		p.pos++
	}
	return nameResult{Text: p.s.Text[start:p.pos], Span: source.Span{Start: start, End: p.pos}, OK: true}
}

func (p *parser) symbol() *Expr {
	start := p.pos
	p.pos++
	nameStart := p.pos
	if p.pos < len(p.s.Text) && (p.s.Text[p.pos] == '+' || p.s.Text[p.pos] == '-') {
		p.pos++
	}
	name := p.name()
	if !name.OK {
		p.error(start, p.pos, "expected symbol name")
		return nil
	}
	e := p.node(Symbol, start)
	e.Text = p.s.Text[nameStart:p.pos]
	return e
}

func (p *parser) selector() *Expr {
	start := p.pos
	p.pos++
	if p.pos == len(p.s.Text) {
		p.error(start, p.pos, "invalid selector")
		return nil
	}
	if p.s.Text[p.pos] == '<' || p.s.Text[p.pos] == '>' {
		p.pos++
	}
	if p.pos < len(p.s.Text) && (p.s.Text[p.pos] == '*' || p.s.Text[p.pos] == '#' || p.s.Text[p.pos] == '_' || (p.s.Text[p.pos] == '?' && p.pos == start+2 && p.s.Text[start+1] == '<')) {
		p.pos++
	} else {
		for p.pos < len(p.s.Text) && (isDigit(p.s.Text[p.pos]) || p.s.Text[p.pos] == '.' || p.s.Text[p.pos] == '-') {
			p.pos++
		}
	}
	text := p.s.Text[start:p.pos]
	if !ValidSelector(text) {
		p.error(start, p.pos, "invalid selector")
		return nil
	}
	e := p.node(Selector, start)
	e.Text = text
	return e
}

// ValidSelector reports whether text is a complete selector.
func ValidSelector(text string) bool {
	if text == "@<?" {
		return true
	}
	if len(text) < 2 || text[0] != '@' {
		return false
	}
	i := 1
	if text[i] == '<' || text[i] == '>' {
		i++
	}
	if i == len(text) {
		return true
	}
	if text[i] == '*' || text[i] == '#' || text[i] == '_' {
		return i+1 == len(text)
	}
	if text[i] == '-' {
		i++
	}
	start := i
	for i < len(text) && isDigit(text[i]) {
		i++
	}
	if i == start && !(i+1 < len(text) && text[i] == '.' && text[i+1] == '.') {
		return false
	}
	if i == len(text) {
		return true
	}
	if i+1 >= len(text) || text[i] != '.' || text[i+1] != '.' {
		return false
	}
	i += 2
	if i < len(text) && text[i] == '-' {
		i++
	}
	for i < len(text) && isDigit(text[i]) {
		i++
	}
	return i == len(text)
}

func (p *parser) number() *Expr {
	start := p.pos
	unsignedStart := start
	if p.s.Text[start] == '-' {
		unsignedStart++
	}
	digits := p.s.Text[unsignedStart:]
	basePrefixed := strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0o") || strings.HasPrefix(digits, "0b")
	for p.pos < len(p.s.Text) && !isDelimiter(p.s.Text[p.pos]) && p.s.Text[p.pos] != ',' && p.s.Text[p.pos] != '}' && p.s.Text[p.pos] != ';' && source.ContinuationEnd(p.s.Text, p.pos, len(p.s.Text)) == p.pos {
		if p.pos > start {
			b := p.s.Text[p.pos]
			if strings.ContainsRune("*%<>=!", rune(b)) || strings.HasPrefix(p.s.Text[p.pos:], "??") ||
				(b == '+' && (basePrefixed || !strings.ContainsRune("eE", rune(p.s.Text[p.pos-1])))) {
				break
			}
		}
		p.pos++
	}
	text := p.s.Text[start:p.pos]
	if !validNumber(text) {
		p.error(start, p.pos, "invalid number")
		return nil
	}
	if hasFloat(text) {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			p.error(start, p.pos, "numeric overflow")
			return nil
		}
		e := p.node(Float, start)
		e.Float, e.Text = value, text
		return e
	}
	negative := len(text) != 0 && text[0] == '-'
	unsigned := text
	if negative {
		unsigned = unsigned[1:]
	}
	base := 10
	if len(unsigned) > 2 && unsigned[0] == '0' && (unsigned[1] == 'b' || unsigned[1] == 'o' || unsigned[1] == 'x') {
		if unsigned[1] == 'b' {
			base = 2
		} else if unsigned[1] == 'o' {
			base = 8
		} else {
			base = 16
		}
		unsigned = unsigned[2:]
	}
	limit := uint64(9223372036854775807)
	if negative {
		limit++
	}
	parsed := parseUnsigned(unsigned, base, limit)
	if !parsed.OK {
		p.error(start, p.pos, "numeric overflow")
		return nil
	}
	e := p.node(Integer, start)
	e.Text = text
	if negative {
		if parsed.Value == uint64(9223372036854775807)+1 {
			e.Int = -9223372036854775807 - 1
		} else {
			e.Int = -int64(parsed.Value)
		}
	} else {
		e.Int = int64(parsed.Value)
	}
	return e
}

type unsignedResult struct {
	Value uint64
	OK    bool
}

func parseUnsigned(text string, base int, limit uint64) unsignedResult {
	value := uint64(0)
	for i := 0; i < len(text); i++ {
		if text[i] == '_' {
			continue
		}
		digit := uint64(text[i] - '0')
		if text[i] >= 'a' && text[i] <= 'f' {
			digit = uint64(text[i]-'a') + 10
		}
		if text[i] >= 'A' && text[i] <= 'F' {
			digit = uint64(text[i]-'A') + 10
		}
		if value > (limit-digit)/uint64(base) {
			return unsignedResult{}
		}
		value = value*uint64(base) + digit
	}
	return unsignedResult{Value: value, OK: true}
}

func isDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '(' || b == ')' || b == '[' || b == ']' || b == '"' || b == '|'
}

func validDigits(text string, base int) bool {
	if len(text) == 0 || text[0] == '_' || text[len(text)-1] == '_' {
		return false
	}
	lastUnderscore := false
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == '_' {
			if lastUnderscore {
				return false
			}
			lastUnderscore = true
			continue
		}
		lastUnderscore = false
		value := int(b - '0')
		if b >= 'a' && b <= 'f' {
			value = int(b-'a') + 10
		}
		if b >= 'A' && b <= 'F' {
			value = int(b-'A') + 10
		}
		if value < 0 || value >= base {
			return false
		}
	}
	return true
}

func hasFloat(text string) bool {
	start := 0
	if len(text) != 0 && text[0] == '-' {
		start++
	}
	if start+2 <= len(text) && text[start] == '0' && (text[start+1] == 'b' || text[start+1] == 'o' || text[start+1] == 'x') {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '.' || text[i] == 'e' || text[i] == 'E' {
			return true
		}
	}
	return false
}

func validNumber(text string) bool {
	if len(text) == 0 {
		return false
	}
	if text[0] == '-' {
		text = text[1:]
		if len(text) == 0 {
			return false
		}
	}
	if len(text) > 2 && text[0] == '0' && (text[1] == 'b' || text[1] == 'o' || text[1] == 'x') {
		base := 2
		if text[1] == 'o' {
			base = 8
		}
		if text[1] == 'x' {
			base = 16
		}
		return validDigits(text[2:], base)
	}
	if !hasFloat(text) {
		if !validDigits(text, 10) {
			return false
		}
		return len(text) == 1 || text[0] != '0'
	}
	dot, exponent := -1, -1
	for i := 0; i < len(text); i++ {
		if text[i] == '.' {
			if dot != -1 {
				return false
			}
			dot = i
		}
		if text[i] == 'e' || text[i] == 'E' {
			if exponent != -1 {
				return false
			}
			exponent = i
		}
	}
	end := len(text)
	if exponent != -1 {
		end = exponent
		i := exponent + 1
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if !validDigits(text[i:], 10) {
			return false
		}
	}
	if dot == -1 {
		return validDigits(text[:end], 10)
	}
	return dot < end && validDigits(text[:dot], 10) && validDigits(text[dot+1:end], 10)
}

func (p *parser) referenceAfterName(name nameResult, qualified bool) *Expr {
	start := name.Span.Start
	quotedNamespace := false
	var spelling string
	var environment *Expr
	if qualified && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '/' {
		namespace := name.Text
		quotedNamespace = strings.HasPrefix(p.s.Text[p.pos:], `/"`)
		p.pos++
		segmentStart := p.pos
		var segmentText string
		if quotedNamespace {
			segmentText, spelling = p.quotedReferenceKey()
			name.Text = sourceText(p.a, namespace+"/"+segmentText)
			name.Span.End = p.pos
		} else {
			segment := p.name()
			if !segment.OK {
				p.error(start, p.pos, "expected reference name after /")
			} else {
				segmentText = segment.Text
				name.Text, name.Span.End = p.s.Text[start:p.pos], p.pos
			}
		}
		if namespace == "env" && quotedNamespace && segmentText == "" {
			p.error(segmentStart, p.pos, "environment namespace requires a variable name")
		}
		if namespace == "env" && segmentText != "" {
			environment = p.node(Symbol, segmentStart)
			environment.Text = segmentText
			if quotedNamespace {
				environment.TextOwned = true
			}
		} else if quotedNamespace {
			mem.FreeString(p.a, segmentText)
		}
	}
	if environment == nil && !quotedNamespace && (p.pos == len(p.s.Text) || p.s.Text[p.pos] != '.') {
		e := p.node(Name, start)
		e.Text = name.Text
		return e
	}
	e := p.node(Reference, start)
	e.Reference = append(e.Reference, ReferencePart{Kind: ReferenceName, Text: name.Text, Spelling: spelling, Span: name.Span, Quoted: quotedNamespace})
	p.referenceFields(e)
	if environment != nil {
		e.Kind = EnvironmentReference
		e.Items = append(e.Items, environment)
	}
	return e
}

func (p *parser) referenceFields(e *Expr) {
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] == '.' {
		partStart := p.pos
		p.pos++
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '"' {
			text, spelling := p.quotedReferenceKey()
			e.Reference = append(e.Reference, ReferencePart{Kind: ReferenceName, Text: text, Spelling: spelling, Span: source.Span{Start: partStart, End: p.pos}, Quoted: true})
			continue
		}
		componentStart := p.pos
		kind := ReferenceName
		if p.pos < len(p.s.Text) && (isDigit(p.s.Text[p.pos]) || p.s.Text[p.pos] == '-') {
			kind = ReferenceIndex
			if p.s.Text[p.pos] == '-' {
				p.pos++
			}
			for p.pos < len(p.s.Text) && isDigit(p.s.Text[p.pos]) {
				p.pos++
			}
			if !validIndex(p.s.Text[componentStart:p.pos]) {
				p.error(partStart, p.pos, "expected field name or index")
				break
			}
		} else if !p.name().OK {
			p.error(partStart, p.pos+1, "expected field name, quoted key, or index")
			break
		}
		text := p.s.Text[componentStart:p.pos]
		e.Reference = append(e.Reference, ReferencePart{Kind: kind, Text: text, Span: source.Span{Start: partStart, End: p.pos}})
	}
	e.Span.End = p.pos
}

func (p *parser) quotedReferenceKey() (text, spelling string) {
	start := p.pos
	key := p.quoted(false)
	defer freeExpr(p.a, key)
	if len(key.Parts) == 1 && key.Parts[0].Expr == nil {
		part := &key.Parts[0]
		text, spelling = part.Text, part.Spelling
		part.Text, part.Spelling = "", ""
		return text, spelling
	}
	if len(key.Parts) != 0 {
		p.error(start, p.pos, "quoted reference segment must be literal text")
	}
	return "", ""
}

func validName(text string) bool {
	if len(text) == 0 || !isNameStart(text[0]) {
		return false
	}
	i := 1
	for i < len(text) && isNameContinue(text[i]) {
		i++
	}
	if i < len(text) && (text[i] == '?' || text[i] == '!') {
		i++
	}
	return i == len(text)
}

// ValidBindingName recognizes an unqualified user binding name. Contextual
// syntax such as template sep is recognized by its owning grammar.
func ValidBindingName(text string) bool {
	return validName(text) && placeholderIndex(text) < 0 && !reservedWord(text)
}

func reservedWord(text string) bool {
	switch text {
	case "def", "let", "set", "rule", "task", "service", "always", "cached", "generate",
		"if", "then", "elif", "else", "match", "for", "in", "while", "break", "continue",
		"return", "yield", "exit", "defer", "with", "stream", "import", "from", "as", "export",
		"include", "include?", "and", "or", "true", "false", "nil":
		return true
	}
	return false
}

func validIndex(text string) bool {
	if len(text) == 0 {
		return false
	}
	if text[0] == '-' {
		text = text[1:]
	}
	return validDigits(text, 10)
}

func (p *parser) braceRecord() *Expr {
	start := p.pos
	p.pos++
	e := p.node(Record, start)
	previous := p.delimitedValue
	p.delimitedValue = true
	defer func() { p.delimitedValue = previous }()
	keys := map[string]bool{}
	for p.skipSpace(); p.pos < len(p.s.Text); p.skipSpace() {
		if p.s.Text[p.pos] == '}' {
			p.pos++
			e.Span.End = p.pos
			return e
		}
		field := Field{Span: source.Span{Start: p.pos}}
		if strings.HasPrefix(p.s.Text[p.pos:], "...") {
			field.Spread = true
			p.pos += 3
			field.Span.End = p.pos
			if p.pos == len(p.s.Text) || strings.ContainsRune(" \t\r\n", rune(p.s.Text[p.pos])) {
				p.error(field.Span.Start, p.pos, "record spread requires an adjacent expression")
			} else {
				field.Value = p.valueExpression()
				if field.Value != nil && field.Value.Kind == List {
					p.error(field.Value.Span.Start, field.Value.Span.End, "record spread requires a record")
				}
			}
		} else {
			if p.s.Text[p.pos] == '"' {
				field.Key, field.Spelling = p.quotedReferenceKey()
				field.Quoted = true
			} else {
				name := p.name()
				if !name.OK || placeholderIndex(name.Text) >= 0 {
					p.error(field.Span.Start, p.pos+1, "expected record field name")
					break
				}
				field.Key = name.Text
			}
			field.Span.End = p.pos
			if keys[field.Key] {
				p.error(field.Span.Start, field.Span.End, "duplicate record field")
			}
			keys[field.Key] = true
			p.skipSpace()
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ':' {
				p.pos++
				p.skipSpace()
				field.Value = p.valueExpression()
			} else if field.Quoted {
				p.error(field.Span.Start, field.Span.End, "quoted record key needs a value")
			} else if reservedWord(field.Key) {
				p.error(field.Span.Start, field.Span.End, "record shorthand requires a value name")
			} else {
				field.Shorthand = true
				field.Value = p.node(Name, field.Span.Start)
				field.Value.Text, field.Value.Span = field.Key, field.Span
			}
		}
		e.Fields = append(e.Fields, field)
		p.skipSpace()
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '}' {
			continue
		}
		if p.pos == len(p.s.Text) || p.s.Text[p.pos] != ',' {
			break
		}
		p.pos++
	}
	p.error(start, p.pos, "unclosed record; expected ',' or '}'")
	e.Span.End = p.pos
	return e
}

func (p *parser) list() *Expr {
	start := p.pos
	p.pos++
	previous := p.delimitedValue
	p.delimitedValue = true
	defer func() { p.delimitedValue = previous }()

	list := p.node(List, start)
	p.skipSpace()
	for p.pos < len(p.s.Text) {
		if p.s.Text[p.pos] == ']' {
			p.pos++
			list.Span.End = p.pos
			return list
		}
		before := len(p.diags)
		var item *Expr
		if strings.HasPrefix(p.s.Text[p.pos:], "...") {
			item = p.spread(p.valueExpression, "list")
			if len(item.Items) != 0 && item.Items[0].Kind == Record {
				p.error(item.Items[0].Span.Start, item.Items[0].Span.End, "list spread requires a list")
			}
		} else {
			item = p.valueExpression()
		}
		if item != nil {
			list.Items = append(list.Items, item)
		}
		if item == nil || len(p.diags) != before {
			list.Span.End = p.pos
			return list
		}
		p.skipSpace()
		if p.pos == len(p.s.Text) {
			break
		}
		switch p.s.Text[p.pos] {
		case ']':
			continue
		case ',':
			p.pos++
			p.skipSpace()
		default:
			p.error(p.pos, p.pos+1, "expected ',' or ']' after list item")
			list.Span.End = p.pos
			return list
		}
	}
	p.error(start, p.pos, "unclosed list; expected ']'")
	list.Span.End = p.pos
	return list
}

// MaxSectionArity bounds synthesized placeholder parameters, including gaps.
// Sections support at most 4096 arguments (_0 through _4095).
const MaxSectionArity = 4096

// classifySection converts a single-item application containing placeholders
// into a placeholder section. Nested sections and placeholder-free bodies are
// left untouched.
func (p *parser) classifySection(app *Expr) *Expr {
	if len(app.Items) != 1 {
		return app
	}
	body := app.Items[0]
	if body == nil || body.Kind == Section || !containsPlaceholder(body) {
		return app
	}
	arity := int64(0)
	reclassifyPlaceholders(body, &arity)
	if arity > MaxSectionArity {
		p.error(body.Span.Start, body.Span.End, "placeholder section exceeds 4096 parameters")
		return app
	}
	app.Kind = Section
	items := app.Items
	app.Items = nil
	mem.FreeSlice(nil, items)
	app.Body = append(app.Body, body)
	for i := int64(0); i < arity; i++ {

		name := sourceText(p.a, "_"+strconv.FormatInt(i, 10))
		app.Parameters = append(app.Parameters, Parameter{Name: name, Span: body.Span})
	}
	return app
}

// containsPlaceholder reports whether the expression subtree contains a
// placeholder name outside nested sections.
func containsPlaceholder(e *Expr) bool {
	if e == nil {
		return false
	}
	if !sectionLevel(e) {
		return false
	}
	if e.Kind == Name && placeholderIndex(e.Text) >= 0 {
		return true
	}
	for i := range e.Items {
		if containsPlaceholder(e.Items[i]) {
			return true
		}
	}
	for i := range e.Body {
		if containsPlaceholder(e.Body[i]) {
			return true
		}
	}
	for i := range e.Fields {
		if containsPlaceholder(e.Fields[i].Value) {
			return true
		}
	}
	for i := range e.Parts {
		if e.Parts[i].Form != "{" && containsPlaceholder(e.Parts[i].Expr) {
			return true
		}
	}
	return false
}

// reclassifyPlaceholders converts placeholder names to placeholder atoms and
// records the section arity.
func reclassifyPlaceholders(e *Expr, arity *int64) {
	if e == nil || !sectionLevel(e) {
		return
	}
	if e.Kind == Name {
		if index := placeholderIndex(e.Text); index >= 0 {
			e.Kind = Placeholder
			e.Int = int64(index)
			if int64(index) >= *arity {
				*arity = int64(index) + 1
			}
			return
		}
	}
	for i := range e.Items {
		reclassifyPlaceholders(e.Items[i], arity)
	}
	for i := range e.Body {
		reclassifyPlaceholders(e.Body[i], arity)
	}
	for i := range e.Fields {
		reclassifyPlaceholders(e.Fields[i].Value, arity)
	}
	for i := range e.Parts {
		if e.Parts[i].Form != "{" {
			reclassifyPlaceholders(e.Parts[i].Expr, arity)
		}
	}
}

func sectionLevel(e *Expr) bool {
	switch e.Kind {
	case Section, Group, Lambda, List, Record, CommandCapture, CommandList, ValuePipe:
		return false
	case Application:
		return e.Juxtaposed || e.Infix != ""
	default:
		return true
	}
}

func (p *parser) checkPlaceholders(e *Expr) {
	if e == nil {
		return
	}
	if e.Kind == Name && placeholderIndex(e.Text) >= 0 {
		p.error(e.Span.Start, e.Span.End, "placeholder outside a section or value pipe stage")
	}
	for _, item := range e.Items {
		p.checkPlaceholders(item)
	}
	for _, item := range e.Body {
		p.checkPlaceholders(item)
	}
	for _, field := range e.Fields {
		p.checkPlaceholders(field.Value)
	}
	for _, part := range e.Parts {
		if part.Form != "{" {
			p.checkPlaceholders(part.Expr)
		}
	}
}

// placeholderIndex returns the positional index of a placeholder name, or -1
// when the text is an ordinary name. Underscore runs index from their length
// and underscore-prefixed canonical decimal digits index from their value.
func placeholderIndex(text string) int {
	if len(text) == 0 || text[0] != '_' {
		return -1
	}
	underscores := 0
	for underscores < len(text) && text[underscores] == '_' {
		underscores++
	}
	if underscores == len(text) {
		return min(underscores-1, MaxSectionArity)
	}
	rest := text[underscores:]
	if underscores != 1 {
		return -1
	}
	if len(rest) > 1 && rest[0] == '0' {
		return -1
	}
	for i := 0; i < len(rest); i++ {
		if !isDigit(rest[i]) {
			return -1
		}
	}
	index := 0
	for i := 0; i < len(rest); i++ {
		digit := int(rest[i] - '0')
		// Saturate only after validating all digits: oversized placeholders
		// remain placeholders and receive the section arity diagnostic.
		if index > (MaxSectionArity-digit)/10 {
			return MaxSectionArity
		}
		index = index*10 + digit
	}
	return index
}

func (p *parser) paren() *Expr {
	start := p.pos
	p.pos++
	previous := p.delimitedValue
	p.delimitedValue = true
	defer func() { p.delimitedValue = previous }()

	p.skipSpace()
	if p.pos == len(p.s.Text) {
		p.error(start, start+1, "unclosed expression group; expected a value and ')'")
		return nil
	}
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' {
		p.pos++
		p.error(start, p.pos, "empty expression group")
		return nil
	}
	body := p.valueExpression()
	p.skipSpace()
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' {
		p.pos++
	} else if len(p.diags) == 0 {
		p.error(start, p.pos, "unclosed expression group; expected ')'")
	}
	if body == nil {
		return nil
	}
	group := p.node(Group, start)
	group.Items = []*Expr{body}
	return p.classifySection(group)
}

func cloneExpr(a mem.Allocator, original *Expr) *Expr {
	if original == nil {
		return nil
	}
	copy := mem.Alloc[Expr](a)
	*copy = *original
	if original.trivia != nil {
		copy.trivia = mem.Alloc[expressionTrivia](a)
		*copy.trivia = *original.trivia
		copy.trivia.text = sourceText(a, original.trivia.text)
		copy.trivia.indent = sourceText(a, original.trivia.indent)
		copy.trivia.spans = slices.Clone(original.trivia.spans)
	}
	copy.wordSpelling = nil
	for _, text := range original.wordSpelling {
		copy.wordSpelling = append(copy.wordSpelling, sourceText(a, text))
	}
	copy.Edges = slices.Clone(original.Edges)
	copy.CommandOps = slices.Clone(original.CommandOps)
	copy.StreamKinds = slices.Clone(original.StreamKinds)
	copy.Channels = nil
	for i := range original.Channels {
		copy.Channels = append(copy.Channels, sourceText(a, original.Channels[i]))
	}
	copy.CapturePolicy = cloneExpr(a, original.CapturePolicy)
	copy.NamesOwned = true
	if original.Text != "" {
		copy.Text, copy.TextOwned = sourceText(a, original.Text), true
	}
	copy.Parts, copy.Reference, copy.Items, copy.Fields, copy.Parameters, copy.Body, copy.Pattern = nil, nil, nil, nil, nil, nil, nil
	if original.Pattern != nil {
		copy.Pattern = original.Pattern.Clone(a)
	}
	for i := range original.Parts {
		part := original.Parts[i]
		if part.Expr != nil {
			part.Expr = cloneExpr(a, part.Expr)
		} else {
			part.Text = sourceText(a, part.Text)
			part.Spelling = sourceText(a, part.Spelling)
		}
		copy.Parts = append(copy.Parts, part)
	}
	copy.Reference = slices.Clone(original.Reference)
	for i := range copy.Reference {
		copy.Reference[i].Text = sourceText(a, original.Reference[i].Text)
		copy.Reference[i].Spelling = sourceText(a, original.Reference[i].Spelling)
	}
	for i := range original.Items {
		copy.Items = append(copy.Items, cloneExpr(a, original.Items[i]))
	}
	for i := range original.Fields {
		field := original.Fields[i]
		field.Key = sourceText(a, field.Key)
		field.Spelling = sourceText(a, field.Spelling)
		field.Value = cloneExpr(a, field.Value)
		copy.Fields = append(copy.Fields, field)
	}
	copy.Parameters = slices.Clone(original.Parameters)
	// Cloned parameter names must outlive the original source, just like
	// reference components and record keys.
	if len(copy.Parameters) != 0 {
		for i := range copy.Parameters {
			copy.Parameters[i].Name = sourceText(a, copy.Parameters[i].Name)
		}
	}
	for i := range original.Body {
		copy.Body = append(copy.Body, cloneExpr(a, original.Body[i]))
	}
	return copy
}

func (p *parser) arrowLambda() *Expr {
	start := p.pos
	var parameters []Parameter
	if strings.HasPrefix(p.s.Text[p.pos:], "()") {
		p.pos += 2
		p.inlineSpace()
	} else {
		for {
			name := p.name()
			if !name.OK {
				break
			}
			rest := strings.HasPrefix(p.s.Text[p.pos:], "...")
			if rest {
				p.pos += 3
			}
			parameters = append(parameters, Parameter{Name: name.Text, Span: name.Span, Rest: rest})
			end := p.pos
			p.inlineSpace()
			if p.pos == end {
				break
			}
		}
		if len(parameters) == 0 {
			p.pos = start
			return nil
		}
	}
	if !strings.HasPrefix(p.s.Text[p.pos:], "=>") {
		p.pos = start
		return nil
	}
	p.pos += 2
	e := p.node(Lambda, start)
	e.Arrow, e.Parameters = true, parameters
	names := make(map[string]bool, len(parameters))
	for i, arg := range parameters {
		if names[arg.Name] {
			p.error(arg.Span.Start, arg.Span.End, "duplicate lambda parameter")
		}
		names[arg.Name] = true
		if arg.Rest && i != len(parameters)-1 {
			p.error(arg.Span.Start, arg.Span.End, "rest parameter must be last")
		}
		if !ValidBindingName(arg.Name) {
			p.error(arg.Span.Start, arg.Span.End, "expected a parameter name")
		}
	}
	p.skipSpace()
	if body := p.valueExpression(); body != nil {
		e.Body = []*Expr{body}
	}
	e.Span.End = p.pos
	return e
}

func (p *parser) quoted(patterns bool) *Expr {
	start := p.pos
	p.pos++
	e := p.node(String, start)
	var text strings.Builder
	text = strings.Builder{}
	partStart := p.pos
	for p.pos < len(p.s.Text) {
		b := p.s.Text[p.pos]
		if b == '"' {
			p.addTextPart(e, &text, partStart, p.pos)
			if patterns {
				p.classifyStringPattern(e, start, p.pos)
			}
			p.pos++
			e.Span.End = p.pos
			text.Reset()
			return e
		}
		if b == '\\' {
			if p.pos+1 == len(p.s.Text) {
				p.error(p.pos, p.pos+1, "unfinished escape")
				break
			}
			p.pos++
			escaped := p.s.Text[p.pos]
			if escaped == 'n' {
				text.WriteByte('\n')
			} else if escaped == 'r' {
				text.WriteByte('\r')
			} else if escaped == 't' {
				text.WriteByte('\t')
			} else if escaped == '"' || escaped == '\\' || escaped == '{' || escaped == '@' {
				text.WriteByte(escaped)
			} else {
				p.error(p.pos-1, p.pos+1, "invalid string escape")
				text.WriteByte(escaped)
			}
			p.pos++
			continue
		}
		if b == '@' && p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '@' {
			if p.pos+2 < len(p.s.Text) && p.s.Text[p.pos+2] == '{' {
				patterns = false
			}
			text.WriteByte('@')
			p.pos += 2
			continue
		}
		if b == '@' && strings.HasPrefix(p.s.Text[p.pos:], "@{") {
			p.addTextPart(e, &text, partStart, p.pos)
			open := p.pos
			before := len(p.diags)
			inner := p.interpolation()
			if inner == nil {
				text.Reset()
				e.Span.End = p.pos
				return e
			}
			e.Parts = append(e.Parts, StringPart{Expr: inner, Form: "@{", Span: source.Span{Start: open, End: p.pos}})
			if p.pos == len(p.s.Text) && len(p.diags) > before {
				text.Reset()
				e.Span.End = p.pos
				return e
			}
			partStart = p.pos
			continue
		}
		text.WriteByte(b)
		p.pos++
		if b == '\n' && p.indent != "" && strings.HasPrefix(p.s.Text[p.pos:], p.indent) {
			p.pos += len(p.indent)
		}
	}
	text.Reset()
	p.error(start, p.pos, "unclosed string")
	return e
}

func (p *parser) interpolation() *Expr {
	open := p.pos
	if !strings.HasPrefix(p.s.Text[open:], "@{") {
		p.error(open, open+1, "expected @{expression}")
		return nil
	}
	previous := p.delimitedValue
	p.delimitedValue = true
	defer func() { p.delimitedValue = previous }()
	p.pos += 2
	p.skipSpace()
	diagnosticStart := len(p.diags)
	inner := p.valueExpression()
	p.skipSpace()
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] != '}' {
		p.error(p.pos, p.pos+1, "expected '}' after interpolation expression")
		for p.pos < len(p.s.Text) && p.s.Text[p.pos] != '}' {
			before := p.pos
			extra := p.expression()
			freeExpr(p.a, extra)
			if p.pos == before {
				p.pos++
			}
			p.skipSpace()
		}
	}
	if p.pos == len(p.s.Text) {
		freeExpr(p.a, inner)
		p.diags = p.diags[:diagnosticStart]
		p.error(open, p.pos, "unclosed reference expansion; expected '}'")
		return nil
	}
	p.pos++
	if inner != nil {
		inner.Expansion = "@{"
	}
	return inner
}

func (p *parser) addTextPart(e *Expr, text *strings.Builder, start int, end int) {
	if text.Len() == 0 {
		return
	}
	part := StringPart{Text: sourceText(p.a, text.String()), Span: source.Span{Start: start, End: end}}
	if e.Kind == String {
		authored := p.s.Text[start:end]
		if p.indent != "" {
			authored = strings.ReplaceAll(authored, "\n"+p.indent, "\n")
		}
		if authored != part.Text || strings.ContainsAny(authored, "@\n\r\t") {
			part.Spelling = sourceText(p.a, authored)
		}
	}
	e.Parts = append(e.Parts, part)
	text.Reset()
}

// classifyStringPattern recognizes groups in a quoted match arm.
func (p *parser) classifyStringPattern(e *Expr, start int, end int) {
	// Parse the string's decoded value, not its source spelling: quoted-string
	// escapes (for example, `\n`) have already been interpreted into Parts.
	var decoded strings.Builder
	decoded = strings.Builder{}
	for i := range e.Parts {
		if e.Parts[i].Expr != nil {
			decoded.Reset()
			return
		}
		decoded.WriteString(e.Parts[i].Text)
	}
	text := sourceText(p.a, decoded.String())
	decoded.Reset()
	defer mem.FreeString(p.a, text)
	if text == "" || strings.IndexByte(text, '{') < 0 {
		return
	}
	// Keep pattern-part spans anchored to the source even when an escape before
	// a group makes source and decoded-string offsets differ.
	var sourceStarts, sourceEnds []int
	for pos := start + 1; pos < end; {
		from := pos
		if pos+1 < end && (p.s.Text[pos] == '\\' || (p.s.Text[pos] == '@' && p.s.Text[pos+1] == '@')) {
			pos += 2
		} else {
			pos++
		}
		sourceStarts = append(sourceStarts, from)
		sourceEnds = append(sourceEnds, pos)
		if p.s.Text[from] == '\n' && p.indent != "" && strings.HasPrefix(p.s.Text[pos:], p.indent) {
			pos += len(p.indent)
		}
	}
	parsed := ParsePatternText(p.a, text, 0)
	for i := range parsed.Pattern.Parts {
		part := &parsed.Pattern.Parts[i]
		if part.Span.Start >= 0 && part.Span.End > part.Span.Start && part.Span.End <= len(sourceStarts) {
			part.Span = source.Span{Start: sourceStarts[part.Span.Start], End: sourceEnds[part.Span.End-1]}
		}
	}
	for i := range parsed.Diagnostics {
		span := parsed.Diagnostics[i].Span
		if span.Start >= 0 && span.Start < len(sourceStarts) {
			startAt := sourceStarts[span.Start]
			endAt := startAt
			if span.End > span.Start && span.End <= len(sourceEnds) {
				endAt = sourceEnds[span.End-1]
			}
			parsed.Diagnostics[i].Span = source.Span{Start: startAt, End: endAt}
		}
	}
	mem.FreeSlice(nil, sourceStarts)
	mem.FreeSlice(nil, sourceEnds)
	if len(parsed.Diagnostics) != 0 {
		if containsRegexGroupMarker(text) {
			for i := range parsed.Diagnostics {
				p.diags = append(p.diags, parsed.Diagnostics[i])
			}
		}
		parsed.Pattern.Free(p.a)
		mem.FreeSlice(nil, parsed.Diagnostics)
		return
	}
	if !HasGroups(parsed.Pattern) {
		parsed.Pattern.Free(p.a)
		mem.FreeSlice(nil, parsed.Diagnostics)
		return
	}
	mem.FreeSlice(nil, parsed.Diagnostics)
	if parsed.Pattern.References != 0 {
		if span, ok := parsed.Pattern.promoteNamedCaptures(); !ok {
			p.error(span.Start, span.End, "positional reference is not valid in a match pattern")
			parsed.Pattern.Free(p.a)
			return
		}
	}
	e.TextOwned = true
	e.Text = CanonicalPattern(p.a, parsed.Pattern)
	e.Pattern = parsed.Pattern
}

func sourceText(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// Format returns allocator-owned canonical expression text.
func Format(a mem.Allocator, e *Expr) string {
	return FormatAt(a, e, 0)
}

// Compact preserves the surrounding grammar of templates and process words.
func Compact(a mem.Allocator, e *Expr) string {
	b := strings.Builder{}
	writeExpr(&b, e)
	text := sourceText(a, restoreTrivia(e, b.String()))
	b.Reset()
	return text
}

// writeMuxHead writes an operator's name and leading arguments. The compact
// and expanded process writers share it so an operator's spelling cannot drift
// between layouts.
func writeMuxHead(b *strings.Builder, mux *Expr) {
	b.WriteString(mux.Text)
	if mux.Text == "tee" && mux.Bool {
		b.WriteString(" :append")
	}
	for j := range mux.Items {
		b.WriteByte(' ')
		writeExpr(b, mux.Items[j])
	}
}

// joinsWithSpace reports whether items[i] continues the previous operator
// instead of starting a piped stage. A fan-out operator's destinations and an
// implicit-stage command are not pipes: only an authored `|` is.
func joinsWithSpace(items []*Expr, i int) bool {
	if i == 0 {
		return false
	}
	if items[i].Kind == CommandWriter {
		return true
	}
	if items[i-1].Kind != CommandMux {
		return false
	}
	return streamOperatorFanOut(items[i-1].Text) || streamOperatorImplicitStage(items[i-1].Text)
}

// writeWriterAlias emits a stderr alias authored after `>&channel`. The alias
// belongs to the writer node, so it is written after that node rather than with
// the stage whose items hold it.
func writeWriterAlias(b *strings.Builder, items []*Expr, i int) {
	if i == 0 || items[i].Kind != CommandWriter || items[i-1].Kind != CommandStage {
		return
	}
	for j := range items[i-1].Items {
		item := items[i-1].Items[j]
		if item.Kind == CommandRedirection && item.Text == "2>&1" && item.Bool {
			b.WriteString(" 2>&1")
		}
	}
}

func writeProcess(b *strings.Builder, e *Expr) {
	for i := range e.Items {
		if e.Items[i].Kind == CommandInput {
			continue
		}
		if e.Items[i].Kind == CommandMux {
			mux := e.Items[i]
			b.WriteString(" |(")
			writeMuxHead(b, mux)
			if mux.Text == "filter" && len(mux.Body) != 0 {
				if len(mux.Body[0].Items) == 0 || mux.Body[0].Items[0].Kind != CommandInput {
					b.WriteString(" | ")
				}
				writeProcess(b, mux.Body[0])
			}
			b.WriteByte(')')
			continue
		}
		if i != 0 {
			if joinsWithSpace(e.Items, i) {
				b.WriteByte(' ')
			} else {
				b.WriteString(" | ")
			}
		}
		writeExpr(b, e.Items[i])
		writeWriterAlias(b, e.Items, i)
	}
	if e.AcceptExit {
		b.WriteString(" ?")
	}
	if len(e.Body) != 0 {
		b.WriteString(" ? ")
		writeProcess(b, e.Body[0])
	}
	if e.Async {
		b.WriteString(" &")
	}
	if e.Exit == "next" {
		b.WriteString(" >&")
	}
	if e.Exit == "otherwise" {
		b.WriteString(" >@")
	}
}

func writeCommandList(b *strings.Builder, e *Expr) {
	if e.CommandListCapture {
		writeActionPrefix(b, e)
	}
	for i := range e.Items {
		if i != 0 {
			operator := e.CommandOps[i-1]
			b.WriteByte(' ')
			b.WriteString(operator.Text)
			if operator.AfterNewline {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
		writeProcess(b, e.Items[i])
	}
	if e.CommandListCapture {
		b.WriteByte(')')
	}
}

func writeActionPrefix(b *strings.Builder, e *Expr) {
	if e.ActionPrefix != "" {
		b.WriteString(e.ActionPrefix)
	} else {
		b.WriteString("$(")
	}
}

func writeExpr(b *strings.Builder, e *Expr) {
	if e == nil {
		return
	}
	if e.Kind == Match {
		line := b.String()
		line = line[strings.LastIndexByte(line, '\n')+1:]
		prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		writeMatch(nil, b, e, Columns(line, 0), Columns(prefix, 0))
		return
	}
	if e.Kind == KashBreak || e.Kind == KashContinue {
		if e.Kind == KashBreak {
			b.WriteString("break")
		} else {
			b.WriteString("continue")
		}
		return
	}
	if e.Kind == ListSpread {
		b.WriteString("...")
		if len(e.Items) != 0 {
			writeExpr(b, e.Items[0])
		}
		return
	}
	if e.Kind == ValueReturn {
		b.WriteString("return")
		if len(e.Items) != 0 {
			b.WriteByte(' ')
			writeExpr(b, e.Items[0])
		}
		return
	}
	if e.Kind == KashExit {
		b.WriteString("exit")
		if len(e.Items) != 0 {
			b.WriteByte(' ')
			writeKashValue(b, e.Items[0])
		}
		return
	}
	if e.Kind == KashYield {
		b.WriteString("yield ")
		writeExpr(b, e.Items[0])
		return
	}
	if e.Kind == KashSet {
		b.WriteString("set ")
		b.WriteString(e.Text)
		b.WriteString(" = ")
		writeExpr(b, e.Items[0])
		return
	}
	if e.Kind == KashLet {
		b.WriteString("let ")
		b.WriteString(e.Text)
		b.WriteString(" = ")
		writeExpr(b, e.Items[0])
		return
	}
	if e.Kind == ValuePipe {
		for i, item := range e.Items {
			if i != 0 {
				b.WriteString(" |> ")
			}
			writeExpr(b, item)
		}
		return
	}
	if e.Kind == ValueRecovery {
		writeExpr(b, e.Items[0])
		b.WriteString(" ?? ")
		writeExpr(b, e.Items[1])
		return
	}
	if e.Kind == KashDefer {
		b.WriteString("defer ")
		writeExpr(b, e.Items[0])
		return
	}
	if e.Kind == CommandList {
		writeCommandList(b, e)
		return
	}
	if e.Kind == CommandCapture || e.Kind == CommandGraph || e.Kind == CommandTest {
		if e.Kind == CommandCapture {
			writeActionPrefix(b, e)
		}
		writeProcess(b, e)
		if e.Kind == CommandCapture {
			b.WriteByte(')')
		}
		return
	}
	if e.Kind == CommandStage {
		first := true
		for i := range e.Items {
			item := e.Items[i]
			// An alias that follows a separate channel writer is emitted with
			// that writer, not inline with the stage it routes.
			if item.Kind == CommandRedirection && item.Text == "2>&1" && item.Bool {
				continue
			}
			if !first {
				b.WriteByte(' ')
			}
			first = false
			writeExpr(b, item)
		}
		return
	}
	if e.Kind == CommandStream {
		b.WriteString("stream ")
		b.WriteString(e.Text)
		return
	}
	if e.Kind == CommandReader {
		b.WriteByte('&')
		b.WriteString(e.Text)
		return
	}
	if e.Kind == CommandWriter {
		b.WriteString(">&")
		b.WriteString(e.Text)
		return
	}
	if e.Kind == CommandOperand {
		if len(e.Body) != 0 {
			b.WriteString("<(")
			writeProcess(b, e.Body[0])
			b.WriteByte(')')
		} else {
			b.WriteString("<&")
			b.WriteString(e.Text)
		}
		return
	}
	if e.Kind == CommandGroup {
		b.WriteByte('(')
		for i := range e.Items {
			if i != 0 {
				b.WriteString(", ")
			}
			writeProcess(b, e.Items[i])
		}
		b.WriteByte(')')
		return
	}
	if e.Kind == CommandRedirection {
		b.WriteString(e.Text)
		if e.Text == "2>&1" {
			return
		}
		b.WriteByte(' ')
		writeCommandWord(b, e.Items[0])
		return
	}
	if e.Kind == CommandSetup {
		b.WriteByte(':')
		b.WriteString(e.Text)
		b.WriteByte(' ')
		writeCommandWord(b, e.Items[0])
		return
	}
	if e.Kind == CommandWord {
		writeCommandWord(b, e)
		return
	}
	if e.Kind == Boolean {
		b.WriteString(strconv.FormatBool(e.Bool))
		return
	}
	if e.Kind == Nil {
		b.WriteString("nil")
		return
	}
	if e.Kind == Integer {
		if e.Text != "" {
			b.WriteString(e.Text)
		} else {
			b.WriteString(strconv.FormatInt(e.Int, 10))
		}
		return
	}
	if e.Kind == Float {
		if e.Text != "" {
			b.WriteString(e.Text)
			return
		}
		text := strconv.FormatFloat(e.Float, 'g', -1, 64)
		b.WriteString(text)
		if !strings.ContainsAny(text, ".eE") {
			b.WriteString(".0")
		}
		return
	}
	if e.Kind == Symbol {
		b.WriteByte(':')
		b.WriteString(e.Text)
		return
	}
	if e.Kind == Name {
		b.WriteString(e.Text)
		if e.Rest {
			b.WriteString("...")
		}
		return
	}
	if e.Kind == Placeholder {
		if e.Text != "" {
			b.WriteString(e.Text)
			return
		}
		b.WriteByte('_')
		b.WriteString(strconv.FormatInt(e.Int, 10))
		return
	}
	if e.Kind == Section {
		b.WriteByte('(')
		writeExpr(b, e.Body[0])
		b.WriteByte(')')
		return
	}
	if e.Kind == Path {
		if len(e.Parts) == 0 {
			b.WriteString(e.Text)
			return
		}
		for i := range e.Parts {
			part := e.Parts[i]
			if part.Expr == nil {
				b.WriteString(part.Text)
			} else {
				writeExpansion(b, part)
			}
		}
		return
	}
	if e.Kind == Selector {
		b.WriteString(e.Text)
		return
	}
	if e.Kind == Reference || e.Kind == EnvironmentReference {
		writeReference(b, e)
		return
	}
	if e.Kind == String {
		writeString(b, e)
		return
	}
	if e.Kind == List {
		b.WriteByte('[')
		for i, item := range e.Items {
			if i != 0 {
				b.WriteString(", ")
			}
			writeExpr(b, item)
		}
		b.WriteByte(']')
		return
	}
	if e.Kind == Group {
		writeMany(b, '(', ')', e.Items)
		return
	}
	if e.Kind == Conditional {
		for i, part := range e.Items {
			switch {
			case i == 0:
				b.WriteString("if ")
			case i == len(e.Items)-1 && i%2 == 0:
				b.WriteString(" else ")
			case i%2 == 0:
				b.WriteString(" elif ")
			default:
				b.WriteString(" then ")
			}
			writeExpr(b, part)
		}
		return
	}
	if e.Kind == Application {
		if e.Infix != "" {
			writeExpr(b, e.Items[1])
			b.WriteByte(' ')
			b.WriteString(e.Infix)
			b.WriteByte(' ')
			writeExpr(b, e.Items[2])
			return
		}
		if e.Juxtaposed {
			for i, item := range e.Items {
				if i != 0 {
					b.WriteByte(' ')
				}
				writeExpr(b, item)
			}
			if len(e.Items) == 1 {
				b.WriteString(" ()")
			}
			return
		}
		writeMany(b, '(', ')', e.Items)
		return
	}
	if e.Kind == Record {
		b.WriteByte('{')
		for i, field := range e.Fields {
			if i != 0 {
				b.WriteString(", ")
			}
			writeBraceField(b, field)
		}
		b.WriteByte('}')
		return
	}
	if e.Kind == Lambda {
		if e.Arrow {
			writeLambdaParameters(b, e.Parameters)
			b.WriteString(" => ")
			if len(e.Body) != 0 {
				writeExpr(b, e.Body[0])
			}
			return
		}
		b.WriteString("([")
		for i := range e.Parameters {
			if i != 0 {
				b.WriteByte(' ')
			}
			b.WriteString(e.Parameters[i].Name)
			if e.Parameters[i].Rest {
				b.WriteString("...")
			}
		}
		b.WriteByte(']')
		for i := range e.Body {
			b.WriteByte(' ')
			writeExpr(b, e.Body[i])
		}
		b.WriteByte(')')
	}
}

func writeLambdaParameters(b *strings.Builder, parameters []Parameter) {
	if len(parameters) == 0 {
		b.WriteString("()")
		return
	}
	for i, param := range parameters {
		if i != 0 {
			b.WriteByte(' ')
		}
		b.WriteString(param.Name)
		if param.Rest {
			b.WriteString("...")
		}
	}
}

func writeBraceField(b *strings.Builder, field Field) {
	if field.Spread {
		b.WriteString("...")
	} else {
		writeBraceKey(b, field)
		if field.Shorthand {
			return
		}
		b.WriteString(": ")
	}
	writeExpr(b, field.Value)
}

func writeBraceKey(b *strings.Builder, field Field) {
	if field.Quoted {
		writeString(b, &Expr{Parts: []StringPart{{Text: field.Key, Spelling: field.Spelling}}})
	} else {
		b.WriteString(field.Key)
	}
}

func writeMany(b *strings.Builder, open byte, close byte, items []*Expr) {
	b.WriteByte(open)
	for i := range items {
		if i != 0 {
			b.WriteByte(' ')
		}
		writeExpr(b, items[i])
	}
	b.WriteByte(close)
}

func writeReference(b *strings.Builder, e *Expr) {
	hasBase := e.Kind == Reference && len(e.Items) != 0
	if hasBase {
		writeExpr(b, e.Items[0])
	}
	for i := range e.Reference {
		part := e.Reference[i]
		if i != 0 || hasBase {
			b.WriteByte('.')
		}
		if part.Quoted {
			text := part.Text
			if i == 0 && !hasBase {
				namespace, key, _ := strings.Cut(text, "/")
				b.WriteString(namespace)
				b.WriteByte('/')
				text = key
			}
			writeString(b, &Expr{Parts: []StringPart{{Text: text, Spelling: part.Spelling}}})
		} else {
			b.WriteString(part.Text)
		}
	}
}

func writeString(b *strings.Builder, e *Expr) {
	if e.Raw {
		b.WriteByte('\'')
		for _, part := range e.Parts {
			b.WriteString(part.Text)
		}
		b.WriteByte('\'')
		return
	}
	if e.Verbatim {
		n := e.VerbatimLen
		if n < 3 {
			n = 3
		}
		for i := 0; i < n; i++ {
			b.WriteByte('"')
		}
		for i := range e.Parts {
			// Verbatim parts are always literal text.
			b.WriteString(e.Parts[i].Text)
		}
		for i := 0; i < n; i++ {
			b.WriteByte('"')
		}
		return
	}
	b.WriteByte('"')
	if e.Pattern != nil && len(e.Parts) == 0 {
		for j := 0; j < len(e.Text); j++ {
			c := e.Text[j]
			if c == '"' || c == '\\' {
				b.WriteByte('\\')
				b.WriteByte(c)
			} else if c == '\n' {
				b.WriteString("\\n")
			} else if c == '\r' {
				b.WriteString("\\r")
			} else if c == '\t' {
				b.WriteString("\\t")
			} else if c == '@' && j+1 < len(e.Text) && e.Text[j+1] == '{' {
				b.WriteString("\\@")
			} else if c == '@' {
				b.WriteString("@@")
			} else {
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
		return
	}
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr != nil {
			writeExpansion(b, part)
			continue
		}
		if part.Spelling != "" {
			b.WriteString(part.Spelling)
			continue
		}
		for j := 0; j < len(part.Text); j++ {
			c := part.Text[j]
			if c == '"' {
				b.WriteString("\\\"")
			} else if c == '\\' {
				b.WriteString("\\\\")
			} else if c == '\n' {
				b.WriteString("\\n")
			} else if c == '\r' {
				b.WriteString("\\r")
			} else if c == '\t' {
				b.WriteString("\\t")
			} else if c == '@' {
				b.WriteString("@@")
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

func writeExpansion(b *strings.Builder, part StringPart) {
	if part.Form == "{" {
		b.WriteByte('{')
		writeExpr(b, part.Expr)
		b.WriteByte('}')
		return
	}
	if part.Form == "@{" {
		b.WriteString("@{")
		writeExpr(b, part.Expr)
		b.WriteByte('}')
		return
	}
	b.WriteByte('@')
	writeExpr(b, part.Expr)
}
