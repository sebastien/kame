package expr

import (
	"kame/internal/mem"
	"strings"
)

func FormatKashControl(a mem.Allocator, e *Expr, indent string) string {
	b := strings.Builder{}
	writeKashControl(&b, e, indent, 0)
	text := sourceText(a, b.String())
	b.Reset()
	return text
}

// FormatKashStatement formats graph structure without changing value formatting.
func FormatKashStatement(a mem.Allocator, e *Expr, indent string) string {
	b := strings.Builder{}
	writeKashStatement(&b, e, indent, 0)
	text := sourceText(a, b.String())
	b.Reset()
	return text
}

func kashNestedGraph(e *Expr) bool {
	for i := range e.Items {
		if e.Items[i].Kind == CommandGroup || (e.Items[i].Kind == CommandMux && e.Items[i].Text == "filter") {
			return true
		}
	}
	return false
}

func kashLine(b *strings.Builder, indent string, level int) {
	b.WriteByte('\n')
	for i := 0; i < level; i++ {
		b.WriteString(indent)
	}
}

func writeKashStatement(b *strings.Builder, e *Expr, indent string, level int) {
	if (e.Kind == KashLet || e.Kind == KashSet) && indent == "\t" {
		if e.Kind == KashLet {
			b.WriteString("let ")
			b.WriteString(e.Text)
		} else {
			b.WriteString("set ")
			writeKashSetTarget(b, e)
		}
		writeKashBindingValue(b, e.Items[0], indent, level)
		return
	}
	if e.Kind != CommandGraph || !kashNestedGraph(e) {
		writeExpr(b, e)
		return
	}
	writeKashGraph(b, e, indent, level)
}

func writeKashSetTarget(b *strings.Builder, e *Expr) {
	b.WriteString(e.Text)
	if len(e.Items) == 2 {
		b.WriteString("${")
		writeExpr(b, e.Items[1])
		b.WriteByte('}')
	}
}

func writeKashBindingValue(b *strings.Builder, value *Expr, indent string, level int) {
	b.WriteString(" =")
	if indent != "\t" {
		b.WriteByte(' ')
		writeExpr(b, value)
		return
	}
	line := b.String()[strings.LastIndexByte(b.String(), '\n')+1:]
	column := Columns(line, 0) + 1
	if value.Kind != Match && !inlineFits(nil, value, column, 0) {
		level++
		kashLine(b, indent, level)
		column = level * 8
	} else {
		b.WriteByte(' ')
	}
	formatted := FormatAtIndent(nil, value, column, level*8)
	b.WriteString(formatted)
	mem.FreeString(nil, formatted)
}

func writeKashGraph(b *strings.Builder, e *Expr, indent string, level int) {
	for i := range e.Items {
		item := e.Items[i]
		if item.Kind == CommandInput {
			continue
		}
		if item.Kind == CommandMux {
			if i != 1 || e.Items[0].Kind != CommandInput {
				b.WriteByte(' ')
			}
			b.WriteString("|(")
			writeMuxHead(b, item)
			if item.Text == "filter" && len(item.Body) != 0 {
				if len(item.Body[0].Items) == 0 || item.Body[0].Items[0].Kind != CommandInput {
					b.WriteString(" |")
				}
				kashLine(b, indent, level+1)
				writeKashGraph(b, item.Body[0], indent, level+1)
				kashLine(b, indent, level)
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
		if item.Kind == CommandGroup {
			b.WriteByte('(')
			for j := range item.Items {
				if j != 0 {
					b.WriteByte(',')
				}
				kashLine(b, indent, level+1)
				writeKashStatement(b, item.Items[j], indent, level+1)
			}
			kashLine(b, indent, level)
			b.WriteByte(')')
		} else {
			writeExpr(b, item)
		}
		writeWriterAlias(b, e.Items, i)
	}
	if e.AcceptExit {
		b.WriteString(" ?")
	}
	if len(e.Body) != 0 {
		b.WriteString(" ? ")
		writeKashStatement(b, e.Body[0], indent, level)
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

func writeKashControl(b *strings.Builder, e *Expr, indent string, level int) {
	if e.Kind == KashWith {
		for j := 0; j < level; j++ {
			b.WriteString(indent)
		}
		b.WriteString("with")
		for i := range e.Items {
			b.WriteByte(' ')
			writeExpr(b, e.Items[i])
		}
		writeKashBody(b, e.Body, indent, level+1)
		return
	}
	if e.Kind == ValueBlock {
		if e.Text != "" {
			b.WriteByte(' ')
			b.WriteString(e.Text)
		}
		writeKashBody(b, e.Body, indent, level+1)
		return
	}
	if e.Kind == KashFunction {
		for j := 0; j < level; j++ {
			b.WriteString(indent)
		}
		b.WriteString("def ")
		b.WriteString(e.Text)
		if len(e.Parameters) == 0 {
			b.WriteString(" ()")
		}
		for i := range e.Parameters {
			b.WriteByte(' ')
			b.WriteString(e.Parameters[i].Name)
			if e.Parameters[i].Rest {
				b.WriteString("...")
			}
		}
		writeKashBody(b, e.Body, indent, level+1)
		return
	}
	if e.Kind == KashMatch {
		writeKashMatch(b, e, indent, level)
		return
	}
	for i := range e.Items {
		branch := e.Items[i]
		if i != 0 {
			b.WriteByte('\n')
		}
		for j := 0; j < level; j++ {
			b.WriteString(indent)
		}
		b.WriteString(branch.Text)
		if len(branch.Items) != 0 {
			b.WriteByte(' ')
			if branch.Items[0].Kind == CommandTest {
				writeProcess(b, branch.Items[0])
			} else {
				writeExpr(b, branch.Items[0])
			}
		}
		if e.Kind == KashRecords {
			b.WriteByte(' ')
			b.WriteString(e.RecordFrame)
		}
		writeKashBody(b, branch.Body, indent, level+1)
	}
}

func writeKashMatch(b *strings.Builder, e *Expr, indent string, level int) {
	b.WriteString(strings.Repeat(indent, level))
	b.WriteString("match ")
	writeExpr(b, e.Body[0])
	comment := 1
	for _, arm := range e.Items {
		for comment < len(e.Body) && e.Body[comment].Span.Start < arm.Span.Start {
			kashLine(b, indent, e.Body[comment].CommentIndent)
			b.WriteString(e.Body[comment].Text)
			comment++
		}
		kashLine(b, indent, level+1)
		if len(arm.Items) == 0 {
			b.WriteString("else")
		} else {
			writeExpr(b, arm.Items[0])
		}
		b.WriteString(" =>")
		if arm.Bool {
			b.WriteByte(' ')
			writeExpr(b, arm.Body[0])
		} else {
			writeKashBody(b, arm.Body, indent, level+2)
		}
	}
	for ; comment < len(e.Body); comment++ {
		kashLine(b, indent, e.Body[comment].CommentIndent)
		b.WriteString(e.Body[comment].Text)
	}
}

func writeKashBody(b *strings.Builder, body []*Expr, indent string, level int) {
	for j := range body {
		statement := body[j]
		b.WriteByte('\n')
		if IsKashControl(statement.Kind) {
			writeKashControl(b, statement, indent, level)
			continue
		}
		if statement.Kind == KashDefinition && statement.Items[0].Kind == KashFunction {
			writeKashControl(b, statement.Items[0], indent, level)
			continue
		}
		depth := level
		if statement.Kind == KashComment {
			depth = statement.CommentIndent
		}
		for k := 0; k < depth; k++ {
			b.WriteString(indent)
		}
		if statement.Kind == KashComment {
			b.WriteString(statement.Text)
		} else if statement.Kind == KashDefinition {
			if statement.Bool || statement.ExplicitDef {
				b.WriteString("def ")
			}
			b.WriteString(statement.Text)
			if statement.Bool && len(statement.Parameters) == 0 {
				b.WriteString(" ()")
			}
			for k := range statement.Parameters {
				b.WriteByte(' ')
				b.WriteString(statement.Parameters[k].Name)
				if statement.Parameters[k].Rest {
					b.WriteString("...")
				}
			}
			writeKashBindingValue(b, statement.Items[0], indent, level)
		} else {
			writeKashStatement(b, statement, indent, level)
		}
	}
}

func writeKashValue(b *strings.Builder, e *Expr) {
	if e.Kind == Name || e.Kind == Reference || e.Kind == EnvironmentReference {
		b.WriteByte('$')
		head := e.Text
		if len(e.Reference) != 0 {
			head = e.Reference[0].Text
		}
		if strings.Contains(head, "/") {
			b.WriteByte('{')
			writeExpr(b, e)
			b.WriteByte('}')
			return
		}
	}
	writeExpr(b, e)
}
