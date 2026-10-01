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

func writeKashControl(b *strings.Builder, e *Expr, indent string, level int) {
	if e.Kind == KashMatch {
		for j := 0; j < level; j++ { b.WriteString(indent) }
		b.WriteString("match ")
		if e.Body[0].Kind == CommandCapture { writeExpr(b, e.Body[0]) } else { b.WriteString("@("); writeExpr(b, e.Body[0]); b.WriteByte(')') }
		level++
		for i := 1; i < len(e.Body); i++ { b.WriteByte('\n'); for j := 0; j < level; j++ { b.WriteString(indent) }; b.WriteString(e.Body[i].Text) }
		b.WriteByte('\n')
	}
	for i := range e.Items {
		branch := e.Items[i]
		if i != 0 { b.WriteByte('\n') }
		for j := 0; j < level; j++ { b.WriteString(indent) }
		b.WriteString(branch.Text)
		if len(branch.Items) != 0 {
			b.WriteByte(' ')
			if e.Kind == KashMatch { writeExpr(b, branch.Items[0]) } else if branch.Items[0].Kind == CommandTest { writeProcess(b, branch.Items[0]) } else { b.WriteString("@("); writeExpr(b, branch.Items[0]); b.WriteByte(')') }
		}
		for j := range branch.Body {
			statement := branch.Body[j]
			b.WriteByte('\n')
			if statement.Kind == KashIf || statement.Kind == KashMatch { writeKashControl(b, statement, indent, level+1); continue }
			for k := 0; k <= level; k++ { b.WriteString(indent) }
			if statement.Kind == KashComment { b.WriteString(statement.Text) } else if statement.Kind == KashDefinition {
				if statement.Bool { b.WriteByte('(') }
				b.WriteString(statement.Text)
				for k := range statement.Parameters { b.WriteByte(' '); b.WriteString(statement.Parameters[k].Name); if statement.Parameters[k].Rest { b.WriteString("...") } }
				if statement.Bool { b.WriteByte(')') }
				b.WriteString(" = ")
				writeExpr(b, statement.Items[0])
			} else { writeExpr(b, statement) }
		}
	}
}
