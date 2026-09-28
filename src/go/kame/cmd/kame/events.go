package main

import (
	"kame/diagnostic"
	"kame/program"
	"solod.dev/so/io"
)

// The JSON event and diagnostic encoders live in the portable program package so
// the native CLI and the freestanding host share one wire format.
func writeJSONEvent(out io.Writer, event program.Event) { program.WriteJSONEvent(out, event) }

func writeJSONDiagnostic(out io.Writer, d diagnostic.Diagnostic) { program.WriteJSONDiagnostic(out, d) }
