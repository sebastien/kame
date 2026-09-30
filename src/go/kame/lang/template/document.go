// Package template parses Kame string, target, and document templates.
package template

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Document is a lowered document template: Root evaluates to the rendered
// string. Diagnostics use TPL_* codes with spans in Source.
type Document struct {
	Alloc       mem.Allocator
	Source      *source.Source
	OwnSource   bool
	Root        *expr.Expr
	Diagnostics []source.Diagnostic
}

// Free releases a document parsed with ParseDocument.
func (d *Document) Free() {
	if d == nil {
		return
	}
	expr.Free(d.Alloc, d.Root)
	slices.Free(d.Alloc, d.Diagnostics)
	if d.OwnSource {
		d.Source.Free(d.Alloc)
	}
	a := d.Alloc
	*d = Document{}
	mem.Free(a, d)
}

// NormalizeStyle lowercases and validates a document comment style.
func NormalizeStyle(style string) (string, bool) {
	norm := normStyle(style)
	if norm == "" {
		return "", false
	}
	return norm, true
}

func normStyle(style string) string {
	if eqFold(style, "html") {
		return "html"
	}
	if eqFold(style, "c") {
		return "c"
	}
	if eqFold(style, "hash") {
		return "hash"
	}
	if eqFold(style, "dash") {
		return "dash"
	}
	if eqFold(style, "semi") {
		return "semi"
	}
	if eqFold(style, "percent") {
		return "percent"
	}
	if eqFold(style, "plain") {
		return "plain"
	}
	if eqFold(style, "none") {
		return "none"
	}
	return ""
}

func eqFold(s string, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != lower[i] {
			return false
		}
	}
	return true
}

// ParseDocument parses text as a document template in the given style and
// lowers it to an expression evaluating to the rendered string.
// Style is matched case-insensitively; unknown styles yield TPL_STYLE.
// Empty style defaults to plain.
func ParseDocument(a mem.Allocator, name string, text string, style string) *Document {
	norm := normStyle(style)
	if style != "" && norm == "" {
		s := source.New(a, name, text)
		d := mem.Alloc[Document](a)
		d.Alloc, d.Source, d.OwnSource = a, s, true
		d.Root = newCat(a, nil, source.Span{Start: 0, End: 0})
		d.Diagnostics = slices.Append(a, d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: 0, End: 0}, Message: "unknown comment style"})
		return d
	}
	if norm == "" {
		norm = "plain"
	}
	s := source.New(a, name, text)
	p := docParser{a: a, s: s, style: norm}
	root := p.parseRange(0, len(s.Text))
	d := mem.Alloc[Document](a)
	d.Alloc, d.Source, d.OwnSource, d.Root, d.Diagnostics = a, s, true, root, p.diags
	return d
}

// ParseDocumentRange parses a document template within a caller-owned Source.
func ParseDocumentRange(a mem.Allocator, s *source.Source, start int, end int, style string) *Document {
	norm := normStyle(style)
	if style != "" && norm == "" {
		d := mem.Alloc[Document](a)
		d.Alloc = a
		d.Root = newCat(a, nil, source.Span{Start: start, End: start})
		d.Diagnostics = slices.Append(a, d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: start, End: start}, Message: "unknown comment style"})
		return d
	}
	if norm == "" {
		norm = "plain"
	}
	p := docParser{a: a, s: s, style: norm}
	root := p.parseRange(start, end)
	d := mem.Alloc[Document](a)
	d.Alloc, d.Root, d.Diagnostics = a, root, p.diags
	return d
}

const (
	dirNone = iota
	dirIf
	dirElif
	dirElse
	dirFor
	dirWith
	dirLet
	dirInclude
	dirRaw
	dirEnd
	dirEscape
	dirError
)

type docParser struct {
	a     mem.Allocator
	s     *source.Source
	style string
	diags []source.Diagnostic
}

type dirOut struct {
	Kind       int
	Args       []*expr.Expr
	Span       source.Span
	EscapeRest source.Span
}

func (p *docParser) diag(code string, span source.Span, msg string) {
	if span.End > len(p.s.Text) {
		span.End = len(p.s.Text)
	}
	if span.Start > span.End {
		span.Start = span.End
	}
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: code, Severity: source.Error, Span: span, Message: msg})
}

func (p *docParser) parseRange(start int, end int) *expr.Expr {
	out := p.parseBlock(start, end)
	items := out.Items
	if out.Term != dirNone {
		p.diag("TPL_BLOCK", out.TermSpan, "stray template directive")
		freeArgs(p.a, out.TermArgs)
	} else {
		slices.Free(p.a, out.TermArgs)
	}
	root := newCat(p.a, items, source.Span{Start: start, End: end})
	return root
}

type blockOut struct {
	Items    []*expr.Expr
	Next     int
	Term     int
	TermSpan source.Span
	TermArgs []*expr.Expr
	TermEnd  int
}

type lineOut struct {
	Start      int
	ContentEnd int
	LineEnd    int
}

type trimOut struct {
	Start int
	End   int
}

type argsRes struct {
	Args []*expr.Expr
	End  int
	OK   bool
}

type ifRes struct {
	Expr *expr.Expr
	Next int
	End  int
}

type pairRes struct {
	Expr *expr.Expr
	Next int
}

func (p *docParser) parseBlock(pos int, end int) blockOut {
	var items []*expr.Expr
	cur := pos
	for cur < end {
		ln := scanLine(p.s.Text, cur, end)
		lineStart, contentEnd, lineEnd := ln.Start, ln.ContentEnd, ln.LineEnd
		d := p.directiveOnLine(lineStart, contentEnd)
		if d.Kind == dirNone {
			// Body text line: parse inline expansions + preserve ending.
			parts := p.inlineParts(lineStart, contentEnd, lineEnd)
			for i := range parts {
				items = slices.Append(p.a, items, parts[i])
			}
			slices.Free(p.a, parts)
			cur = lineEnd
			continue
		}
		if d.Kind == dirError {
			// Malformed: diagnostic already pushed. Treat line as text to
			// continue parsing and surface further errors.
			parts := p.inlineParts(lineStart, contentEnd, lineEnd)
			for i := range parts {
				items = slices.Append(p.a, items, parts[i])
			}
			slices.Free(p.a, parts)
			cur = lineEnd
			continue
		}
		if d.Kind == dirEscape {
			// "@@" at directive position: emit "@" + rest + ending.
			restText := ""
			if d.EscapeRest.End > d.EscapeRest.Start {
				restText = p.s.Text[d.EscapeRest.Start:d.EscapeRest.End]
			}
			ending := ""
			if lineEnd > contentEnd {
				ending = p.s.Text[contentEnd:lineEnd]
			}
			combined := "@" + restText + ending
			items = slices.Append(p.a, items, newStringLit(p.a, combined, source.Span{Start: lineStart, End: lineEnd}))
			cur = lineEnd
			continue
		}
		if d.Kind == dirElif || d.Kind == dirElse || d.Kind == dirEnd {
			// Terminator for caller. Free nothing; caller owns TermArgs.
			return blockOut{Items: items, Next: lineStart, Term: d.Kind, TermSpan: d.Span, TermArgs: d.Args, TermEnd: lineEnd}
		}
		if d.Kind == dirLet {
			// (let [n e] rest): remainder of this block is the body.
			rest := p.parseBlock(lineEnd, end)
			restCat := newCat(p.a, rest.Items, source.Span{Start: lineEnd, End: rest.Next})
			nameExpr := d.Args[0]
			valExpr := d.Args[1]
			slices.Free(p.a, d.Args)
			bind := mem.Alloc[expr.Expr](p.a)
			bind.Kind = expr.List
			bind.Span = d.Span
			bind.Items = slices.Append(p.a, bind.Items, nameExpr)
			bind.Items = slices.Append(p.a, bind.Items, valExpr)
			letApp := mem.Alloc[expr.Expr](p.a)
			letApp.Kind = expr.Application
			letApp.Span = source.Span{Start: d.Span.Start, End: rest.Next}
			head := mem.Alloc[expr.Expr](p.a)
			head.Kind = expr.Name
			head.Span = d.Span
			head.Text = "let"
			letApp.Items = slices.Append(p.a, letApp.Items, head)
			letApp.Items = slices.Append(p.a, letApp.Items, bind)
			letApp.Items = slices.Append(p.a, letApp.Items, restCat)
			items = slices.Append(p.a, items, letApp)
			// @let consumes the remainder; propagate terminator outward.
			freeArgs(p.a, rest.TermArgs)
			return blockOut{Items: items, Next: rest.Next, Term: rest.Term, TermSpan: rest.TermSpan, TermEnd: rest.TermEnd}
		}
		if d.Kind == dirInclude {
			inc := mem.Alloc[expr.Expr](p.a)
			inc.Kind = expr.Application
			inc.Span = d.Span
			head := mem.Alloc[expr.Expr](p.a)
			head.Kind = expr.Name
			head.Span = d.Span
			head.Text = "render"
			inc.Items = slices.Append(p.a, inc.Items, head)
			for i := range d.Args {
				inc.Items = slices.Append(p.a, inc.Items, d.Args[i])
			}
			slices.Free(p.a, d.Args)
			items = slices.Append(p.a, items, inc)
			cur = lineEnd
			continue
		}
		if d.Kind == dirIf {
			r := p.parseIf(d, lineEnd, end)
			items = slices.Append(p.a, items, r.Expr)
			cur = r.Next
			continue
		}
		if d.Kind == dirFor {
			r := p.parseFor(d, lineEnd, end)
			items = slices.Append(p.a, items, r.Expr)
			cur = r.Next
			continue
		}
		if d.Kind == dirWith {
			r := p.parseWith(d, lineEnd, end)
			items = slices.Append(p.a, items, r.Expr)
			cur = r.Next
			continue
		}
		if d.Kind == dirRaw {
			r := p.parseRaw(d, lineEnd, end)
			items = slices.Append(p.a, items, r.Expr)
			cur = r.Next
			continue
		}
		// Unknown: skip line.
		cur = lineEnd
	}
	return blockOut{Items: items, Next: end, Term: dirNone, TermEnd: end}
}

func (p *docParser) parseIf(head dirOut, bodyPos int, end int) ifRes {
	// First branch.
	first := p.parseBlock(bodyPos, end)
	firstCat := newCat(p.a, first.Items, source.Span{Start: bodyPos, End: first.Next})
	app := mem.Alloc[expr.Expr](p.a)
	app.Kind = expr.Application
	app.Span = head.Span
	ifHead := mem.Alloc[expr.Expr](p.a)
	ifHead.Kind = expr.Name
	ifHead.Span = head.Span
	ifHead.Text = "if"
	app.Items = slices.Append(p.a, app.Items, ifHead)
	app.Items = slices.Append(p.a, app.Items, head.Args[0])
	app.Items = slices.Append(p.a, app.Items, firstCat)
	slices.Free(p.a, head.Args)
	curTerm := first.Term
	curPos := first.Next
	curEnd := first.TermEnd
	curArgs := first.TermArgs
	curSpan := first.TermSpan
	// Elif chain: TermArgs holds the condition.
	for curTerm == dirElif {
		ln := scanLine(p.s.Text, curPos, end)
		branch := p.parseBlock(ln.LineEnd, end)
		branchCat := newCat(p.a, branch.Items, source.Span{Start: ln.LineEnd, End: branch.Next})
		// curArgs has exactly one condition (validated at parse).
		cond := curArgs[0]
		slices.Free(p.a, curArgs)
		app.Items = slices.Append(p.a, app.Items, cond)
		app.Items = slices.Append(p.a, app.Items, branchCat)
		app.Span.End = branch.Next
		curTerm = branch.Term
		curPos = branch.Next
		curEnd = branch.TermEnd
		curArgs = branch.TermArgs
		curSpan = branch.TermSpan
		_ = curSpan
	}
	if curTerm == dirElse {
		eln := scanLine(p.s.Text, curPos, end)
		branch := p.parseBlock(eln.LineEnd, end)
		branchCat := newCat(p.a, branch.Items, source.Span{Start: eln.LineEnd, End: branch.Next})
		app.Items = slices.Append(p.a, app.Items, branchCat)
		app.Span.End = branch.Next
		curTerm = branch.Term
		curPos = branch.Next
		curEnd = branch.TermEnd
		freeArgs(p.a, curArgs)
		curArgs = branch.TermArgs
		curSpan = branch.TermSpan
		_ = curSpan
	} else {
		freeArgs(p.a, curArgs)
	}
	if curTerm != dirEnd {
		p.diag("TPL_BLOCK", head.Span, "unterminated @if block")
		freeArgs(p.a, curArgs)
		return ifRes{Expr: app, Next: curPos, End: curEnd}
	}
	eln := scanLine(p.s.Text, curPos, end)
	freeArgs(p.a, curArgs)
	app.Span.End = eln.LineEnd
	return ifRes{Expr: app, Next: eln.LineEnd, End: eln.LineEnd}
}

func (p *docParser) parseFor(head dirOut, bodyPos int, end int) pairRes {
	body := p.parseBlock(bodyPos, end)
	bodyCat := newCat(p.a, body.Items, source.Span{Start: bodyPos, End: body.Next})
	freeArgs(p.a, body.TermArgs)
	if body.Term != dirEnd {
		p.diag("TPL_BLOCK", head.Span, "unterminated @for block")
	}
	nextPos := body.Next
	if body.Term == dirEnd {
		nln := scanLine(p.s.Text, body.Next, end)
		nextPos = nln.LineEnd
	}
	// ([params] bodyCat)
	lam := mem.Alloc[expr.Expr](p.a)
	lam.Kind = expr.Lambda
	lam.Span = head.Span
	params := lambdaParams(p.a, head.Args[0])
	lam.Parameters = params
	lam.Body = slices.Append(p.a, lam.Body, bodyCat)
	// (map lam xs)
	mapApp := mem.Alloc[expr.Expr](p.a)
	mapApp.Kind = expr.Application
	mapApp.Span = head.Span
	mapHead := mem.Alloc[expr.Expr](p.a)
	mapHead.Kind = expr.Name
	mapHead.Span = head.Span
	mapHead.Text = "map"
	mapApp.Items = slices.Append(p.a, mapApp.Items, mapHead)
	mapApp.Items = slices.Append(p.a, mapApp.Items, lam)
	mapApp.Items = slices.Append(p.a, mapApp.Items, head.Args[1])
	// (join mapApp sep)
	joinApp := mem.Alloc[expr.Expr](p.a)
	joinApp.Kind = expr.Application
	joinApp.Span = head.Span
	joinHead := mem.Alloc[expr.Expr](p.a)
	joinHead.Kind = expr.Name
	joinHead.Span = head.Span
	joinHead.Text = "join"
	joinApp.Items = slices.Append(p.a, joinApp.Items, joinHead)
	joinApp.Items = slices.Append(p.a, joinApp.Items, mapApp)
	if len(head.Args) == 3 {
		joinApp.Items = slices.Append(p.a, joinApp.Items, head.Args[2])
	} else {
		joinApp.Items = slices.Append(p.a, joinApp.Items, newStringLit(p.a, "", head.Span))
	}
	expr.Free(p.a, head.Args[0])
	slices.Free(p.a, head.Args)
	joinApp.Span.End = nextPos
	return pairRes{Expr: joinApp, Next: nextPos}
}

func (p *docParser) parseWith(head dirOut, bodyPos int, end int) pairRes {
	body := p.parseBlock(bodyPos, end)
	bodyCat := newCat(p.a, body.Items, source.Span{Start: bodyPos, End: body.Next})
	freeArgs(p.a, body.TermArgs)
	if body.Term != dirEnd {
		p.diag("TPL_BLOCK", head.Span, "unterminated @with block")
	}
	nextPos := body.Next
	if body.Term == dirEnd {
		nln := scanLine(p.s.Text, body.Next, end)
		nextPos = nln.LineEnd
	}
	app := mem.Alloc[expr.Expr](p.a)
	app.Kind = expr.Application
	app.Span = source.Span{Start: head.Span.Start, End: nextPos}
	headExpr := mem.Alloc[expr.Expr](p.a)
	headExpr.Kind = expr.Name
	headExpr.Span = head.Span
	headExpr.Text = "with"
	app.Items = slices.Append(p.a, app.Items, headExpr)
	app.Items = slices.Append(p.a, app.Items, head.Args[0])
	app.Items = slices.Append(p.a, app.Items, bodyCat)
	slices.Free(p.a, head.Args)
	return pairRes{Expr: app, Next: nextPos}
}

func (p *docParser) parseRaw(head dirOut, bodyPos int, end int) pairRes {
	cur := bodyPos
	for cur < end {
		ln := scanLine(p.s.Text, cur, end)
		if p.isEndLine(ln.Start, ln.ContentEnd) {
			rawText := p.s.Text[bodyPos:ln.Start]
			lit := newStringLit(p.a, rawText, source.Span{Start: bodyPos, End: ln.Start})
			return pairRes{Expr: lit, Next: ln.LineEnd}
		}
		cur = ln.LineEnd
	}
	p.diag("TPL_PARSE", head.Span, "unterminated @raw block")
	rawText := ""
	if bodyPos < end {
		rawText = p.s.Text[bodyPos:end]
	}
	lit := newStringLit(p.a, rawText, source.Span{Start: bodyPos, End: end})
	return pairRes{Expr: lit, Next: end}
}

// isEndLine reports whether the line is a valid bare @end directive in the
// current style, without emitting diagnostics. Raw regions only recognize
// @end; all other text is verbatim.
func (p *docParser) isEndLine(lineStart int, contentEnd int) bool {
	text := p.s.Text
	pos := lineStart
	for pos < contentEnd && (text[pos] == ' ' || text[pos] == '\t') {
		pos++
	}
	indentEnd := pos
	if p.style == "none" || p.style == "plain" {
		if p.style == "none" {
			return false
		}
		trimEnd := contentEnd
		for trimEnd > indentEnd && (text[trimEnd-1] == ' ' || text[trimEnd-1] == '\t') {
			trimEnd--
		}
		if trimEnd-indentEnd != 4 {
			return false
		}
		return text[indentEnd] == '@' && text[indentEnd+1] == 'e' && text[indentEnd+2] == 'n' && text[indentEnd+3] == 'd'
	}
	// Comment styles: extract inner without diagnostics.
	var innerStart, innerEnd int
	hasInner := false
	switch p.style {
	case "html":
		if !hasPrefixAt(text, indentEnd, contentEnd, "<!--") {
			return false
		}
		openEnd := indentEnd + 4
		closeStart := findSuffix(text, openEnd, contentEnd, "-->")
		if closeStart < 0 {
			return false
		}
		for i := closeStart + 3; i < contentEnd; i++ {
			if text[i] != ' ' && text[i] != '\t' {
				return false
			}
		}
		tr := trimRange(text, openEnd, closeStart)
		innerStart, innerEnd, hasInner = tr.Start, tr.End, true
	case "c":
		if hasPrefixAt(text, indentEnd, contentEnd, "/*") {
			openEnd := indentEnd + 2
			closeStart := findSuffix(text, openEnd, contentEnd, "*/")
			if closeStart < 0 {
				return false
			}
			for i := closeStart + 2; i < contentEnd; i++ {
				if text[i] != ' ' && text[i] != '\t' {
					return false
				}
			}
			tr := trimRange(text, openEnd, closeStart)
			innerStart, innerEnd, hasInner = tr.Start, tr.End, true
		} else {
			if !hasPrefixAt(text, indentEnd, contentEnd, "//") {
				return false
			}
			innerStart = indentEnd + 2
			for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
				innerStart++
			}
			innerEnd = contentEnd
			for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
				innerEnd--
			}
			hasInner = true
		}
	case "hash":
		if !hasPrefixAt(text, indentEnd, contentEnd, "#") {
			return false
		}
		innerStart = indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd = contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		hasInner = true
	case "dash":
		if !hasPrefixAt(text, indentEnd, contentEnd, "--") {
			return false
		}
		innerStart = indentEnd + 2
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd = contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		hasInner = true
	case "semi":
		if !hasPrefixAt(text, indentEnd, contentEnd, ";") {
			return false
		}
		innerStart = indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd = contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		hasInner = true
	case "percent":
		if !hasPrefixAt(text, indentEnd, contentEnd, "%") {
			return false
		}
		innerStart = indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd = contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		hasInner = true
	}
	if !hasInner {
		return false
	}
	if innerEnd-innerStart != 4 {
		return false
	}
	return text[innerStart] == '@' && text[innerStart+1] == 'e' && text[innerStart+2] == 'n' && text[innerStart+3] == 'd'
}

// scanLine splits one line: ContentEnd excludes \r\n, LineEnd includes \n.
func scanLine(text string, pos int, end int) lineOut {
	lineStart := pos
	i := pos
	for i < end && text[i] != '\n' && text[i] != '\r' {
		i++
	}
	contentEnd := i
	if i < end && text[i] == '\r' {
		i++
		if i < end && text[i] == '\n' {
			i++
		}
	} else if i < end && text[i] == '\n' {
		i++
	}
	return lineOut{Start: lineStart, ContentEnd: contentEnd, LineEnd: i}
}

func (p *docParser) directiveOnLine(lineStart int, contentEnd int) dirOut {
	text := p.s.Text
	pos := lineStart
	for pos < contentEnd && (text[pos] == ' ' || text[pos] == '\t') {
		pos++
	}
	indentEnd := pos
	if p.style == "none" {
		return dirOut{Kind: dirNone}
	}
	if p.style == "plain" {
		trimEnd := contentEnd
		for trimEnd > indentEnd && (text[trimEnd-1] == ' ' || text[trimEnd-1] == '\t') {
			trimEnd--
		}
		return p.parseInner(indentEnd, trimEnd, source.Span{Start: lineStart, End: contentEnd})
	}
	// Comment styles.
	switch p.style {
	case "html":
		if !hasPrefixAt(text, indentEnd, contentEnd, "<!--") {
			return dirOut{Kind: dirNone}
		}
		openEnd := indentEnd + 4
		closeStart := findSuffix(text, openEnd, contentEnd, "-->")
		if closeStart < 0 {
			return dirOut{Kind: dirNone}
		}
		for i := closeStart + 3; i < contentEnd; i++ {
			if text[i] != ' ' && text[i] != '\t' {
				return dirOut{Kind: dirNone}
			}
		}
		tr := trimRange(text, openEnd, closeStart)
		return p.parseInner(tr.Start, tr.End, source.Span{Start: lineStart, End: contentEnd})
	case "c":
		if hasPrefixAt(text, indentEnd, contentEnd, "/*") {
			openEnd := indentEnd + 2
			closeStart := findSuffix(text, openEnd, contentEnd, "*/")
			if closeStart < 0 {
				return dirOut{Kind: dirNone}
			}
			for i := closeStart + 2; i < contentEnd; i++ {
				if text[i] != ' ' && text[i] != '\t' {
					return dirOut{Kind: dirNone}
				}
			}
			tr2 := trimRange(text, openEnd, closeStart)
			return p.parseInner(tr2.Start, tr2.End, source.Span{Start: lineStart, End: contentEnd})
		}
		if !hasPrefixAt(text, indentEnd, contentEnd, "//") {
			return dirOut{Kind: dirNone}
		}
		innerStart := indentEnd + 2
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd := contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		return p.parseInner(innerStart, innerEnd, source.Span{Start: lineStart, End: contentEnd})
	case "hash":
		if !hasPrefixAt(text, indentEnd, contentEnd, "#") {
			return dirOut{Kind: dirNone}
		}
		innerStart := indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd := contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		return p.parseInner(innerStart, innerEnd, source.Span{Start: lineStart, End: contentEnd})
	case "dash":
		if !hasPrefixAt(text, indentEnd, contentEnd, "--") {
			return dirOut{Kind: dirNone}
		}
		innerStart := indentEnd + 2
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd := contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		return p.parseInner(innerStart, innerEnd, source.Span{Start: lineStart, End: contentEnd})
	case "semi":
		if !hasPrefixAt(text, indentEnd, contentEnd, ";") {
			return dirOut{Kind: dirNone}
		}
		innerStart := indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd := contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		return p.parseInner(innerStart, innerEnd, source.Span{Start: lineStart, End: contentEnd})
	case "percent":
		if !hasPrefixAt(text, indentEnd, contentEnd, "%") {
			return dirOut{Kind: dirNone}
		}
		innerStart := indentEnd + 1
		for innerStart < contentEnd && (text[innerStart] == ' ' || text[innerStart] == '\t') {
			innerStart++
		}
		innerEnd := contentEnd
		for innerEnd > innerStart && (text[innerEnd-1] == ' ' || text[innerEnd-1] == '\t') {
			innerEnd--
		}
		return p.parseInner(innerStart, innerEnd, source.Span{Start: lineStart, End: contentEnd})
	}
	return dirOut{Kind: dirNone}
}

func hasPrefixAt(text string, pos int, end int, prefix string) bool {
	if pos+len(prefix) > end {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if text[pos+i] != prefix[i] {
			return false
		}
	}
	return true
}

func findSuffix(text string, start int, end int, suffix string) int {
	if len(suffix) > end-start {
		return -1
	}
	// Last occurrence such that only spaces follow is checked by caller;
	// here find the last occurrence within range.
	last := -1
	for i := start; i+len(suffix) <= end; i++ {
		match := true
		for j := 0; j < len(suffix); j++ {
			if text[i+j] != suffix[j] {
				match = false
				break
			}
		}
		if match {
			last = i
		}
	}
	return last
}

func trimRange(text string, start int, end int) trimOut {
	for start < end && (text[start] == ' ' || text[start] == '\t') {
		start++
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}
	return trimOut{Start: start, End: end}
}

func isKeywordLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func keywordKind(word string) int {
	switch word {
	case "if":
		return dirIf
	case "elif":
		return dirElif
	case "else":
		return dirElse
	case "for":
		return dirFor
	case "with":
		return dirWith
	case "let":
		return dirLet
	case "include":
		return dirInclude
	case "raw":
		return dirRaw
	case "end":
		return dirEnd
	}
	return dirNone
}

func (p *docParser) parseInner(innerStart int, innerEnd int, lineSpan source.Span) dirOut {
	text := p.s.Text
	if innerStart >= innerEnd {
		return dirOut{Kind: dirNone}
	}
	if text[innerStart] != '@' {
		return dirOut{Kind: dirNone}
	}
	if innerStart+1 < innerEnd && text[innerStart+1] == '@' {
		return dirOut{Kind: dirEscape, Span: lineSpan, EscapeRest: source.Span{Start: innerStart + 2, End: innerEnd}}
	}
	// Parse keyword letters.
	kend := innerStart + 1
	for kend < innerEnd && isKeywordLetter(text[kend]) {
		kend++
	}
	if kend == innerStart+1 {
		return dirOut{Kind: dirNone}
	}
	word := text[innerStart+1 : kend]
	// Case-sensitive? Spec examples lowercase; match literally but allow
	// exact lowercase only to keep @media etc untouched (they are lowercase
	// but unknown, so dirNone anyway). Use exact match.
	kind := keywordKind(word)
	if kind == dirNone {
		return dirOut{Kind: dirNone}
	}
	span := source.Span{Start: innerStart, End: innerEnd}
	// Bare keywords.
	if kind == dirElse || kind == dirEnd || kind == dirRaw {
		if kend == innerEnd {
			return dirOut{Kind: kind, Span: lineSpan}
		}
		// "@else (" with space before paren is not a directive per spec?
		// For bare keywords, any trailing content is malformed.
		// Distinguish "@else (" (space) as non-directive to avoid false
		// positives on English? Keep strict: malformed.
		_ = span
		p.diag("TPL_PARSE", lineSpan, "malformed template directive")
		return dirOut{Kind: dirError, Span: lineSpan}
	}
	// Args keywords need "(" adjacent.
	if kend >= innerEnd || text[kend] != '(' {
		// "@if (" with space, or bare "@if": not a directive (ordinary text).
		return dirOut{Kind: dirNone}
	}
	ares := p.parseArgs(kend+1, innerEnd)
	args := ares.Args
	argsEnd := ares.End
	ok := ares.OK
	if !ok {
		p.diag("TPL_PARSE", lineSpan, "malformed template directive")
		for i := range args {
			expr.Free(p.a, args[i])
		}
		slices.Free(p.a, args)
		return dirOut{Kind: dirError, Span: lineSpan}
	}
	if argsEnd != innerEnd {
		p.diag("TPL_PARSE", lineSpan, "malformed template directive")
		for i := range args {
			expr.Free(p.a, args[i])
		}
		slices.Free(p.a, args)
		return dirOut{Kind: dirError, Span: lineSpan}
	}
	// Validate arity and shapes.
	switch kind {
	case dirIf, dirElif, dirWith:
		if len(args) != 1 {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
	case dirFor:
		if len(args) != 2 && len(args) != 3 {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
		if args[0].Kind != expr.List {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
		if !validParams(args[0]) {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
	case dirLet:
		if len(args) != 2 || args[0].Kind != expr.Name {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
	case dirInclude:
		if len(args) != 1 && len(args) != 2 {
			p.diag("TPL_PARSE", lineSpan, "malformed template directive")
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return dirOut{Kind: dirError, Span: lineSpan}
		}
	}
	return dirOut{Kind: kind, Args: args, Span: lineSpan}
}

// parseArgs parses whitespace-separated expressions starting at pos until
// the outer ")".
func (p *docParser) parseArgs(pos int, innerEnd int) argsRes {
	text := p.s.Text
	var args []*expr.Expr
	cur := pos
	for {
		for cur < innerEnd && (text[cur] == ' ' || text[cur] == '\t') {
			cur++
		}
		if cur < innerEnd && text[cur] == ')' {
			cur++
			for cur < innerEnd && (text[cur] == ' ' || text[cur] == '\t') {
				cur++
			}
			if cur != innerEnd {
				for i := range args {
					expr.Free(p.a, args[i])
				}
				slices.Free(p.a, args)
				return argsRes{End: innerEnd, OK: false}
			}
			return argsRes{Args: args, End: cur, OK: true}
		}
		if cur >= innerEnd {
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			return argsRes{End: innerEnd, OK: false}
		}
		prefix := expr.ParsePrefix(p.a, p.s, cur)
		if prefix.Expr == nil {
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			slices.Free(p.a, prefix.Diagnostics)
			return argsRes{End: innerEnd, OK: false}
		}
		if len(prefix.Diagnostics) != 0 {
			expr.Free(p.a, prefix.Expr)
			for i := range args {
				expr.Free(p.a, args[i])
			}
			slices.Free(p.a, args)
			slices.Free(p.a, prefix.Diagnostics)
			return argsRes{End: innerEnd, OK: false}
		}
		slices.Free(p.a, prefix.Diagnostics)
		args = slices.Append(p.a, args, prefix.Expr)
		cur = prefix.End
	}
}

func validParams(list *expr.Expr) bool {
	seenRest := false
	for i := range list.Items {
		it := list.Items[i]
		if it.Kind != expr.Name {
			return false
		}
		if it.Rest {
			if i != len(list.Items)-1 {
				return false
			}
			seenRest = true
		}
		for j := 0; j < i; j++ {
			if list.Items[j].Text == it.Text {
				return false
			}
		}
	}
	_ = seenRest
	return true
}

func lambdaParams(a mem.Allocator, list *expr.Expr) []expr.Parameter {
	var out []expr.Parameter
	for i := range list.Items {
		it := list.Items[i]
		rest := it.Rest
		end := it.Span.End
		if rest {
			end += 3
		}
		out = slices.Append(a, out, expr.Parameter{Name: it.Text, Span: source.Span{Start: it.Span.Start, End: end}, Rest: rest})
	}
	return out
}

// inlineParts parses body text (lineStart..contentEnd) plus its line ending
// (contentEnd..lineEnd) into lowered pieces: string literals, inline
// @(expr) values, selectors, and \@ escapes. Tool references @(x/...) are
// emitted literally (deferred); malformed @( stays literal.
func (p *docParser) inlineParts(lineStart int, contentEnd int, lineEnd int) []*expr.Expr {
	var items []*expr.Expr
	text := p.s.Text
	litStart := lineStart
	i := lineStart
	for i < contentEnd {
		b := text[i]
		if b == '\\' && i+1 < contentEnd && (text[i+1] == '@' || text[i+1] == '\\') {
			if i > litStart {
				items = slices.Append(p.a, items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
			}
			items = slices.Append(p.a, items, newStringLit(p.a, text[i+1:i+2], source.Span{Start: i, End: i + 2}))
			i += 2
			litStart = i
			continue
		}
		if b == '@' && i+1 < contentEnd && text[i+1] == '(' {
			// Tool references @(x/...) are deferred: emit literally.
			if isToolOpen(text, i+2, contentEnd) {
				toolEnd := findToolClose(text, i+2, contentEnd)
				if toolEnd > i {
					if i > litStart {
						items = slices.Append(p.a, items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
					}
					items = slices.Append(p.a, items, newStringLit(p.a, text[i:toolEnd], source.Span{Start: i, End: toolEnd}))
					i = toolEnd
					litStart = i
					continue
				}
			}
			if i > litStart {
				items = slices.Append(p.a, items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
			}
			prefix := p.inlineExpr(i + 2, contentEnd)
			if prefix.Expr == nil || !prefix.Closed {
				slices.Free(p.a, prefix.Diagnostics)
				items = slices.Append(p.a, items, newStringLit(p.a, "@", source.Span{Start: i, End: i + 1}))
				i++
				litStart = i
				continue
			}
			slices.Free(p.a, prefix.Diagnostics)
			items = slices.Append(p.a, items, prefix.Expr)
			i = prefix.End + 1
			litStart = i
			continue
		}
		if b == '@' {
			selEnd := docSelectorEnd(text, contentEnd, i)
			if selEnd > i {
				if i > litStart {
					items = slices.Append(p.a, items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
				}
				sel := mem.Alloc[expr.Expr](p.a)
				sel.Kind = expr.Selector
				sel.Span = source.Span{Start: i, End: selEnd}
				sel.Text = text[i:selEnd]
				items = slices.Append(p.a, items, sel)
				i = selEnd
				litStart = i
				continue
			}
		}
		i++
	}
	if contentEnd > litStart {
		items = slices.Append(p.a, items, newStringLit(p.a, text[litStart:contentEnd], source.Span{Start: litStart, End: contentEnd}))
	}
	if lineEnd > contentEnd {
		ending := text[contentEnd:lineEnd]
		items = slices.Append(p.a, items, newStringLit(p.a, ending, source.Span{Start: contentEnd, End: lineEnd}))
	}
	return items
}

func isToolOpen(text string, pos int, end int) bool {
	if pos+2 > end {
		return false
	}
	return text[pos] == 'x' && pos+1 < end && text[pos+1] == '/'
}

func findToolClose(text string, pos int, end int) int {
	// pos is after "@(": scan x/NAME then ")". Returns end offset after ")",
	// or 0 when not a well-formed tool reference.
	i := pos + 2
	for i < end && isDocToolNameByte(text[i]) {
		i++
	}
	if i == pos+2 || i >= end || text[i] != ')' {
		return 0
	}
	return i + 1
}

func isDocToolNameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func docSelectorEnd(text string, end int, start int) int {
	if start+1 >= end {
		// Need at least "@x"; lone "@" at end is not a selector.
		// Allow two-char selectors only when both bytes exist.
		if start+1 == end {
			return start
		}
		return start
	}
	b := text[start+1]
	if b == '*' || b == '#' || b == '_' {
		candidate := text[start : start+2]
		if expr.ValidSelector(candidate) {
			return start + 2
		}
		return start
	}
	if b != '<' && b != '>' && !isDocDigit(b) && b != '-' {
		return start
	}
	i := start + 2
	if b == '<' || b == '>' {
		if i < end && (text[i] == '*' || text[i] == '#') {
			candidate := text[start : i+1]
			if expr.ValidSelector(candidate) {
				return i + 1
			}
			return start
		}
	}
	for i < end && (isDocDigit(text[i]) || text[i] == '-' || text[i] == '.') {
		i++
	}
	candidate := text[start:i]
	if expr.ValidSelector(candidate) {
		return i
	}
	return start
}

func isDocDigit(b byte) bool { return b >= '0' && b <= '9' }

type inlineOut struct {
	Expr        *expr.Expr
	End         int
	Closed      bool
	Diagnostics []source.Diagnostic
}

func (p *docParser) inlineExpr(start int, contentEnd int) inlineOut {
	prefix := expr.ParsePrefix(p.a, p.s, start)
	if prefix.Expr == nil {
		return inlineOut{Diagnostics: prefix.Diagnostics}
	}
	items := slices.Append(p.a, []*expr.Expr(nil), prefix.Expr)
	end := prefix.End
	diags := prefix.Diagnostics
	text := p.s.Text
	for end < contentEnd && text[end] != ')' {
		if text[end] != ' ' && text[end] != '\t' && text[end] != '\r' && text[end] != '\n' {
			break
		}
		pos := end
		for pos < contentEnd && (text[pos] == ' ' || text[pos] == '\t' || text[pos] == '\r' || text[pos] == '\n') {
			pos++
		}
		if pos >= contentEnd || text[pos] == ')' {
			end = pos
			break
		}
		next := expr.ParsePrefix(p.a, p.s, pos)
		for i := range next.Diagnostics {
			diags = slices.Append(p.a, diags, next.Diagnostics[i])
		}
		slices.Free(p.a, next.Diagnostics)
		if next.Expr == nil {
			break
		}
		items = slices.Append(p.a, items, next.Expr)
		end = next.End
	}
	if len(items) == 1 {
		single := items[0]
		slices.Free(p.a, items)
		closed := end < contentEnd && text[end] == ')'
		return inlineOut{Expr: single, End: end, Closed: closed, Diagnostics: diags}
	}
	app := mem.Alloc[expr.Expr](p.a)
	app.Kind = expr.Application
	app.Span = source.Span{Start: start, End: end}
	app.Items = items
	closed := end < contentEnd && text[end] == ')'
	return inlineOut{Expr: app, End: end, Closed: closed, Diagnostics: diags}
}

func newStringLit(a mem.Allocator, text string, span source.Span) *expr.Expr {
	e := mem.Alloc[expr.Expr](a)
	e.Kind = expr.String
	e.Span = span
	if len(text) != 0 {
		partText := ""
		if len(text) != 0 {
			b := mem.AllocSlice[byte](a, len(text), len(text))
			copy(b, []byte(text))
			partText = string(b)
		}
		e.Parts = slices.Append(a, e.Parts, expr.StringPart{Text: partText, Span: span})
	}
	return e
}

func newCat(a mem.Allocator, items []*expr.Expr, span source.Span) *expr.Expr {
	e := mem.Alloc[expr.Expr](a)
	e.Kind = expr.Application
	e.Span = span
	head := mem.Alloc[expr.Expr](a)
	head.Kind = expr.Name
	head.Span = span
	head.Text = "cat"
	e.Items = slices.Append(a, e.Items, head)
	for i := range items {
		e.Items = slices.Append(a, e.Items, items[i])
	}
	slices.Free(a, items)
	return e
}

func freeArgs(a mem.Allocator, args []*expr.Expr) {
	for i := range args {
		expr.Free(a, args[i])
	}
	slices.Free(a, args)
}
