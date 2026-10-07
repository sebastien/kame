package cli

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

// Style is the hardcoded semantic theme. Exact command data never passes here.
func Style(out io.Writer, token string, text string, enabled bool) {
	sequence := ""
	if enabled {
		switch token {
		case "target.task":
			sequence = "\x1b[35m" // purple: named tasks
		case "worker.tool":
			sequence = "\x1b[33m" // gold: executable/operation
		case "path.basename":
			sequence = "\x1b[1m"
		case "path.directory":
			sequence = "\x1b[2m"
		case "heading", "value.target", "value.symbol", "value.tool", "value.option", "diagnostic.code":
			sequence = "\x1b[1m"
		case "value.path", "location", "message.info", "status.running", "progress.complete":
			sequence = "\x1b[36m"
		case "message.tip":
			sequence = "\x1b[1;36m"
		case "message.warning", "status.retrying":
			sequence = "\x1b[93m" // amber, 16-color terminal baseline
		case "status.cancelled":
			sequence = "\x1b[33m"
		case "message.error", "message.fatal", "status.failed":
			sequence = "\x1b[1;31m"
		case "status.success", "status.reused", "status.ready":
			sequence = "\x1b[32m"
		case "text.muted", "structure":
			sequence = "\x1b[2m"
		}
	}
	if sequence != "" {
		io.WriteString(out, sequence)
	}
	io.WriteString(out, text)
	if sequence != "" {
		io.WriteString(out, "\x1b[0m")
	}
}

// WriteReport renders the established machine document without rerunning a query
// or maintaining a second set of inspection structs. Data remains unchanged.
func WriteReport(out io.Writer, heading string, data string, styled bool) bool {
	var value core.Value
	if !core.ParseJSON(mem.System, []byte(data), &value) {
		return false
	}
	Style(out, "heading", heading, styled)
	io.WriteString(out, "\n")
	if value.Kind == core.List && len(value.List) == 0 {
		text := "no entries"
		if heading == "tools" {
			text = "no referenced tools"
		} else if heading == "cache list" {
			text = "no managed records"
		} else if len(heading) > 7 && heading[:7] == "inputs " {
			text = "no inputs"
		} else if len(heading) > 8 && heading[:8] == "outputs " {
			text = "no outputs"
		}
		io.WriteString(out, "  "+text+"\n")
		value.Free(mem.System)
		return true
	}
	writeReportValue(out, value, "", 1, styled, true)
	value.Free(mem.System)
	return true
}

func writeReportValue(out io.Writer, value core.Value, label string, depth int, styled bool, root bool) {
	if value.Kind == core.Record {
		if label != "" {
			reportIndent(out, depth)
			Style(out, "value.symbol", label, styled)
			io.WriteString(out, "\n")
			depth++
		}
		for i := range value.Record {
			field := value.Record[i]
			if root && (field.Key == "schema" || field.Key == "type") {
				continue
			}
			writeReportValue(out, field.Value, field.Key, depth, styled, false)
		}
		return
	}
	if value.Kind == core.List {
		if label != "" {
			reportIndent(out, depth)
			Style(out, "value.symbol", label, styled)
			io.WriteString(out, "\n")
			depth++
		}
		if len(value.List) == 0 {
			reportIndent(out, depth)
			io.WriteString(out, "(none)\n")
		}
		for i := range value.List {
			writeReportValue(out, value.List[i], "", depth, styled, false)
		}
		return
	}
	reportIndent(out, depth)
	if label != "" {
		Style(out, "value.symbol", label, styled)
		io.WriteString(out, ": ")
	}
	text := eval.Display(mem.System, value)
	token := "value.literal"
	if value.Kind == core.String {
		plain := value.Text != "" && (label == "source" || label == "path" || label == "target" || label == "name" || label == "kind" || label == "freshness" || label == "backend" || label == "key" || label == "")
		for i := range value.Text {
			if value.Text[i] <= ' ' || value.Text[i] == 127 {
				plain = false
				break
			}
		}
		if plain {
			mem.FreeString(mem.System, text)
			text = cloneText(value.Text)
		}
		if label == "path" && value.Text == "" {
			mem.FreeString(mem.System, text)
			text = cloneText("unavailable")
		}
		if label == "source" || label == "path" || label == "target" || label == "" {
			token = "value.path"
		}
	}
	Style(out, token, text, styled)
	io.WriteString(out, "\n")
	mem.FreeString(mem.System, text)
}

func reportIndent(out io.Writer, depth int) {
	for i := 0; i < depth; i++ {
		io.WriteString(out, "  ")
	}
}
