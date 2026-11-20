package template

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"strings"
)

// DocumentFormatResult carries canonical document text or the first source
// diagnostic. Its strings are owned by Alloc.
type DocumentFormatResult struct {
	Alloc   mem.Allocator
	Text    string
	Code    string
	Message string
	Span    source.Span
	OK      bool
}

func (r *DocumentFormatResult) Free() {
	if r == nil {
		return
	}
	a := r.Alloc
	mem.FreeString(a, r.Text)
	mem.FreeString(a, r.Code)
	mem.FreeString(a, r.Message)
	*r = DocumentFormatResult{}
	mem.Free(a, r)
}

// FormatDocument canonicalizes recognized directive tokens while copying all
// other source bytes unchanged. Empty style selects auto inference.
func FormatDocument(a mem.Allocator, name string, text string, requestedStyle string) *DocumentFormatResult {
	style := requestedStyle
	if style == "" {
		style = "auto"
	}
	if style == "auto" {
		var ok bool
		style, ok = ResolveAutoStyle(name, text)
		if !ok {
			return documentFormatError(a, "TPL_STYLE", "ambiguous template comment style")
		}
	} else {
		var ok bool
		style, ok = NormalizeStyle(style)
		if !ok {
			return documentFormatError(a, "TPL_STYLE", "unknown comment style")
		}
	}
	doc := ParseDocument(a, name, text, style)
	if len(doc.Diagnostics) != 0 {
		diag := doc.Diagnostics[0]
		result := documentFormatError(a, diag.Code, diag.Message)
		result.Span = diag.Span
		doc.Free()
		return result
	}
	doc.Free()

	sourceText := source.New(a, name, text)
	p := docParser{a: a, s: sourceText, style: style}
	out := strings.Builder{}
	defer out.Reset()
	var blocks []int
	cur := 0
	for cur < len(text) {
		line := scanLine(text, cur, len(text))
		if len(blocks) != 0 && blocks[len(blocks)-1] == dirRaw {
			if !p.nextRawEnd(cur, line.ContentEnd) {
				out.WriteString(text[cur:line.LineEnd])
				cur = line.LineEnd
				continue
			}
			directive := p.inlineFound
			isWhole := directive.Start == cur && directive.End == line.ContentEnd
			start, end := directive.Start, directive.End
			if isWhole {
				token := trimRange(text, start, end)
				start, end = token.Start, token.End
			}
			out.WriteString(text[cur:start])
			writeCanonicalDirective(a, &out, style, directive.Dir, &blocks)
			freeArgs(a, directive.Dir.Args)
			cur = end
			if isWhole {
				out.WriteString(text[cur:line.LineEnd])
				cur = line.LineEnd
			}
			continue
		}
		lineDirective := p.directiveOnLine(line.Start, line.ContentEnd)
		if lineDirective.Kind == dirEscape {
			out.WriteString(text[cur:line.LineEnd])
			cur = line.LineEnd
			continue
		}
		if lineDirective.Kind != dirNone && lineDirective.Kind != dirError {
			indentEnd := line.Start
			for indentEnd < line.ContentEnd && horizontal(text[indentEnd]) {
				indentEnd++
			}
			trailingStart := line.ContentEnd
			for trailingStart > indentEnd && horizontal(text[trailingStart-1]) {
				trailingStart--
			}
			out.WriteString(text[cur:indentEnd])
			writeCanonicalDirective(a, &out, style, lineDirective, &blocks)
			out.WriteString(text[trailingStart:line.ContentEnd])
			if line.LineEnd > line.ContentEnd {
				out.WriteString(text[line.ContentEnd:line.LineEnd])
			}
			freeArgs(a, lineDirective.Args)
			cur = line.LineEnd
			continue
		}
		if p.scanInlineLine(&line, len(text)) {
			directive := p.inlineFound
			p.writeInlineText(&out, cur, directive.Start)
			writeCanonicalDirective(a, &out, style, directive.Dir, &blocks)
			freeArgs(a, directive.Dir.Args)
			cur = directive.End
			continue
		}
		p.writeInlineText(&out, cur, line.ContentEnd)
		out.WriteString(text[line.ContentEnd:line.LineEnd])
		cur = line.LineEnd
	}
	mem.FreeSlice(nil, blocks)
	sourceText.Free(a)
	result := mem.Alloc[DocumentFormatResult](a)
	result.Alloc, result.Text, result.OK = a, cloneDocumentFormatText(a, out.String()), true
	return result
}

func (p *docParser) writeInlineText(out *strings.Builder, start, end int) {
	text := p.s.Text
	literal := start
	for i := start; i+1 < end; i++ {
		if text[i] != '@' {
			continue
		}
		if text[i+1] == '@' {
			i++
			continue
		}
		if text[i+1] != '{' {
			continue
		}
		value := p.inlineExpr(i, end)
		if value.Closed && value.Expr != nil && len(value.Diagnostics) == 0 {
			out.WriteString(text[literal:i])
			out.WriteString("@{")
			formatted := expr.Compact(p.a, value.Expr)
			if strings.ContainsRune(formatted, '\n') {
				out.WriteByte('\n')
			}
			out.WriteString(formatted)
			mem.FreeString(p.a, formatted)
			out.WriteByte('}')
			literal = value.End + 1
			i = value.End
		}
		expr.Free(p.a, value.Expr)
		mem.FreeSlice(nil, value.Diagnostics)
	}
	out.WriteString(text[literal:end])
}

func documentFormatError(a mem.Allocator, code string, message string) *DocumentFormatResult {
	result := mem.Alloc[DocumentFormatResult](a)
	result.Alloc = a
	result.Code = cloneDocumentFormatText(a, code)
	result.Message = cloneDocumentFormatText(a, message)
	return result
}

func cloneDocumentFormatText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	buffer := mem.AllocSlice[byte](a, len(text), len(text))
	copy(buffer, text)
	return string(buffer)
}

func writeCanonicalDirective(a mem.Allocator, out *strings.Builder, style string, d dirOut, blocks *[]int) {
	canonical := canonicalDirective(a, d, blocks)
	wrapped := canonicalComment(a, style, canonical)
	out.WriteString(wrapped)
	mem.FreeString(a, wrapped)
	mem.FreeString(a, canonical)
}

func canonicalDirective(a mem.Allocator, d dirOut, blocks *[]int) string {
	var name string
	switch d.Kind {
	case dirIf:
		name = "if"
	case dirElif:
		name = "elif"
	case dirElse:
		name = "else"
	case dirFor:
		name = "for"
	case dirWith:
		name = "with"
	case dirLet:
		name = "let"
	case dirInclude:
		name = "include"
	case dirRaw:
		name = "raw"
	case dirEnd:
		name = "end"
	case dirMatch:
		name = "match"
	case dirCase:
		name = "case"
	default:
		return ""
	}
	out := strings.Builder{}
	out.WriteString("@")
	if d.TrimLeft {
		out.WriteString("-")
	}
	out.WriteString(name)
	writeOperand := func(operand *expr.Expr) {
		out.WriteByte(' ')
		formatted := expr.Compact(a, operand)
		out.WriteString(formatted)
		mem.FreeString(a, formatted)
	}
	switch d.Kind {
	case dirEnd:
		if len(d.Args) != 0 {
			writeOperand(d.Args[0])
		} else if len(*blocks) != 0 {
			out.WriteByte(' ')
			out.WriteString(blockName((*blocks)[len(*blocks)-1]))
		}
	case dirFor:
		for _, param := range d.Args[0].Items {
			out.WriteByte(' ')
			out.WriteString(param.Text)
			if param.Rest {
				out.WriteString("...")
			}
		}
		out.WriteString(" in")
		writeOperand(d.Args[1])
		if len(d.Args) == 3 {
			out.WriteString(" sep")
			writeOperand(d.Args[2])
		}
	case dirLet:
		writeOperand(d.Args[0])
		out.WriteString(" =")
		writeOperand(d.Args[1])
	default:
		for _, operand := range d.Args {
			writeOperand(operand)
		}
	}
	if d.TrimRight {
		out.WriteString(" -")
	}
	text := cloneDocumentFormatText(a, out.String())
	out.Reset()
	if d.Kind == dirIf || d.Kind == dirFor || d.Kind == dirWith || d.Kind == dirMatch || d.Kind == dirRaw {
		*blocks = append(*blocks, d.Kind)
	} else if d.Kind == dirEnd && len(*blocks) != 0 {
		*blocks = (*blocks)[:len(*blocks)-1]
	}
	return text
}

func blockName(kind int) string {
	switch kind {
	case dirIf:
		return "if"
	case dirFor:
		return "for"
	case dirWith:
		return "with"
	case dirMatch:
		return "match"
	case dirRaw:
		return "raw"
	default:
		return ""
	}
}

func canonicalComment(a mem.Allocator, style string, directive string) string {
	out := strings.Builder{}
	switch style {
	case "html":
		out.WriteString("<!-- ")
	case "c":
		out.WriteString("/* ")
	case "powershell":
		out.WriteString("<# ")
	case "hash":
		out.WriteString("# ")
	case "dash":
		out.WriteString("-- ")
	case "semi":
		out.WriteString("; ")
	case "percent":
		out.WriteString("% ")
	case "batch":
		out.WriteString("REM ")
	}
	out.WriteString(directive)
	switch style {
	case "html":
		out.WriteString(" -->")
	case "c":
		out.WriteString(" */")
	case "powershell":
		out.WriteString(" #>")
	}
	result := cloneDocumentFormatText(a, out.String())
	out.Reset()
	return result
}
