package wasm

import (
	"kame/cli"
	"solod.dev/so/bytes"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// ParseCLI parses one command line into the shared Invocation JSON. Arguments
// are NUL-separated because argv values cannot contain NUL. command is "" for
// the primary invocation.
func ParseCLI(a mem.Allocator, command string, encoded string) PureResult {
	args := splitNUL(encoded)
	inv := cli.Parse(command, args)
	slices.Free(mem.System, args)
	var buffer bytes.Buffer = bytes.NewBuffer(a, nil)
	cli.WriteJSON(&buffer, &inv)
	inv.Free()
	text := pureText(a, buffer.String())
	buffer.Free()
	return PureResult{Text: text}
}

func splitNUL(encoded string) []string {
	if encoded == "" {
		return nil
	}
	var args []string
	start := 0
	for start <= len(encoded) {
		end := strings.IndexByte(encoded[start:], 0)
		if end < 0 {
			args = slices.Append(mem.System, args, encoded[start:])
			break
		}
		args = slices.Append(mem.System, args, encoded[start:start+end])
		start = start + end + 1
	}
	return args
}
