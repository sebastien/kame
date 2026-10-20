package cli

import (
	"kame/core"
	"solod.dev/so/io"
)

func writeTargetReport(out io.Writer, doc core.Value, styled bool) {
	entries := reportField(doc, "entries")
	groups := []string{"rules", "services", "tasks", "artifacts"}
	headings := []string{"Rules", "Services", "Tasks", "Artifacts"}
	tokens := []string{"target.rule", "target.service", "target.task", "target.artifact"}
	if len(entries.List) == 0 {
		io.WriteString(out, "  no entries\n")
	}
	for group := range groups {
		shown := false
		for pass := 0; pass < 2; pass++ {
			for i := range entries.List {
				entry := entries.List[i]
				if reportField(entry, "group").Text != groups[group] {
					continue
				}
				status := reportField(entry, "status").Text
				if groups[group] == "artifacts" {
					if (status == "") != (pass == 0) {
						continue
					}
				} else if pass != 0 {
					continue
				}
				if !shown {
					io.WriteString(out, "  ")
					Style(out, tokens[group], headings[group], styled)
					io.WriteString(out, "\n")
					shown = true
				}
				io.WriteString(out, "    ")
				reportText(out, reportField(entry, "name").Text, tokens[group], styled)
				if status != "" {
					label, token := status, "text.muted"
					if status == "built" {
						label, token = "ok", "status.success"
					}
					if status == "outdated" {
						label, token = "out of date", "message.warning"
					}
					if status == "missing" {
						token = "status.failed"
					}
					io.WriteString(out, "  ")
					Style(out, token, label, styled)
				}
				if summary := reportField(entry, "summary").Text; summary != "" {
					io.WriteString(out, "  ")
					reportText(out, summary, "text.muted", styled)
				}
				io.WriteString(out, "\n")
			}
		}
	}
}
