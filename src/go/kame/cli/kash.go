package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// KashInterpreterArgs selects Kash and treats tokens following its source as
// script arguments, like a normal language interpreter. The returned slice is
// owned by mem.System; its strings borrow args or static storage.
func KashInterpreterArgs(args []string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-l" || arg == "--lang" {
			if i+1 == len(args) {
				return slices.Append(mem.System, result, arg)
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "--lang=") {
			continue
		}
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" {
			result = slices.Append(mem.System, result, "--lang=kash")
			result = slices.Append(mem.System, result, arg)
			if i+1 == len(args) {
				return result
			}
			i++
			result = slices.Append(mem.System, result, args[i])
			result = slices.Append(mem.System, result, "--")
			for j := i + 1; j < len(args); j++ {
				result = slices.Append(mem.System, result, args[j])
			}
			return result
		}
		if strings.HasPrefix(arg, "--file=") || strings.HasPrefix(arg, "--command=") {
			result = slices.Append(mem.System, result, "--lang=kash")
			result = slices.Append(mem.System, result, arg)
			result = slices.Append(mem.System, result, "--")
			for j := i + 1; j < len(args); j++ {
				result = slices.Append(mem.System, result, args[j])
			}
			return result
		}
		if OptionTakesValue(arg) {
			result = slices.Append(mem.System, result, arg)
			if i+1 == len(args) {
				return result
			}
			i++
			result = slices.Append(mem.System, result, args[i])
			continue
		}
		if arg == "-" || (!strings.HasPrefix(arg, "-") && strings.IndexByte(arg, '=') < 0) {
			result = slices.Append(mem.System, result, "--lang=kash")
			result = slices.Append(mem.System, result, arg)
			result = slices.Append(mem.System, result, "--")
			for j := i + 1; j < len(args); j++ {
				result = slices.Append(mem.System, result, args[j])
			}
			return result
		}
		result = slices.Append(mem.System, result, arg)
	}
	result = slices.Append(mem.System, result, "--lang=kash")
	return slices.Append(mem.System, result, "-")
}
