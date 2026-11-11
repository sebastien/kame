package wasm

import (
	"bytes"
	"kame/cli"
	"kame/internal/mem"

	"strings"
)

// ParseCLI parses one command line into the shared Invocation JSON. Arguments
// are NUL-separated because argv values cannot contain NUL. command is "" for
// the primary invocation.
func ParseCLI(a mem.Allocator, command string, encoded string) PureResult {
	args := splitNUL(encoded)
	inv := cli.Parse(command, args)
	mem.FreeSlice(nil, args)
	var buffer bytes.Buffer
	cli.WriteJSON(&buffer, &inv)
	inv.Free()
	text := pureText(a, buffer.String())
	buffer.Reset()
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
			args = append(args, encoded[start:])
			break
		}
		args = append(args, encoded[start:start+end])
		start = start + end + 1
	}
	return args
}
