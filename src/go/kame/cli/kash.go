package cli

import (
	"strings"
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
				return append(result, arg)
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "--lang=") {
			continue
		}
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" {
			result = append(result, "--lang=kash")
			result = append(result, arg)
			if i+1 == len(args) {
				return result
			}
			i++
			result = append(result, args[i])
			result = append(result, "--")
			for j := i + 1; j < len(args); j++ {
				result = append(result, args[j])
			}
			return result
		}
		if strings.HasPrefix(arg, "--file=") || strings.HasPrefix(arg, "--command=") {
			result = append(result, "--lang=kash")
			result = append(result, arg)
			result = append(result, "--")
			for j := i + 1; j < len(args); j++ {
				result = append(result, args[j])
			}
			return result
		}
		if OptionTakesValue(arg) {
			result = append(result, arg)
			if i+1 == len(args) {
				return result
			}
			i++
			result = append(result, args[i])
			continue
		}
		if arg == "-" || (!strings.HasPrefix(arg, "-") && strings.IndexByte(arg, '=') < 0) {
			result = append(result, "--lang=kash")
			result = append(result, arg)
			result = append(result, "--")
			for j := i + 1; j < len(args); j++ {
				result = append(result, args[j])
			}
			return result
		}
		result = append(result, arg)
	}
	result = append(result, "--lang=kash")
	return append(result, "-")
}
