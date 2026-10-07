package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Presentation extracts only global controls, preserving option values and the
// literal tail. Args owns its backing array; all strings still borrow argv.
func Presentation(args []string) Invocation {
	inv := Invocation{Name: "@presentation", Directory: ".", Output: "ansi", OK: true}
	requestedJSON := requestsJSON(args)
	selected := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for j := i; j < len(args); j++ {
				inv.Args = slices.Append(mem.System, inv.Args, args[j])
			}
			break
		}
		name, value, equals := arg, "", false
		for j := 0; j < len(arg); j++ {
			if arg[j] == '=' && len(arg) > 2 && arg[:2] == "--" {
				name, value, equals = arg[:j], arg[j+1:], true
				break
			}
		}
		if name == "--json" && !equals {
			name, value, equals = "--output", "json", true
		}
		if name == "-o" || name == "--output" || name == "--color" || name == "--diagnostic-format" {
			if !equals {
				if i+1 == len(args) {
					inv.fail("OPT_NO_VALUE", "missing value for "+name)
					inv.JSON = requestedJSON
					return inv
				}
				i++
				value = args[i]
			}
			if name == "-o" || name == "--output" {
				if value != "ansi" && value != "text" && value != "json" {
					inv.fail("OPT_VALUE_INVALID", "output must be ansi, text, or json")
					inv.JSON = requestedJSON
					return inv
				}
				if selected != "" && selected != value {
					inv.fail("OPT_CONFLICT", "conflicting output modes")
					inv.JSON = requestedJSON
					return inv
				}
				selected, inv.Output, inv.JSON = value, value, value == "json"
			} else if !assignBuildOption(&inv, name, value) {
				inv.JSON = requestedJSON
				return inv
			}
			continue
		}
		inv.Args = slices.Append(mem.System, inv.Args, arg)
		if OptionTakesValue(arg) && i+1 < len(args) {
			i++
			inv.Args = slices.Append(mem.System, inv.Args, args[i])
		}
	}
	for i := 0; i < len(inv.Args); i++ {
		arg := inv.Args[i]
		if arg == "--" {
			break
		}
		if arg == "-h" || arg == "--help" {
			inv.EarlyAction = "help"
		}
		if (arg == "-V" || arg == "--version") && inv.EarlyAction != "help" {
			inv.EarlyAction = "version"
		}
		if OptionTakesValue(arg) {
			i++
		}
	}
	if len(inv.Args) != 0 && inv.Args[0] == "do" {
		inv.HelpTopic = "do"
		if len(inv.Args) > 1 && inv.Args[1] != "help" && len(inv.Args[1]) != 0 && inv.Args[1][0] != '-' {
			inv.HelpTopic = inv.Args[1]
		}
	}
	return inv
}

func requestsJSON(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--output=json" {
			return true
		}
		if (arg == "-o" || arg == "--output") && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
		if OptionTakesValue(arg) {
			i++
		}
	}
	return false
}

// OptionTakesValue is shared with help/source dispatch. Never inspect the next
// argv token for global controls when it belongs to one of these options.
func OptionTakesValue(arg string) bool {
	return isBuildValueOption(arg) || arg == "-l" || arg == "--lang" ||
		arg == "--entry" || arg == "--depth" || arg == "--indent" ||
		arg == "--indent-width" || arg == "--comment"
}
