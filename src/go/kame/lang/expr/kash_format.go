package expr

import (
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

func FormatKashControl(a mem.Allocator, e *Expr, indent string) string {
	b := strings.NewBuilder(a)
	writeKashControl(&b, e, indent, 0)
	text := sourceText(a, b.String())
	b.Free()
	return text
}

// FormatKashStatement formats graph structure without changing value formatting.
func FormatKashStatement(a mem.Allocator, e *Expr, indent string) string {
	b := strings.NewBuilder(a)
	writeKashStatement(&b, e, indent, 0)
	text := sourceText(a, b.String())
	b.Free()
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
	if e.Kind != CommandGraph || !kashNestedGraph(e) {
		writeExpr(b, e)
		return
	}
	writeKashGraph(b, e, indent, level)
}

func writeKashGraph(b *strings.Builder, e *Expr, indent string, level int) {
	for i := range e.Items {
		item := e.Items[i]
		if item.Kind == CommandInput {
			continue
		}
		if item.Kind == CommandMux {
			if i != 1 || e.Items[0].Kind != CommandInput {
				kashLine(b, indent, level+1)
			}
			b.WriteString("|(")
			writeMuxHead(b, item)
			if item.Text == "filter" && len(item.Body) != 0 {
				if len(item.Body[0].Items) == 0 || item.Body[0].Items[0].Kind != CommandInput {
					b.WriteString(" | ")
				}
				writeKashStatement(b, item.Body[0], indent, level+1)
			}
			b.WriteByte(')')
			continue
		}
		if i != 0 {
			if joinsWithSpace(e.Items, i) {
				b.WriteByte(' ')
			} else {
				kashLine(b, indent, level+1)
				b.WriteString("| ")
			}
		}
		if item.Kind == CommandGroup {
			groupLevel := level
			if i != 0 {
				groupLevel++
			}
			b.WriteByte('(')
			for j := range item.Items {
				if j != 0 {
					b.WriteByte(',')
				}
				kashLine(b, indent, groupLevel+1)
				writeKashStatement(b, item.Items[j], indent, groupLevel+1)
			}
			kashLine(b, indent, groupLevel)
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

func writeKashControl(b *strings.Builder, e *Expr, indent string, level int) {
	if e.Kind == KashFunction {
		for j := 0; j < level; j++ {
			b.WriteString(indent)
		}
		b.WriteString("defn ")
		b.WriteString(e.Text)
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
		for j := 0; j < level; j++ {
			b.WriteString(indent)
		}
		b.WriteString("match ")
		writeKashValue(b, e.Body[0])
		level++
		for i := 1; i < len(e.Body); i++ {
			b.WriteByte('\n')
			for j := 0; j < level; j++ {
				b.WriteString(indent)
			}
			b.WriteString(e.Body[i].Text)
		}
		b.WriteByte('\n')
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
			if e.Kind == KashMatch {
				writeExpr(b, branch.Items[0])
			} else if branch.Items[0].Kind == CommandTest {
				writeProcess(b, branch.Items[0])
			} else {
				writeExpr(b, branch.Items[0])
			}
		}
		writeKashBody(b, branch.Body, indent, level+1)
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
		for k := 0; k < level; k++ {
			b.WriteString(indent)
		}
		if statement.Kind == KashComment {
			b.WriteString(statement.Text)
		} else if statement.Kind == KashDefinition {
			if statement.Bool {
				b.WriteByte('(')
			}
			b.WriteString(statement.Text)
			for k := range statement.Parameters {
				b.WriteByte(' ')
				b.WriteString(statement.Parameters[k].Name)
				if statement.Parameters[k].Rest {
					b.WriteString("...")
				}
			}
			if statement.Bool {
				b.WriteByte(')')
			}
			b.WriteString(" = ")
			writeExpr(b, statement.Items[0])
		} else {
			writeKashStatement(b, statement, indent, level)
		}
	}
}

func writeKashValue(b *strings.Builder, e *Expr) {
	if e.Kind == Name || e.Kind == Reference || e.Kind == EnvironmentReference {
		b.WriteByte('$')
	}
	writeExpr(b, e)
}
