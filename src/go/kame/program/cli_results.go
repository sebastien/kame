package program

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/unicode/utf8"
)

func WriteInvocation(out io.Writer, command string, dryRun bool) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("invocation-started")
	e.Str("command")
	e.Str(command)
	if dryRun {
		e.Str("dryRun")
		e.Bool(true)
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func (p *Program) WriteToolsCheckResult(out io.Writer, target string, uses []ToolUse) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("tools-check-result")
	e.Str("target")
	e.Str(target)
	e.Str("tools")
	e.BeginArray()
	for i := range uses {
		e.BeginObject()
		e.Str("name")
		e.Str(uses[i].Name)
		e.Str("available")
		e.Bool(p.ResolveTool(uses[i].Name) != "")
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func WriteWatchTransition(out io.Writer, kind string, cycle int, status string, elapsedMS int64, completed int, failed int, cancelled int) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str(kind)
	e.Str("cycle")
	e.Int(int64(cycle))
	if status != "" {
		e.Str("status")
		e.Str(status)
	}
	if kind == "watch-cycle-finished" {
		e.Str("elapsedMS")
		e.Int(elapsedMS)
		e.Str("completed")
		e.Int(int64(completed))
		e.Str("failed")
		e.Int(int64(failed))
		e.Str("cancelled")
		e.Int(int64(cancelled))
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func WriteSummary(out io.Writer, command string, status int, different bool, elapsedMS int64, completed int, failed int, cancelled int) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("summary")
	e.Str("command")
	e.Str(command)
	e.Str("status")
	if different {
		e.Str("different")
	} else if status >= 128 || (cancelled != 0 && failed == 0 && status != 0) {
		e.Str("cancelled")
	} else if status != 0 {
		e.Str("failure")
	} else {
		e.Str("success")
	}
	e.Str("exitStatus")
	e.Int(int64(status))
	e.Str("elapsedMS")
	e.Int(elapsedMS)
	if completed >= 0 {
		e.Str("completed")
		e.Int(int64(completed))
		e.Str("failed")
		e.Int(int64(failed))
		e.Str("cancelled")
		e.Int(int64(cancelled))
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

// WriteDataResult encodes exact command output; it shares stream encoding policy.
func WriteDataResult(out io.Writer, kind string, source string, target string, action string, changed bool, data []byte, includeData bool) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str(kind)
	if source != "" {
		e.Str("source")
		e.Str(source)
	}
	if target != "" {
		e.Str("target")
		e.Str(target)
	}
	if action != "" {
		e.Str("action")
		e.Str(action)
	}
	if kind == "format-result" {
		e.Str("changed")
		e.Bool(changed)
	}
	if includeData {
		e.Str("data")
		if utf8.Valid(data) {
			e.Str(string(data))
			e.Str("encoding")
			e.Str("utf-8")
		} else {
			encoded := base64Text(mem.System, data)
			e.Str(encoded)
			mem.FreeString(mem.System, encoded)
			e.Str("encoding")
			e.Str("base64")
		}
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}
