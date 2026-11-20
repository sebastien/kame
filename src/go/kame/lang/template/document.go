package template

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
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
	mem.FreeSlice(nil, d.Diagnostics)
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
	if eqFold(style, "powershell") {
		return "powershell"
	}
	if eqFold(style, "batch") {
		return "batch"
	}
	if eqFold(style, "auto") {
		return "auto"
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
		d.Diagnostics = append(d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: 0, End: 0}, Message: "unknown comment style"})
		return d
	}
	if norm == "auto" {
		var ok bool
		norm, ok = ResolveAutoStyle(name, text)
		if !ok {
			return styleErrorDocument(a, name, text, "ambiguous template comment style")
		}
	}
	if norm == "" {
		norm = "plain"
	}
	s := source.New(a, name, text)
	p := docParser{a: a, s: s, style: norm}
	d := p.documentRange(0, len(s.Text))
	d.OwnSource = true
	return d
}

// ParseDocumentRange parses a document template within a caller-owned Source.
func ParseDocumentRange(a mem.Allocator, s *source.Source, start int, end int, style string) *Document {
	norm := normStyle(style)
	if style != "" && norm == "" {
		d := mem.Alloc[Document](a)
		d.Alloc, d.Source = a, s
		d.Root = newCat(a, nil, source.Span{Start: start, End: start})
		d.Diagnostics = append(d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: start, End: start}, Message: "unknown comment style"})
		return d
	}
	if norm == "auto" {
		var ok bool
		norm, ok = ResolveAutoStyle(s.Name, s.Text[start:end])
		if !ok {
			d := mem.Alloc[Document](a)
			d.Alloc, d.Source = a, s
			d.Root = newCat(a, nil, source.Span{Start: start, End: start})
			d.Diagnostics = append(d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: start, End: start}, Message: "ambiguous template comment style"})
			return d
		}
	}
	if norm == "" {
		norm = "plain"
	}
	p := docParser{a: a, s: s, style: norm}
	return p.documentRange(start, end)
}

// ParseRecipeDocumentRange removes the rule's indentation from plain template
// text while keeping all expression and directive offsets in the authored source.
func ParseRecipeDocumentRange(a mem.Allocator, s *source.Source, start int, end int, indent string) *Document {
	p := docParser{a: a, s: s, style: "plain", indent: indent}
	return p.documentRange(start, end)
}

func (p *docParser) documentRange(start int, end int) *Document {
	root := p.parseRange(start, end)
	d := mem.Alloc[Document](p.a)
	d.Alloc, d.Source, d.Root, d.Diagnostics = p.a, p.s, root, p.diags
	return d
}

func styleErrorDocument(a mem.Allocator, name string, text string, message string) *Document {
	s := source.New(a, name, text)
	d := mem.Alloc[Document](a)
	d.Alloc, d.Source, d.OwnSource = a, s, true
	d.Root = newCat(a, nil, source.Span{Start: 0, End: 0})
	d.Diagnostics = append(d.Diagnostics, source.Diagnostic{Code: "TPL_STYLE", Severity: source.Error, Span: source.Span{Start: 0, End: 0}, Message: message})
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
	dirMatch
	dirCase
)

type docParser struct {
	a           mem.Allocator
	s           *source.Source
	style       string
	indent      string
	diags       []source.Diagnostic
	inlineFound inlineBlockDirective
	expansions  expr.ExpansionProbe
}

// NeedsDocumentRange reports whether a plain template contains a directive or
// an interpolation that crosses a line boundary.
func NeedsDocumentRange(a mem.Allocator, s *source.Source, start int, end int) bool {
	p := docParser{a: a, s: s, style: "plain"}
	found := false
	for pos := start; pos < end; {
		line := scanLine(s.Text, pos, end)
		d := p.directiveOnLine(pos, line.ContentEnd)
		found = d.Kind != dirNone
		freeArgs(a, d.Args)
		if !found {
			lineEnd := line.LineEnd
			p.scanInlineLine(&line, end)
			found = line.LineEnd > lineEnd
		}
		if found {
			break
		}
		pos = line.LineEnd
	}
	mem.FreeSlice(nil, p.diags)
	return found
}

type dirOut struct {
	Kind      int
	Args      []*expr.Expr
	Span      source.Span
	EscapeAt  int
	TrimLeft  bool
	TrimRight bool
}

func (p *docParser) diag(code string, span source.Span, msg string) {
	if span.End > len(p.s.Text) {
		span.End = len(p.s.Text)
	}
	if span.Start > span.End {
		span.Start = span.End
	}
	p.diags = append(p.diags, source.Diagnostic{Code: code, Severity: source.Error, Span: span, Message: msg})
}

func (p *docParser) parseRange(start int, end int) *expr.Expr {
	out := p.parseBlock(start, end)
	items := out.Items
	if out.Term != dirNone {
		p.diag("TPL_BLOCK", out.TermSpan, "stray template directive")
		freeArgs(p.a, out.TermArgs)
	} else {
		mem.FreeSlice(nil, out.TermArgs)
	}
	root := newCat(p.a, items, source.Span{Start: start, End: end})
	return root
}

type blockOut struct {
	Items         []*expr.Expr
	Next          int
	Term          int
	TermSpan      source.Span
	TermArgs      []*expr.Expr
	TermEnd       int
	TermTrimRight bool
	TrimRight     bool
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

type ifRes struct {
	Expr      *expr.Expr
	Next      int
	End       int
	TrimRight bool
}

type pairRes struct {
	Expr      *expr.Expr
	Next      int
	TrimRight bool
}

func wrapTemplateTrim(a mem.Allocator, value *expr.Expr, trimStart bool) *expr.Expr {
	call := mem.Alloc[expr.Expr](a)
	call.Kind = expr.Application
	call.Span = value.Span
	head := mem.Alloc[expr.Expr](a)
	head.Kind = expr.Name
	head.Span = value.Span
	if trimStart {
		head.Text = "template-trim-start"
	} else {
		head.Text = "template-trim-end"
	}
	call.Items = append(call.Items, head)
	call.Items = append(call.Items, value)
	return call
}

func wrapTemplateTrimTail(a mem.Allocator, items []*expr.Expr, from int) []*expr.Expr {
	if from < 0 || from >= len(items) {
		return items
	}
	span := source.Span{Start: items[from].Span.Start, End: items[len(items)-1].Span.End}
	var tail []*expr.Expr
	for i := from; i < len(items); i++ {
		tail = append(tail, items[i])
		items[i] = nil
	}
	cat := newCat(a, tail, span)
	trimmed := wrapTemplateTrim(a, cat, true)
	var prefix []*expr.Expr
	for i := 0; i < from; i++ {
		prefix = append(prefix, items[i])
		items[i] = nil
	}
	mem.FreeSlice(nil, items)
	prefix = append(prefix, trimmed)
	return prefix
}

type inlineBlockDirective struct {
	Start int
	End   int
	Dir   dirOut
}

// scanInlineLine extends a text line through multiline interpolations and
// finds the first block comment directive outside those interpolations.
func (p *docParser) scanInlineLine(line *lineOut, documentEnd int) bool {
	open, close := blockCommentDelimiters(p.style)
	text := p.s.Text
	for i := line.Start; i < line.ContentEnd; i++ {
		end := line.ContentEnd
		if hasPrefixAt(text, i, end, "@@") {
			i++
			continue
		}
		if hasPrefixAt(text, i, end, "@{") {
			value := p.inlineExpr(i, end)
			closed, last := value.Closed, value.End
			expr.Free(p.a, value.Expr)
			mem.FreeSlice(nil, value.Diagnostics)
			if !closed && end < documentEnd {
				view := *p.s
				view.Text = text[:documentEnd]
				var next int
				next, closed = p.expansions.End(p.a, &view, i)
				last = next - 1
			}
			if !closed {
				return false
			}
			if last >= end {
				tail := scanLine(text, last+1, documentEnd)
				line.ContentEnd, line.LineEnd = tail.ContentEnd, tail.LineEnd
			}
			i = last
			continue
		}
		if open == "" || !hasPrefixAt(text, i, end, open) {
			continue
		}
		closeStart := findFirst(text, i+len(open), end, close)
		if closeStart < 0 {
			return false
		}
		inner := trimRange(text, i+len(open), closeStart)
		kind, _, _ := directiveKeyword(text, inner.Start, inner.End)
		switch kind {
		case dirIf, dirElif, dirElse, dirFor, dirWith, dirRaw, dirEnd, dirMatch, dirCase:
			span := source.Span{Start: i, End: closeStart + len(close)}
			d := p.parseInner(inner.Start, inner.End, span)
			p.inlineFound = inlineBlockDirective{Start: i, End: span.End, Dir: d}
			return true
		}
		i = closeStart + len(close) - 1
	}
	return false
}

func blockCommentDelimiters(style string) (string, string) {
	switch style {
	case "html":
		return "<!--", "-->"
	case "c":
		return "/*", "*/"
	case "powershell":
		return "<#", "#>"
	}
	return "", ""
}

func findFirst(text string, start int, end int, needle string) int {
	for i := start; i+len(needle) <= end; i++ {
		if hasPrefixAt(text, i, end, needle) {
			return i
		}
	}
	return -1
}

func (p *docParser) parseBlock(pos int, end int) blockOut {
	var items []*expr.Expr
	cur := pos
	trimStartAt := -1
	for cur < end {
		ln := scanLine(p.s.Text, cur, end)
		lineStart, contentEnd, lineEnd := ln.Start, ln.ContentEnd, ln.LineEnd
		d := p.directiveOnLine(lineStart, contentEnd)
		if d.Kind == dirNone {
			inlineBlock := p.scanInlineLine(&ln, end)
			contentEnd, lineEnd = ln.ContentEnd, ln.LineEnd
			if inlineBlock {
				inline := p.inlineFound
				parts := p.inlineParts(cur, inline.Start, inline.Start)
				for i := range parts {
					items = append(items, parts[i])
				}
				mem.FreeSlice(nil, parts)
				lineStart, contentEnd, lineEnd, d = inline.Start, inline.End, inline.End, inline.Dir
			}
		}
		if d.Kind == dirNone || d.Kind == dirError {
			lineStart = p.literalStart(lineStart, contentEnd)
			parts := p.inlineParts(lineStart, contentEnd, lineEnd)
			items = append(items, parts...)
			mem.FreeSlice(nil, parts)
			cur = lineEnd
			continue
		}
		if d.Kind == dirEscape {
			text := p.s.Text
			lineStart = p.literalStart(lineStart, d.EscapeAt)
			combined := text[lineStart:d.EscapeAt] + text[d.EscapeAt+1:lineEnd]
			items = append(items, newStringLit(p.a, combined, source.Span{Start: lineStart, End: lineEnd}))
			cur = lineEnd
			continue
		}
		if d.TrimLeft && len(items) != 0 {
			items = wrapTemplateTrimTail(p.a, items, trimStartAt)
			trimStartAt = -1
			span := source.Span{Start: items[0].Span.Start, End: items[len(items)-1].Span.End}
			items = []*expr.Expr{wrapTemplateTrim(p.a, newCat(p.a, items, span), false)}
		}
		if d.Kind == dirElif || d.Kind == dirElse || d.Kind == dirEnd || d.Kind == dirCase {
			// Terminator for caller. Free nothing; caller owns TermArgs.
			pendingTrim := trimStartAt >= 0 && trimStartAt == len(items)
			items = wrapTemplateTrimTail(p.a, items, trimStartAt)
			return blockOut{Items: items, Next: lineStart, Term: d.Kind, TermSpan: d.Span, TermArgs: d.Args, TermEnd: lineEnd, TermTrimRight: d.TrimRight, TrimRight: pendingTrim || d.TrimRight}
		}
		if d.Kind == dirLet {
			// The binding scopes over the remainder of this block.
			rest := p.parseBlock(lineEnd, end)
			restCat := newCat(p.a, rest.Items, source.Span{Start: lineEnd, End: rest.Next})
			if d.TrimRight {
				restCat = wrapTemplateTrim(p.a, restCat, true)
			}
			nameExpr := d.Args[0]
			valExpr := d.Args[1]
			mem.FreeSlice(nil, d.Args)
			bind := mem.Alloc[expr.Expr](p.a)
			bind.Kind = expr.List
			bind.Span = d.Span
			bind.Items = append(bind.Items, nameExpr)
			bind.Items = append(bind.Items, valExpr)
			letApp := mem.Alloc[expr.Expr](p.a)
			letApp.Kind = expr.Application
			letApp.Span = source.Span{Start: d.Span.Start, End: rest.Next}
			head := mem.Alloc[expr.Expr](p.a)
			head.Kind = expr.Name
			head.Span = d.Span
			head.Text = "let"
			letApp.Items = append(letApp.Items, head)
			letApp.Items = append(letApp.Items, bind)
			letApp.Items = append(letApp.Items, restCat)
			items = append(items, letApp)
			// @let consumes the remainder; propagate terminator outward.
			return blockOut{Items: items, Next: rest.Next, Term: rest.Term, TermSpan: rest.TermSpan, TermArgs: rest.TermArgs, TermEnd: rest.TermEnd, TermTrimRight: rest.TermTrimRight, TrimRight: rest.TrimRight}
		}
		if d.Kind == dirInclude {
			inc := mem.Alloc[expr.Expr](p.a)
			inc.Kind = expr.Application
			inc.Span = d.Span
			head := mem.Alloc[expr.Expr](p.a)
			head.Kind = expr.Name
			head.Span = d.Span
			head.Text = "template-include"
			inc.Items = append(inc.Items, head)
			for i := 1; i < len(d.Args); i++ {
				inc.Items = append(inc.Items, d.Args[i])
			}
			inc.Items = append(inc.Items, d.Args[0])
			mem.FreeSlice(nil, d.Args)
			items = append(items, inc)
			if d.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			cur = lineEnd
			continue
		}
		if d.Kind == dirIf {
			r := p.parseIf(d, lineEnd, end)
			items = append(items, r.Expr)
			cur = r.Next
			if r.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			continue
		}
		if d.Kind == dirFor {
			r := p.parseFor(d, lineEnd, end)
			items = append(items, r.Expr)
			cur = r.Next
			if r.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			continue
		}
		if d.Kind == dirWith {
			r := p.parseWith(d, lineEnd, end)
			items = append(items, r.Expr)
			cur = r.Next
			if r.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			continue
		}
		if d.Kind == dirRaw {
			r := p.parseRaw(d, lineEnd, end)
			items = append(items, r.Expr)
			cur = r.Next
			if r.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			continue
		}
		if d.Kind == dirMatch {
			r := p.parseMatch(d, lineEnd, end)
			items = append(items, r.Expr)
			cur = r.Next
			if r.TrimRight {
				items = wrapTemplateTrimTail(p.a, items, trimStartAt)
				trimStartAt = len(items)
			}
			continue
		}
		// Unknown: skip line.
		cur = lineEnd
	}
	pendingTrim := trimStartAt >= 0 && trimStartAt == len(items)
	items = wrapTemplateTrimTail(p.a, items, trimStartAt)
	return blockOut{Items: items, Next: end, Term: dirNone, TermEnd: end, TrimRight: pendingTrim}
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
	app.Items = append(app.Items, ifHead)
	app.Items = append(app.Items, head.Args[0])
	app.Items = append(app.Items, firstCat)
	mem.FreeSlice(nil, head.Args)
	curTerm := first.Term
	curPos := first.Next
	curEnd := first.TermEnd
	curArgs := first.TermArgs
	curSpan := first.TermSpan
	curTrimRight := first.TermTrimRight
	// Elif chain: TermArgs holds the condition.
	for curTerm == dirElif {
		branch := p.parseBlock(curEnd, end)
		branchCat := newCat(p.a, branch.Items, source.Span{Start: curEnd, End: branch.Next})
		if curTrimRight {
			branchCat = wrapTemplateTrim(p.a, branchCat, true)
		}
		// curArgs has exactly one condition (validated at parse).
		cond := curArgs[0]
		mem.FreeSlice(nil, curArgs)
		app.Items = append(app.Items, cond)
		app.Items = append(app.Items, branchCat)
		app.Span.End = branch.Next
		curTerm = branch.Term
		curPos = branch.Next
		curEnd = branch.TermEnd
		curArgs = branch.TermArgs
		curSpan = branch.TermSpan
		curTrimRight = branch.TermTrimRight
		_ = curSpan
	}
	if curTerm == dirElse {
		branch := p.parseBlock(curEnd, end)
		branchCat := newCat(p.a, branch.Items, source.Span{Start: curEnd, End: branch.Next})
		if curTrimRight {
			branchCat = wrapTemplateTrim(p.a, branchCat, true)
		}
		app.Items = append(app.Items, branchCat)
		app.Span.End = branch.Next
		curTerm = branch.Term
		curPos = branch.Next
		curEnd = branch.TermEnd
		freeArgs(p.a, curArgs)
		curArgs = branch.TermArgs
		curSpan = branch.TermSpan
		curTrimRight = branch.TermTrimRight
		_ = curSpan
	}
	if curTerm != dirEnd {
		p.diag("TPL_BLOCK", head.Span, "unterminated @if block")
		freeArgs(p.a, curArgs)
		return ifRes{Expr: app, Next: curPos, End: curEnd}
	}
	p.checkEndLabel(curArgs, "if", curSpan)
	freeArgs(p.a, curArgs)
	app.Span.End = curEnd
	if head.TrimRight {
		app = wrapTemplateTrim(p.a, app, true)
	}
	return ifRes{Expr: app, Next: curEnd, End: curEnd, TrimRight: curTrimRight}
}

func (p *docParser) parseFor(head dirOut, bodyPos int, end int) pairRes {
	body := p.parseBlock(bodyPos, end)
	bodyCat := newCat(p.a, body.Items, source.Span{Start: bodyPos, End: body.Next})
	next := body.Next
	if body.Term == dirEnd {
		p.checkEndLabel(body.TermArgs, "for", body.TermSpan)
		next = body.TermEnd
	} else {
		p.diag("TPL_BLOCK", head.Span, "unterminated @for block")
	}
	freeArgs(p.a, body.TermArgs)
	params := lambdaParams(head.Args[0])
	callback := mem.Alloc[expr.Expr](p.a)
	callback.Kind, callback.Span = expr.Lambda, head.Span
	callback.Parameters, callback.Body = params, []*expr.Expr{bodyCat}
	if len(params) != 1 || params[0].Rest {
		const item = "@template-item"
		arg := mem.Alloc[expr.Expr](p.a)
		arg.Kind, arg.Span, arg.Text = expr.Name, head.Span, item
		apply := p.templateCall("template-apply", head.Span, callback, arg)
		callback = mem.Alloc[expr.Expr](p.a)
		callback.Kind, callback.Span = expr.Lambda, head.Span
		callback.Parameters = []expr.Parameter{{Name: item, Span: head.Span}}
		callback.Body = []*expr.Expr{apply}
	}
	mapped := p.templateCall("template-map", head.Span, callback, head.Args[1])
	var separator *expr.Expr
	if len(head.Args) == 3 {
		separator = head.Args[2]
	} else {
		separator = newStringLit(p.a, "", head.Span)
	}
	joined := p.templateCall("join", source.Span{Start: head.Span.Start, End: next}, separator, mapped)
	expr.Free(p.a, head.Args[0])
	mem.FreeSlice(nil, head.Args)
	if head.TrimRight {
		joined = wrapTemplateTrim(p.a, joined, true)
	}
	return pairRes{Expr: joined, Next: next, TrimRight: body.TermTrimRight}
}

func (p *docParser) templateCall(name string, span source.Span, args ...*expr.Expr) *expr.Expr {
	head := mem.Alloc[expr.Expr](p.a)
	head.Kind, head.Span, head.Text = expr.Name, span, name
	call := mem.Alloc[expr.Expr](p.a)
	call.Kind, call.Span = expr.Application, span
	call.Items = append([]*expr.Expr{head}, args...)
	return call
}

func (p *docParser) parseWith(head dirOut, bodyPos int, end int) pairRes {
	body := p.parseBlock(bodyPos, end)
	bodyCat := newCat(p.a, body.Items, source.Span{Start: bodyPos, End: body.Next})
	if body.Term == dirEnd {
		p.checkEndLabel(body.TermArgs, "with", body.TermSpan)
	}
	freeArgs(p.a, body.TermArgs)
	if body.Term != dirEnd {
		p.diag("TPL_BLOCK", head.Span, "unterminated @with block")
	}
	nextPos := body.Next
	if body.Term == dirEnd {
		nextPos = body.TermEnd
	}
	app := mem.Alloc[expr.Expr](p.a)
	app.Kind = expr.Application
	app.Span = source.Span{Start: head.Span.Start, End: nextPos}
	headExpr := mem.Alloc[expr.Expr](p.a)
	headExpr.Kind = expr.Name
	headExpr.Span = head.Span
	headExpr.Text = "with"
	app.Items = append(app.Items, headExpr)
	app.Items = append(app.Items, head.Args[0])
	app.Items = append(app.Items, bodyCat)
	mem.FreeSlice(nil, head.Args)
	if head.TrimRight {
		app = wrapTemplateTrim(p.a, app, true)
	}
	return pairRes{Expr: app, Next: nextPos, TrimRight: body.TermTrimRight}
}

func (p *docParser) parseMatch(head dirOut, bodyPos int, end int) pairRes {
	app := mem.Alloc[expr.Expr](p.a)
	app.Kind = expr.Application
	app.Span = head.Span
	matchHead := mem.Alloc[expr.Expr](p.a)
	matchHead.Kind = expr.Name
	matchHead.Text = "match"
	matchHead.Span = head.Span
	app.Items = append(app.Items, matchHead)
	app.Items = append(app.Items, head.Args[0])
	mem.FreeSlice(nil, head.Args)
	first := p.parseBlock(bodyPos, end)
	if first.Term != dirCase || first.Next != bodyPos || len(first.TermArgs) != 1 {
		p.diag("TPL_BLOCK", head.Span, "@match must begin with @case")
		freeArgs(p.a, first.Items)
		freeArgs(p.a, first.TermArgs)
		return pairRes{Expr: app, Next: first.Next}
	}
	pattern := first.TermArgs[0]
	mem.FreeSlice(nil, first.TermArgs)
	cur := first.TermEnd
	caseTrimRight := first.TermTrimRight
	for {
		block := p.parseBlock(cur, end)
		bodyCat := newCat(p.a, block.Items, source.Span{Start: cur, End: block.Next})
		if head.TrimRight || caseTrimRight {
			bodyCat = wrapTemplateTrim(p.a, bodyCat, true)
		}
		clause := mem.Alloc[expr.Expr](p.a)
		clause.Kind = expr.List
		clause.Span = source.Span{Start: pattern.Span.Start, End: block.Next}
		clause.Items = append(clause.Items, pattern)
		clause.Items = append(clause.Items, bodyCat)
		app.Items = append(app.Items, clause)
		if block.Term == dirCase {
			pattern = block.TermArgs[0]
			mem.FreeSlice(nil, block.TermArgs)
			cur = block.TermEnd
			caseTrimRight = block.TermTrimRight
			continue
		}
		if block.Term == dirElse {
			freeArgs(p.a, block.TermArgs)
			fallback := p.parseBlock(block.TermEnd, end)
			if fallback.Term != dirEnd {
				p.diag("TPL_BLOCK", block.TermSpan, "unterminated or invalid @match fallback")
				freeArgs(p.a, fallback.Items)
				freeArgs(p.a, fallback.TermArgs)
				return pairRes{Expr: app, Next: fallback.Next}
			}
			marker := mem.Alloc[expr.Expr](p.a)
			marker.Kind = expr.KashBranch
			marker.Text = "else"
			marker.Span = block.TermSpan
			elseClause := mem.Alloc[expr.Expr](p.a)
			elseClause.Kind = expr.List
			elseClause.Span = source.Span{Start: marker.Span.Start, End: fallback.Next}
			elseClause.Items = append(elseClause.Items, marker)
			fallbackCat := newCat(p.a, fallback.Items, source.Span{Start: block.TermEnd, End: fallback.Next})
			if head.TrimRight || block.TermTrimRight {
				fallbackCat = wrapTemplateTrim(p.a, fallbackCat, true)
			}
			elseClause.Items = append(elseClause.Items, fallbackCat)
			app.Items = append(app.Items, elseClause)
			p.checkEndLabel(fallback.TermArgs, "match", fallback.TermSpan)
			freeArgs(p.a, fallback.TermArgs)
			app.Span.End = fallback.TermEnd
			if head.TrimRight {
				app = wrapTemplateTrim(p.a, app, true)
			}
			return pairRes{Expr: app, Next: fallback.TermEnd, TrimRight: fallback.TermTrimRight}
		}
		if block.Term != dirEnd {
			p.diag("TPL_BLOCK", head.Span, "unterminated @match block")
			freeArgs(p.a, block.TermArgs)
			return pairRes{Expr: app, Next: block.Next}
		}
		p.checkEndLabel(block.TermArgs, "match", block.TermSpan)
		freeArgs(p.a, block.TermArgs)
		app.Span.End = block.TermEnd
		if head.TrimRight {
			app = wrapTemplateTrim(p.a, app, true)
		}
		return pairRes{Expr: app, Next: block.TermEnd, TrimRight: block.TermTrimRight}
	}
}

func (p *docParser) parseRaw(head dirOut, bodyPos int, end int) pairRes {
	cur := bodyPos
	for cur < end {
		ln := scanLine(p.s.Text, cur, end)
		if p.nextRawEnd(cur, ln.ContentEnd) {
			directive := p.inlineFound
			freeArgs(p.a, directive.Dir.Args)
			rawText := p.rawText(bodyPos, directive.Start)
			value := newStringLit(p.a, rawText, source.Span{Start: bodyPos, End: directive.Start})
			if directive.Dir.TrimLeft {
				value = wrapTemplateTrim(p.a, value, false)
			}
			if head.TrimRight {
				value = wrapTemplateTrim(p.a, value, true)
			}
			next := directive.End
			if onlyTemplateWhitespace(p.s.Text, ln.Start, directive.Start) && onlyTemplateWhitespace(p.s.Text, directive.End, ln.ContentEnd) {
				next = ln.LineEnd
			}
			return pairRes{Expr: value, Next: next, TrimRight: directive.Dir.TrimRight}
		}
		cur = ln.LineEnd
	}
	p.diag("TPL_PARSE", head.Span, "unterminated @raw block")
	lit := newStringLit(p.a, p.rawText(bodyPos, end), source.Span{Start: bodyPos, End: end})
	if head.TrimRight {
		lit = wrapTemplateTrim(p.a, lit, true)
	}
	return pairRes{Expr: lit, Next: end}
}

func (p *docParser) literalStart(start int, end int) int {
	if hasPrefixAt(p.s.Text, start, end, p.indent) {
		return start + len(p.indent)
	}
	return start
}

func (p *docParser) rawText(start int, end int) string {
	if start >= end {
		return ""
	}
	text := p.s.Text
	if p.indent == "" {
		return text[start:end]
	}
	var result strings.Builder
	for start < end {
		line := scanLine(text, start, end)
		result.WriteString(text[p.literalStart(start, line.ContentEnd):line.LineEnd])
		start = line.LineEnd
	}
	return result.String()
}

func onlyTemplateWhitespace(text string, start int, end int) bool {
	for i := start; i < end; i++ {
		if text[i] != ' ' && text[i] != '\t' && text[i] != '\r' && text[i] != '\n' {
			return false
		}
	}
	return true
}

func (p *docParser) checkEndLabel(args []*expr.Expr, expected string, span source.Span) {
	if len(args) == 0 {
		return
	}
	if len(args) != 1 || args[0].Kind != expr.Name || args[0].Text != expected {
		p.diag("TPL_BLOCK", span, "template block end label does not match opening directive")
	}
}

// Raw text is scanned only for an end directive in the active style.
func (p *docParser) nextRawEnd(start int, end int) bool {
	text := p.s.Text
	whole := p.directiveBodyOnLine(start, end)
	span := source.Span{Start: start, End: end}
	if d, ok := p.rawEndDirective(whole, span); ok {
		p.inlineFound = inlineBlockDirective{Start: start, End: end, Dir: d}
		return true
	}
	open, close := blockCommentDelimiters(p.style)
	if open == "" {
		return false
	}
	for i := start; i < end; i++ {
		if !hasPrefixAt(text, i, end, open) {
			continue
		}
		closeStart := findFirst(text, i+len(open), end, close)
		if closeStart < 0 {
			return false
		}
		inner := trimRange(text, i+len(open), closeStart)
		span := source.Span{Start: i, End: closeStart + len(close)}
		if d, ok := p.rawEndDirective(inner, span); ok {
			p.inlineFound = inlineBlockDirective{Start: i, End: span.End, Dir: d}
			return true
		}
		i = closeStart + len(close) - 1
	}
	return false
}

func (p *docParser) rawEndDirective(inner trimOut, span source.Span) (dirOut, bool) {
	text := p.s.Text
	kind, after, _ := directiveKeyword(text, inner.Start, inner.End)
	if kind != dirEnd || (after < inner.End && !horizontal(text[after])) {
		return dirOut{}, false
	}
	args, _ := directiveOperands(text, after, inner.End)
	if label := text[args.Start:args.End]; label != "" && label != "raw" {
		return dirOut{}, false
	}
	return p.parseInner(inner.Start, inner.End, span), true
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

func (p *docParser) directiveOnLine(start, end int) dirOut {
	body := p.directiveBodyOnLine(start, end)
	return p.parseInner(body.Start, body.End, source.Span{Start: start, End: end})
}

func (p *docParser) directiveBodyOnLine(start, end int) trimOut {
	text := p.s.Text
	line := trimRange(text, start, end)
	if p.style == "plain" {
		return line
	}
	open, close := blockCommentDelimiters(p.style)
	if open != "" && hasPrefixAt(text, line.Start, line.End, open) {
		closeStart := findFirst(text, line.Start+len(open), line.End, close)
		if closeStart >= 0 && closeStart+len(close) == line.End {
			return trimRange(text, line.Start+len(open), closeStart)
		}
		return trimOut{}
	}
	var prefix string
	switch p.style {
	case "c":
		prefix = "//"
	case "hash", "powershell":
		prefix = "#"
	case "dash":
		prefix = "--"
	case "semi":
		prefix = ";"
	case "percent":
		prefix = "%"
	case "batch":
		if inner := batchInnerStart(text, line.Start, line.End); inner >= 0 {
			return trimRange(text, inner, line.End)
		}
		return trimOut{}
	default:
		return trimOut{}
	}
	if hasPrefixAt(text, line.Start, line.End, prefix) {
		return trimRange(text, line.Start+len(prefix), line.End)
	}
	return trimOut{}
}

func batchInnerStart(text string, start int, end int) int {
	if hasPrefixAt(text, start, end, "::") {
		pos := start + 2
		for pos < end && (text[pos] == ' ' || text[pos] == '\t') {
			pos++
		}
		return pos
	}
	if end-start < 4 || !eqFold(text[start:start+3], "rem") || (text[start+3] != ' ' && text[start+3] != '\t') {
		return -1
	}
	pos := start + 4
	for pos < end && (text[pos] == ' ' || text[pos] == '\t') {
		pos++
	}
	return pos
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
	case "match":
		return dirMatch
	case "case":
		return dirCase
	}
	return dirNone
}

func directiveKeyword(text string, start, end int) (kind, operandStart int, trimLeft bool) {
	if start >= end || text[start] != '@' {
		return dirNone, start, false
	}
	word := start + 1
	trimLeft = word < end && text[word] == '-'
	if trimLeft {
		word++
	}
	pos := word
	for pos < end && isKeywordLetter(text[pos]) {
		pos++
	}
	if pos < end && !horizontal(text[pos]) && text[pos] != '-' {
		return dirNone, pos, trimLeft
	}
	return keywordKind(text[word:pos]), pos, trimLeft
}

func (p *docParser) parseInner(start, end int, span source.Span) dirOut {
	text := p.s.Text
	if hasPrefixAt(text, start, end, "@@") {
		return dirOut{Kind: dirEscape, Span: span, EscapeAt: start}
	}
	kind, after, left := directiveKeyword(text, start, end)
	if kind == dirNone {
		return dirOut{}
	}
	argsRange, right := directiveOperands(text, after, end)
	var args []*expr.Expr
	ok := false
	code, message := "TPL_PARSE", "malformed template directive"
	if after == end || horizontal(text[after]) {
		switch kind {
		case dirElse, dirRaw:
			ok = argsRange.Start == argsRange.End
		case dirEnd:
			label := text[argsRange.Start:argsRange.End]
			switch label {
			case "":
				ok = true
			case "if", "for", "with", "match", "raw":
				args = []*expr.Expr{p.directiveName(argsRange)}
				ok = true
			}
		case dirLet:
			args, ok = p.parseLetOperands(argsRange)
			message = "expected @let NAME = EXPR"
		case dirFor:
			args, ok = p.parseForOperands(argsRange)
			message = "expected @for NAME… in EXPR with unique names, any rest parameter last, and an optional sep EXPR"
		case dirInclude:
			args, ok = p.parseIncludeOperands(argsRange)
			message = "expected @include PATH followed by an optional payload expression"
		case dirCase:
			bounded := *p.s
			bounded.Text = text[:argsRange.End]
			if argsRange.Start != argsRange.End {
				pattern := expr.ParseMatchPatternPrefix(p.a, &bounded, argsRange.Start)
				ok = pattern.Expr != nil && len(pattern.Diagnostics) == 0 && pattern.End == argsRange.End
				args = []*expr.Expr{pattern.Expr}
				if len(pattern.Diagnostics) != 0 && pattern.Diagnostics[0].Code == "PAT_INVALID" {
					code, message = pattern.Diagnostics[0].Code, pattern.Diagnostics[0].Message
				}
				mem.FreeSlice(nil, pattern.Diagnostics)
			}
		default:
			if value := p.directiveExpression(argsRange); value != nil {
				args, ok = []*expr.Expr{value}, true
			}
		}
	}
	if !ok {
		freeArgs(p.a, args)
		p.diag(code, span, message)
		return dirOut{Kind: dirError, Span: span}
	}
	return dirOut{Kind: kind, Args: args, Span: span, TrimLeft: left, TrimRight: right}
}

func directiveOperands(text string, after, end int) (trimOut, bool) {
	right := end > after && text[end-1] == '-' && horizontal(text[end-2])
	if right {
		end--
	}
	return trimRange(text, after, end), right
}

func (p *docParser) directiveName(r trimOut) *expr.Expr {
	name := mem.Alloc[expr.Expr](p.a)
	name.Kind, name.Text = expr.Name, p.s.Text[r.Start:r.End]
	name.Span = source.Span{Start: r.Start, End: r.End}
	return name
}

func (p *docParser) directiveExpression(r trimOut) *expr.Expr {
	if r.Start == r.End {
		return nil
	}
	value := expr.ParseRange(p.a, p.s, r.Start, r.End, "")
	defer mem.FreeSlice(nil, value.Diagnostics)
	if len(value.Diagnostics) != 0 {
		expr.Free(p.a, value.Expr)
		return nil
	}
	return value.Expr
}

func (p *docParser) parseLetOperands(r trimOut) ([]*expr.Expr, bool) {
	text := p.s.Text
	equal := strings.IndexByte(text[r.Start:r.End], '=')
	if equal < 0 {
		return nil, false
	}
	equal += r.Start
	name := trimRange(text, r.Start, equal)
	if !expr.ValidBindingName(text[name.Start:name.End]) {
		return nil, false
	}
	value := p.directiveExpression(trimRange(text, equal+1, r.End))
	if value == nil {
		return nil, false
	}
	return []*expr.Expr{p.directiveName(name), value}, true
}

func (p *docParser) parseForOperands(r trimOut) ([]*expr.Expr, bool) {
	text := p.s.Text
	params := mem.Alloc[expr.Expr](p.a)
	params.Kind = expr.List
	params.Span.Start = r.Start
	params.Span.End = r.Start
	names := make(map[string]bool)
	pos := r.Start
	for pos < r.End {
		start := pos
		for pos < r.End && !horizontal(text[pos]) {
			pos++
		}
		word := text[start:pos]
		if word == "in" {
			break
		}
		name, rest := strings.CutSuffix(word, "...")
		if !expr.ValidBindingName(name) || names[name] || (len(params.Items) != 0 && params.Items[len(params.Items)-1].Rest) {
			expr.Free(p.a, params)
			return nil, false
		}
		names[name] = true
		arg := p.directiveName(trimOut{Start: start, End: start + len(name)})
		arg.Rest = rest
		params.Items = append(params.Items, arg)
		params.Span.End = pos
		pos = trimRange(text, pos, r.End).Start
	}
	sourceRange := trimRange(text, pos, r.End)
	if len(params.Items) == 0 || sourceRange.Start == sourceRange.End {
		expr.Free(p.a, params)
		return nil, false
	}
	list := expr.ParseTemplateLoopSource(p.a, p.s, sourceRange.Start, sourceRange.End)
	defer mem.FreeSlice(nil, list.Diagnostics)
	args := []*expr.Expr{params, list.Expr}
	if list.Expr == nil || len(list.Diagnostics) != 0 {
		return args, false
	}
	tail := trimRange(text, list.End, r.End)
	if tail.Start == tail.End {
		return args, true
	}
	if !hasPrefixAt(text, tail.Start, tail.End, "sep") || tail.Start+3 == tail.End || !horizontal(text[tail.Start+3]) {
		return args, false
	}
	separator := p.directiveExpression(trimRange(text, tail.Start+3, tail.End))
	return append(args, separator), separator != nil
}

func (p *docParser) parseIncludeOperands(r trimOut) ([]*expr.Expr, bool) {
	if r.Start == r.End {
		return nil, false
	}
	path := expr.ParsePostfixRange(p.a, p.s, r.Start, r.End)
	defer mem.FreeSlice(nil, path.Diagnostics)
	args := []*expr.Expr{path.Expr}
	if path.Expr == nil || len(path.Diagnostics) != 0 || (path.End < r.End && !horizontal(p.s.Text[path.End])) {
		return args, false
	}
	switch path.Expr.Kind {
	case expr.Path, expr.String, expr.Name, expr.Reference, expr.EnvironmentReference, expr.Group, expr.Application:
	default:
		return args, false
	}
	tail := trimRange(p.s.Text, path.End, r.End)
	if tail.Start == tail.End {
		return args, true
	}
	payload := p.directiveExpression(tail)
	return append(args, payload), payload != nil
}

func lambdaParams(list *expr.Expr) []expr.Parameter {
	out := make([]expr.Parameter, 0, len(list.Items))
	for _, item := range list.Items {
		span := item.Span
		if item.Rest {
			span.End += 3
		}
		out = append(out, expr.Parameter{Name: item.Text, Span: span, Rest: item.Rest})
	}
	return out
}

// inlineParts parses body text (lineStart..contentEnd) plus its line ending
// (contentEnd..lineEnd) into string literals and expression interpolations.
func (p *docParser) inlineParts(lineStart int, contentEnd int, lineEnd int) []*expr.Expr {
	var items []*expr.Expr
	text := p.s.Text
	litStart := lineStart
	i := lineStart
	for i < contentEnd {
		b := text[i]
		if b == '@' && i+1 < contentEnd && text[i+1] == '@' {
			if i > litStart {
				items = append(items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
			}
			items = append(items, newStringLit(p.a, text[i+1:i+2], source.Span{Start: i, End: i + 2}))
			i += 2
			litStart = i
			continue
		}
		if b == '@' && i+1 < contentEnd && text[i+1] == '{' {
			if i > litStart {
				items = append(items, newStringLit(p.a, text[litStart:i], source.Span{Start: litStart, End: i}))
			}
			prefix := p.inlineExpr(i, contentEnd)
			if !prefix.Closed {
				expr.Free(p.a, prefix.Expr)
				mem.FreeSlice(nil, prefix.Diagnostics)
				p.diag("PARSE_ERR", source.Span{Start: i, End: i + 2}, "unclosed template interpolation; expected '}'")
				items = append(items, newStringLit(p.a, "@", source.Span{Start: i, End: i + 1}))
				i++
				litStart = i
				continue
			}
			p.diags = append(p.diags, prefix.Diagnostics...)
			mem.FreeSlice(nil, prefix.Diagnostics)
			if prefix.Expr == nil {
				items = append(items, newStringLit(p.a, text[i:prefix.End+1], source.Span{Start: i, End: prefix.End + 1}))
			} else {
				items = append(items, prefix.Expr)
			}
			i = prefix.End + 1
			litStart = i
			continue
		}
		i++
	}
	if contentEnd > litStart {
		items = append(items, newStringLit(p.a, text[litStart:contentEnd], source.Span{Start: litStart, End: contentEnd}))
	}
	if lineEnd > contentEnd {
		ending := text[contentEnd:lineEnd]
		items = append(items, newStringLit(p.a, ending, source.Span{Start: contentEnd, End: lineEnd}))
	}
	return items
}

type inlineOut struct {
	Expr        *expr.Expr
	End         int
	Closed      bool
	Diagnostics []source.Diagnostic
}

func (p *docParser) inlineExpr(start int, contentEnd int) inlineOut {
	view := mem.Alloc[source.Source](p.a)
	*view = *p.s
	view.Text = p.s.Text[:contentEnd]
	prefix := expr.ParseExpansionPrefix(p.a, view, start)
	mem.Free(p.a, view)
	return inlineOut{Expr: prefix.Expr, End: prefix.End - 1, Closed: prefix.Closed, Diagnostics: prefix.Diagnostics}
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
		e.Parts = append(e.Parts, expr.StringPart{Text: partText, Span: span})
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
	head.Text = "template-cat"
	e.Items = append(e.Items, head)
	for i := range items {
		e.Items = append(e.Items, items[i])
	}
	mem.FreeSlice(nil, items)
	return e
}

func freeArgs(a mem.Allocator, args []*expr.Expr) {
	for i := range args {
		expr.Free(a, args[i])
	}
	mem.FreeSlice(nil, args)
}
