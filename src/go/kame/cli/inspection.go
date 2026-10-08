package cli

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

func reportField(value core.Value, key string) core.Value {
	for i := range value.Record { if value.Record[i].Key == key { return value.Record[i].Value } }
	return core.Value{}
}

func reportID(values core.Value, id int64) core.Value {
	for i := range values.List { if reportField(values.List[i], "id").Int == id { return values.List[i] } }
	return core.Value{}
}

func reportHas(values core.Value, text string) bool {
	for i := range values.List { if values.List[i].Text == text { return true } }
	return false
}

func reportText(out io.Writer, text string, token string, styled bool) {
	plain := true
	for i := range text { if text[i] < 32 || text[i] == 127 { plain = false; break } }
	if plain { Style(out, token, text, styled); return }
	escaped := eval.Display(mem.System, core.Value{Kind: core.String, Text: text})
	Style(out, token, escaped, styled)
	mem.FreeString(mem.System, escaped)
}

func reportReferences(resource core.Value, producers core.Value, dependencies core.Value) int {
	count := 0
	for i := range producers.List {
		id := reportField(producers.List[i], "id").Int
		if reportField(resource, "producer").Int == id {
			count++
			continue
		}
		for j := range dependencies.List {
			d := dependencies.List[j]
			if reportField(d, "resource").Int == reportField(resource, "id").Int && reportField(d, "producer").Int == id {
				count++
				break
			}
		}
	}
	return count
}

func reportBoundaryPath(boundary, resources, producers core.Value) string {
	r := reportID(resources, reportField(boundary, "resource").Int)
	if r.Kind == core.Nil {
		p := reportID(producers, reportField(boundary, "producer").Int)
		r = reportID(resources, reportField(p, "resource").Int)
	}
	return reportField(r, "display").Text
}

func writeInspectionReport(out io.Writer, doc core.Value, styled bool) {
	resources, producers := reportField(doc, "resources"), reportField(doc, "producers")
	dependencies, deferred := reportField(doc, "dependencies"), reportField(doc, "deferred")
	kind, depth := reportField(doc, "type").Text, reportField(doc, "depth").Int
	io.WriteString(out, "  scope: declared and read-only resolved\n")
	for section := 0; section < 3; section++ {
		label := "file inputs"
		if kind == "outputs" { label = "artifacts" } else if kind == "plan" { label = "files" }
		if section == 1 { label = "configuration" }
		if section == 2 { label = "logical / value / service resources" }
		io.WriteString(out, "  "); Style(out, "heading", label, styled); io.WriteString(out, "\n")
		count := 0
		if depth != 0 { for i := range resources.List {
			r := resources.List[i]
			roles := reportField(r, "roles")
			isFile := reportField(reportField(r, "key"), "kind").Text == "file"
			config := reportHas(roles, "configuration")
			include := (section == 1 && config) || (section == 2 && !isFile && !config)
			if section == 0 && isFile && !config { include = kind == "plan" || (kind == "inputs" && reportHas(roles, "input")) || (kind == "outputs" && reportHas(roles, "artifact")) }
			if !include { continue }
			count++
			io.WriteString(out, "    "); reportText(out, reportField(r, "display").Text, "value.path", styled)
			fmt.Fprintf(out, " [%d]", reportReferences(r, producers, dependencies))
			if status := reportField(r, "status").Text; status != "" { io.WriteString(out, " · "+status) }
			io.WriteString(out, "\n")
		} }
		if count == 0 {
			if section == 0 && reportField(doc, "truncated").Bool { io.WriteString(out, "    not expanded at requested depth\n") } else if section == 0 && len(deferred.List) != 0 { io.WriteString(out, "    unresolved discovery; see boundaries below\n") } else if section == 0 && kind == "inputs" { io.WriteString(out, "    no inputs\n") } else if section == 0 && kind == "outputs" { io.WriteString(out, "    no declared artifacts\n") } else { io.WriteString(out, "    (none)\n") }
		}
	}
	if kind == "plan" {
		io.WriteString(out, "  producers\n")
		for i := range producers.List {
			p := producers.List[i]
			io.WriteString(out, "    "); reportText(out, reportField(p, "target").Text, "value.target", styled); io.WriteString(out, " · "+reportField(p, "kind").Text+"\n")
			for j := range p.Record {
				field := p.Record[j]
				if field.Key == "id" || field.Key == "resource" || field.Key == "target" || field.Key == "kind" || field.Key == "outputs" { continue }
				writeReportValue(out, field.Value, field.Key, 3, styled, false)
			}
		}
		io.WriteString(out, "  stages · dependency ordering, not global barriers")
		if reportField(doc, "truncated").Bool || len(deferred.List) != 0 { io.WriteString(out, " · partial") }
		io.WriteString(out, "\n")
		stages := reportField(doc, "stages")
		for i := range stages.List {
			s := stages.List[i]; members := reportField(s, "producers")
			fmt.Fprintf(out, "    stage %d", reportField(s, "number").Int)
			if len(members.List) > 1 { io.WriteString(out, " · parallel eligible") }
			io.WriteString(out, "\n")
			for j := range members.List { p := reportID(producers, members.List[j].Int); io.WriteString(out, "      "); reportText(out, reportField(p, "target").Text, "value.target", styled); io.WriteString(out, "\n") }
		}
		for i := range dependencies.List {
			d := dependencies.List[i]
			if reportField(d, "group").Int <= 1 { continue }
			p := reportID(producers, reportField(d, "producer").Int)
			io.WriteString(out, "    sequence barrier: "); reportText(out, reportField(p, "target").Text, "value.target", styled)
			fmt.Fprintf(out, " · group %d follows earlier groups\n", reportField(d, "group").Int)
		}
	}
	if reportField(doc, "truncated").Bool { io.WriteString(out, "  truncated: deeper producer inputs omitted\n") }
	if len(deferred.List) != 0 {
		io.WriteString(out, "  discovery boundaries\n")
		for i := range deferred.List {
			path := reportBoundaryPath(deferred.List[i], resources, producers)
			seen := false
			for j := 0; j < i; j++ { if reportBoundaryPath(deferred.List[j], resources, producers) == path { seen = true; break } }
			if seen { continue }
			io.WriteString(out, "    "); reportText(out, path, "value.path", styled)
			for j := i; j < len(deferred.List); j++ {
				d := deferred.List[j]
				if reportBoundaryPath(d, resources, producers) != path { continue }
				reason := reportField(d, "reason").Text
				seen = false
				for k := i; k < j; k++ { if reportBoundaryPath(deferred.List[k], resources, producers) == path && reportField(deferred.List[k], "reason").Text == reason { seen = true; break } }
				if !seen { io.WriteString(out, " · "+reason) }
			}
			io.WriteString(out, "\n")
		}
	}
}
