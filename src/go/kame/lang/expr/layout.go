package expr

import (
	"kame/internal/mem"
	"strings"
)

// Columns measures canonical source columns, not UTF-8 bytes.
func Columns(text string, column int) int {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\t' {
			column += 8 - column%8
		} else if c == '\n' {
			column = 0
		} else if c&0xc0 != 0x80 {
			column++
		}
	}
	return column
}

func Fits(text string, column int) bool {
	return !strings.Contains(text, "\n") && Columns(text, column) <= 80
}

// FormatAt lays out an expression at its actual starting column.
func FormatAt(a mem.Allocator, e *Expr, column int) string {
	b := strings.Builder{}
	writeLayout(a, &b, e, column, column, 0)
	text := sourceText(a, restoreTrivia(e, b.String()))
	b.Reset()
	return text
}

func layoutLine(b *strings.Builder, indent int) {
	b.WriteByte('\n')
	for i := 0; i < indent; i++ {
		b.WriteByte(' ')
	}
}

func inlineFits(a mem.Allocator, e *Expr, column int, tail int) bool {
	// ponytail: remeasure subtrees; cache widths if deeply nested sources make this costly.
	text := Compact(a, e)
	fit := !strings.Contains(text, "\n") && Columns(text, column)+tail <= 80
	mem.FreeString(a, text)
	return fit
}

func writeDelimitedValue(a mem.Allocator, b *strings.Builder, e *Expr, column, indent, tail int) {
	if e == nil || e.Kind != Application || !e.Juxtaposed || len(e.Items) <= 1 || inlineFits(a, e, column, tail) {
		writeLayout(a, b, e, column, indent, tail)
		return
	}
	writeLayout(a, b, e.Items[0], column, indent, 0)
	next := indent/8 + 1
	for i, arg := range e.Items[1:] {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("\t", next))
		argTail := 0
		if i == len(e.Items)-2 {
			argTail = tail
		}
		writeLayout(a, b, arg, next*8, next*8, argTail)
	}
}

// An undelimited application can break only within an authored delimiter.
// Try those operands from right to left and measure the lines they produce.
func writeJuxtaposition(a mem.Allocator, b *strings.Builder, e *Expr, column, indent, tail int) {
	if len(e.Items) == 1 {
		writeLayout(a, b, e.Items[0], column, indent, tail+3)
		b.WriteString(" ()")
		return
	}
	inline := make([]string, len(e.Items))
	parts := make([]string, len(e.Items))
	columns := make([]int, len(e.Items))
	start := column
	for i, item := range e.Items {
		columns[i] = start
		inline[i] = Compact(a, item)
		parts[i] = inline[i]
		start = Columns(inline[i], start) + 1
	}
	expanded := make([]bool, len(e.Items))
	for i := len(e.Items) - 1; i >= 0; i-- {
		item := e.Items[i]
		delimiter := item.Kind
		if delimiter == ListSpread {
			delimiter = item.Items[0].Kind
		}
		switch delimiter {
		case Group, Section, List, Record:
		default:
			continue
		}
		if !strings.Contains(inline[i], "\n") && applicationLinesFit(parts, column, tail) {
			continue
		}
		var part strings.Builder
		writeLayout(a, &part, item, columns[i], indent, max(0, 81-Columns(inline[i], columns[i])))
		parts[i] = part.String()
		expanded[i] = true
	}
	for i, item := range e.Items {
		if i != 0 {
			b.WriteByte(' ')
			column++
		}
		from := b.Len()
		if expanded[i] && column != columns[i] {
			writeLayout(a, b, item, column, indent, max(0, 81-Columns(inline[i], column)))
		} else {
			b.WriteString(parts[i])
		}
		column = Columns(b.String()[from:], column)
		mem.FreeString(a, inline[i])
	}
}

func applicationLinesFit(parts []string, column, tail int) bool {
	for i, part := range parts {
		if i != 0 {
			column++
		}
		for {
			line, rest, multiline := strings.Cut(part, "\n")
			column = Columns(line, column)
			if column > 80 {
				return false
			}
			if !multiline {
				break
			}
			column, part = 0, rest
		}
	}
	return column+tail <= 80
}

func writeLayout(a mem.Allocator, b *strings.Builder, e *Expr, column int, indent int, tail int) {
	if e == nil {
		return
	}
	if e.Kind == Match && len(e.Body) != 0 {
		writeMatch(a, b, e, column, indent)
		return
	}
	if inlineFits(a, e, column, tail) {
		writeExpr(b, e)
		return
	}
	if e.Infix != "" {
		level := infixPrecedence(e.Infix)
		next := indent/8 + 1
		var writeChain func(*Expr, int, int, int)
		writeChain = func(part *Expr, column, indent, tail int) {
			if infixPrecedence(part.Infix) != level {
				if part.Infix != "" {
					writeExpr(b, part)
				} else {
					writeLayout(a, b, part, column, indent, tail)
				}
				return
			}
			writeChain(part.Items[1], column, indent, 0)
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("\t", next))
			b.WriteString(part.Infix)
			b.WriteByte(' ')
			writeChain(part.Items[2], next*8+len(part.Infix)+1, next*8, tail)
		}
		writeChain(e, column, indent, tail)
		return
	}
	if e.Kind == ValuePipe {
		writeLayout(a, b, e.Items[0], column, indent, 0)
		next := indent/8 + 1
		for i := 1; i < len(e.Items); i++ {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("\t", next))
			b.WriteString("|> ")
			stageTail := 0
			if i+1 == len(e.Items) {
				stageTail = tail
			}
			writeLayout(a, b, e.Items[i], next*8+3, next*8, stageTail)
		}
		return
	}
	if e.Kind == Section || (e.Kind == Group && len(e.Items) == 1) {
		var child *Expr
		if e.Kind == Section {
			child = e.Body[0]
		} else {
			child = e.Items[0]
		}
		b.WriteByte('(')
		if child != nil && child.Kind == Conditional && len(child.Items) >= 3 {
			next := strings.Repeat("\t", indent/8+1)
			b.WriteString("if ")
			writeExpr(b, child.Items[0])
			b.WriteString(" then ")
			writeExpr(b, child.Items[1])
			for i := 2; i+1 < len(child.Items); i += 2 {
				b.WriteByte('\n')
				b.WriteString(next)
				b.WriteString("elif ")
				writeExpr(b, child.Items[i])
				b.WriteString(" then ")
				writeExpr(b, child.Items[i+1])
			}
			b.WriteByte('\n')
			b.WriteString(next)
			b.WriteString("else ")
			writeExpr(b, child.Items[len(child.Items)-1])
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("\t", indent/8))
			b.WriteByte(')')
			return
		}
		if child != nil && child.Kind == Lambda && child.Arrow && len(child.Body) != 0 {
			writeLambdaParameters(b, child.Parameters)
			b.WriteString(" =>")
			child = child.Body[0]
		}
		next := indent/8 + 1
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("\t", next))
		writeDelimitedValue(a, b, child, next*8, next*8, 0)
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("\t", indent/8))
		b.WriteByte(')')
		return
	}
	if e.Kind == List {
		b.WriteByte('[')
		next := indent - indent%8 + 8
		for _, item := range e.Items {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("\t", next/8))
			writeDelimitedValue(a, b, item, next, next, 1)
			b.WriteByte(',')
		}
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("\t", indent/8))
		b.WriteByte(']')
		return
	}
	if e.Kind == ListSpread {
		b.WriteString("...")
		writeLayout(a, b, e.Items[0], column+3, indent+3, tail)
		return
	}
	if e.Kind == Record {
		b.WriteByte('{')
		next := indent/8 + 1
		for _, field := range e.Fields {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("\t", next))
			switch {
			case field.Spread:
				b.WriteString("...")
				writeDelimitedValue(a, b, field.Value, next*8+3, next*8, 1)
			case field.Shorthand:
				writeBraceKey(b, field)
			default:
				start := b.Len()
				writeBraceKey(b, field)
				b.WriteByte(':')
				valueColumn := Columns(b.String()[start:], next*8) + 1
				if inlineFits(a, field.Value, valueColumn, 1) {
					b.WriteByte(' ')
					writeExpr(b, field.Value)
				} else {
					b.WriteByte('\n')
					b.WriteString(strings.Repeat("\t", next+1))
					writeDelimitedValue(a, b, field.Value, (next+1)*8, (next+1)*8, 1)
				}
			}
			b.WriteByte(',')
		}
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("\t", indent/8))
		b.WriteByte('}')
		return
	}
	if e.Kind == Lambda {
		if e.Arrow {
			writeExpr(b, e)
			return
		}
		b.WriteString("([")
		parameterWidth := column + 3 // opening delimiters and closing bracket
		if len(e.Body) == 0 {
			parameterWidth += tail + 1
		}
		for i := range e.Parameters {
			if i != 0 {
				parameterWidth++
			}
			parameterWidth = Columns(e.Parameters[i].Name, parameterWidth)
			if e.Parameters[i].Rest {
				parameterWidth += 3
			}
		}
		for i := range e.Parameters {
			if parameterWidth > 80 {
				layoutLine(b, indent+2)
			} else if i != 0 {
				b.WriteByte(' ')
			}
			b.WriteString(e.Parameters[i].Name)
			if e.Parameters[i].Rest {
				b.WriteString("...")
			}
		}
		b.WriteByte(']')
		for i := range e.Body {
			layoutLine(b, indent+2)
			childTail := 0
			if i+1 == len(e.Body) {
				childTail = tail + 1
			}
			writeLayout(a, b, e.Body[i], indent+2, indent+2, childTail)
		}
		b.WriteByte(')')
		return
	}
	if e.Kind == Application && len(e.Items) != 0 {
		if e.Juxtaposed {
			writeJuxtaposition(a, b, e, column, indent, tail)
			return
		}
		b.WriteByte('(')
		operatorTail := 0
		if len(e.Items) == 1 {
			operatorTail = tail + 1
		}
		writeLayout(a, b, e.Items[0], column+1, indent, operatorTail)
		operator := e.Items[0]
		for i := 1; i < len(e.Items); i++ {
			childIndent := indent + 2
			if operator.Kind == Name && operator.Text == "if" && i%2 == 0 {
				childIndent += 2
			}
			layoutLine(b, childIndent)
			childTail := 0
			if i+1 == len(e.Items) {
				childTail = tail + 1
			}
			writeLayout(a, b, e.Items[i], childIndent, childIndent, childTail)
		}
		b.WriteByte(')')
		return
	}
	// Literal content and Kash process syntax are indivisible here.
	writeExpr(b, e)
}
