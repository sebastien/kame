package expr

import (
	"solod.dev/so/mem"
	"solod.dev/so/strings"
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
	b := strings.NewBuilder(a)
	writeLayout(a, &b, e, column, column, 0)
	text := sourceText(a, b.String())
	b.Free()
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

func writeBindings(a mem.Allocator, b *strings.Builder, e *Expr, column int, tail int) {
	if e.Kind != List || len(e.Items)%2 != 0 || inlineFits(a, e, column, tail) {
		writeLayout(a, b, e, column, column, tail)
		return
	}
	b.WriteByte('[')
	for i := 0; i < len(e.Items); i += 2 {
		if i != 0 {
			layoutLine(b, column+1)
		}
		name := Compact(a, e.Items[i])
		pairTail := 0
		if i+2 == len(e.Items) {
			pairTail = tail + 1
		}
		writePairedValue(a, b, name, "", e.Items[i+1], column+1, pairTail)
		mem.FreeString(a, name)
	}
	b.WriteByte(']')
}

func writePairedValue(a mem.Allocator, b *strings.Builder, name string, suffix string, value *Expr, column int, tail int) {
	b.WriteString(name)
	b.WriteString(suffix)
	valueColumn := Columns(suffix, Columns(name, column)) + 1
	if inlineFits(a, value, valueColumn, tail) {
		b.WriteByte(' ')
		writeExpr(b, value)
	} else {
		layoutLine(b, column+2)
		writeLayout(a, b, value, column+2, column+2, tail)
	}
}

func writeClause(a mem.Allocator, b *strings.Builder, e *Expr, column int, tail int) {
	if e.Kind != List || len(e.Items) != 2 || inlineFits(a, e, column, tail) {
		writeLayout(a, b, e, column, column, tail)
		return
	}
	b.WriteByte('[')
	writeLayout(a, b, e.Items[0], column+1, column, 0)
	layoutLine(b, column+2)
	writeLayout(a, b, e.Items[1], column+2, column+2, tail+1)
	b.WriteByte(']')
}

func writeLayout(a mem.Allocator, b *strings.Builder, e *Expr, column int, indent int, tail int) {
	if e == nil {
		return
	}
	if inlineFits(a, e, column, tail) {
		writeExpr(b, e)
		return
	}
	if e.Kind == List {
		b.WriteByte('[')
		for i := range e.Items {
			layoutLine(b, indent+2)
			childTail := 0
			if i+1 == len(e.Items) {
				childTail = tail + 1
			}
			writeLayout(a, b, e.Items[i], indent+2, indent+2, childTail)
		}
		b.WriteByte(']')
		return
	}
	if e.Kind == Record {
		b.WriteByte('[')
		for i := range e.Fields {
			layoutLine(b, indent+2)
			childTail := 0
			if i+1 == len(e.Fields) {
				childTail = tail + 1
			}
			writePairedValue(a, b, e.Fields[i].Key, ":", e.Fields[i].Value, indent+2, childTail)
		}
		b.WriteByte(']')
		return
	}
	if e.Kind == Lambda {
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
			if operator.Kind == Name && operator.Text == "let" && i == 1 {
				writeBindings(a, b, e.Items[i], childIndent, childTail)
			} else if operator.Kind == Name && operator.Text == "match" && i > 1 {
				writeClause(a, b, e.Items[i], childIndent, childTail)
			} else {
				writeLayout(a, b, e.Items[i], childIndent, childIndent, childTail)
			}
		}
		b.WriteByte(')')
		return
	}
	if e.Kind == Section {
		b.WriteByte('(')
		writeLayout(a, b, e.Body[0], column+1, indent, tail+1)
		b.WriteByte(')')
		return
	}
	// Literal content and Kash process syntax are indivisible here.
	writeExpr(b, e)
}
