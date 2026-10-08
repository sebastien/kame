package cli

import (
	"kame/lang/eval"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
)

// WriteJSON serializes a parsed Invocation as the schema-1 object the
// JavaScript wrapper consumes. Field names are camelCase.
func WriteJSON(out io.Writer, inv *Invocation) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("name")
	e.Str(inv.Name)
	e.Str("command")
	e.Str(inv.Command)
	e.Str("file")
	e.Str(inv.File)
	e.Str("directory")
	e.Str(inv.Directory)
	e.Str("jobs")
	e.Int(int64(inv.Jobs))
	e.Str("dryRun")
	e.Bool(inv.DryRun)
	e.Str("watch")
	e.Bool(inv.Watch)
	e.Str("force")
	e.Bool(inv.Force)
	e.Str("json")
	e.Bool(inv.JSON)
	e.Str("output")
	e.Str(inv.Output)
	e.Str("earlyAction")
	e.Str(inv.EarlyAction)
	e.Str("helpTopic")
	e.Str(inv.HelpTopic)
	e.Str("help")
	e.Str(inv.HelpResult)
	e.Str("verbose")
	e.Bool(inv.Verbose)
	e.Str("color")
	e.Str(inv.Color)
	e.Str("diagnosticFormat")
	e.Str(inv.DiagnosticFormat)
	e.Str("shell")
	writeStrings(&e, inv.Shell)
	e.Str("toolOverrides")
	writeStrings(&e, inv.ToolOverrides)
	e.Str("environment")
	writeStrings(&e, inv.Environment)
	e.Str("timeoutMS")
	e.Int(inv.TimeoutMS)
	e.Str("retryCount")
	e.Int(int64(inv.RetryCount))
	e.Str("logLimit")
	e.Int(int64(inv.RetainBytes))
	e.Str("captureLimit")
	e.Int(int64(inv.CaptureLimit))
	e.Str("grants")
	e.BeginArray()
	for i := range inv.Grants {
		e.BeginObject()
		e.Str("capability")
		e.Str(capabilityName(inv.Grants[i].Capability))
		e.Str("names")
		writeStrings(&e, inv.Grants[i].Names)
		e.EndObject()
	}
	e.EndArray()
	e.Str("noDefaultGrants")
	e.Bool(inv.NoDefaultGrants)
	e.Str("lang")
	e.Str(inv.Lang)
	e.Str("indent")
	e.Str(inv.Indent)
	e.Str("indentWidth")
	e.Int(int64(inv.IndentWidth))
	e.Str("inPlace")
	e.Bool(inv.InPlace)
	e.Str("comment")
	e.Str(inv.Comment)
	e.Str("defines")
	writeStrings(&e, inv.Defines)
	e.Str("parameters")
	writeStrings(&e, inv.Parameters)
	e.Str("check")
	e.Bool(inv.Check)
	e.Str("depth")
	e.Int(int64(inv.Depth))
	e.Str("expand")
	e.Bool(inv.Expand)
	e.Str("targets")
	writeStrings(&e, inv.Targets)
	e.Str("files")
	writeStrings(&e, inv.Files)
	e.Str("args")
	writeStrings(&e, inv.Args)
	e.Str("inputs")
	e.BeginArray()
	for i := range inv.Inputs {
		e.BeginObject()
		e.Str("kind")
		e.Str(inv.Inputs[i].Kind)
		e.Str("value")
		e.Str(inv.Inputs[i].Value)
		e.Str("lang")
		e.Str(inv.Inputs[i].Lang)
		e.Str("entries")
		writeStrings(&e, inv.Inputs[i].Entries)
		e.EndObject()
	}
	e.EndArray()
	e.Str("ok")
	e.Bool(inv.OK)
	e.Str("error")
	e.BeginObject()
	e.Str("code")
	e.Str(inv.Error.Code)
	e.Str("message")
	e.Str(inv.Error.Message)
	e.EndObject()
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func writeStrings(e *json.Encoder, values []string) {
	e.BeginArray()
	for i := range values {
		e.Str(values[i])
	}
	e.EndArray()
}

func capabilityName(capability eval.Capability) string {
	if capability == eval.Write {
		return "write"
	}
	if capability == eval.Run {
		return "run"
	}
	if capability == eval.Env {
		return "env"
	}
	return "read"
}
