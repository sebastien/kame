package main

import (
	"kame/cli"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
)

func writeTopHelp(out io.Writer) { cli.WriteHelp(out, "", cliDiagnosticJSON, stdoutColor) }
func writeDoHelp(out io.Writer)  { cli.WriteHelp(out, "do", cliDiagnosticJSON, stdoutColor) }

func writeVersion(out io.Writer) {
	if cliDiagnosticJSON {
		e := json.NewEncoder(out)
		e.BeginObject()
		e.Str("schema")
		e.Int(1)
		e.Str("type")
		e.Str("version")
		e.Str("version")
		e.Str(version)
		e.Str("buildID")
		e.Str(buildID)
		e.Str("buildTime")
		e.Str(buildTime)
		e.Str("buildMode")
		e.Str(buildMode())
		e.EndObject()
		e.Flush()
		io.WriteString(out, "\n")
		return
	}
	io.WriteString(out, "kame "+version+" ("+buildID+"; "+buildTime+"; "+buildMode()+")\n")
}

func isDoCommand(name string) bool {
	for i := range cli.Commands {
		if cli.Commands[i].Name == name {
			return true
		}
	}
	return false
}

func writeCommandHelp(out io.Writer, command string) bool {
	if !isDoCommand(command) {
		return false
	}
	if command == "help" {
		writeDoHelp(out)
	} else {
		cli.WriteHelp(out, command, cliDiagnosticJSON, stdoutColor)
	}
	return true
}

func runHelpCommand(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) != 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 || args[0] == "help" {
		writeDoHelp(out)
		return 0
	}
	if writeCommandHelp(out, args[0]) {
		return 0
	}
	cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[0])
	if !cliDiagnosticJSON {
		io.WriteString(errOut, "help: run 'kame do --help' to list commands\n")
	}
	return 2
}

func handleHelpAndVersion(presentation cli.Invocation, out io.Writer) (bool, int) {
	if presentation.EarlyAction == "version" {
		writeVersion(out)
		return true, 0
	}
	if presentation.EarlyAction == "help" {
		if presentation.HelpTopic != "" && writeCommandHelp(out, presentation.HelpTopic) {
			return true, 0
		}
		if presentation.HelpTopic != "" {
			writeDoHelp(out)
		} else {
			writeTopHelp(out)
		}
		return true, 0
	}
	return false, 0
}
