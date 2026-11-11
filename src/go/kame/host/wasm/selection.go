package wasm

import (
	"bytes"
	"kame/core"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"kame/program"
)

// TargetOperandsJSON normalizes CLI operands against the compiled declarations.
// Unlike plan roots, an empty input is valid: the program selects its default.
func (r *Runtime) TargetOperandsJSON(encoded string) PureResult {
	if r == nil || r.Program == nil {
		a := mem.System
		if r != nil {
			a = r.Alloc
		}
		return PureResult{Code: pureText(a, "PHASE_INVALID"), Message: pureText(a, "no compiled build source")}
	}
	var value core.Value
	if !core.ParseJSON(r.Alloc, []byte(encoded), &value) {
		return PureResult{Code: pureText(r.Alloc, "OPT_VALUE_INVALID"), Message: pureText(r.Alloc, "invalid target operands")}
	}
	defer value.Free(r.Alloc)
	if value.Kind != core.List {
		return PureResult{Code: pureText(r.Alloc, "OPT_VALUE_INVALID"), Message: pureText(r.Alloc, "target operands must be a string array")}
	}
	operands := make([]string, len(value.List))
	defer mem.FreeSlice(nil, operands)
	for i := range value.List {
		if value.List[i].Kind != core.String || value.List[i].Text == "" {
			return PureResult{Code: pureText(r.Alloc, "OPT_VALUE_INVALID"), Message: pureText(r.Alloc, "target operands must be nonempty strings")}
		}
		operands[i] = value.List[i].Text
	}
	targets := r.Program.NormalizeTargetOperands(operands)
	defer program.FreeStrings(r.Alloc, targets)
	buffer := bytes.Buffer{}
	e := json.NewEncoder(&buffer)
	e.BeginArray()
	for i := range targets {
		e.Str(targets[i])
	}
	e.EndArray()
	e.Flush()
	out := PureResult{Text: pureText(r.Alloc, buffer.String())}
	buffer.Reset()
	return out
}
