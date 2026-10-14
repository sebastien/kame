package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// sourceOption shares file, inline, and directory spelling across language tools.
// Inputs records selection even when an inline source is empty.
func sourceOption(inv *Invocation, args []string, index *int) bool {
	arg := args[*index]
	option, value, equals := arg, "", false
	for j := range arg {
		if arg[j] == '=' && len(arg) > 2 && arg[:2] == "--" {
			option, value, equals = arg[:j], arg[j+1:], true
			break
		}
	}
	if option != "-f" && option != "--file" && option != "-c" && option != "--command" && option != "-C" && option != "--directory" {
		return false
	}
	if !equals {
		if *index+1 == len(args) {
			inv.fail("OPT_NO_VALUE", "missing value for "+option)
			return true
		}
		*index = *index + 1
		value = args[*index]
	}
	if option == "-C" || option == "--directory" {
		assignBuildOption(inv, option, value)
	} else if option == "-c" || option == "--command" {
		inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "command", Value: value})
	} else {
		languageFile(inv, value)
	}
	return true
}

func languageFile(inv *Invocation, value string) {
	if value == "" {
		inv.fail("OPT_VALUE_INVALID", "source filename must not be empty")
		return
	}
	kind := "file"
	if value == "-" {
		kind = "stdin"
	}
	inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: kind, Value: value})
}

func validateLanguageSources(inv *Invocation, multiple bool) bool {
	if len(inv.Inputs) == 0 {
		inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "stdin"})
	}
	if len(inv.Inputs) > 1 {
		if !multiple {
			inv.fail("OPT_VALUE_INVALID", inv.Name+" accepts one source")
			return false
		}
		for i := range inv.Inputs {
			if inv.Inputs[i].Kind != "file" {
				inv.fail("OPT_CONFLICT", "only file-backed formatting accepts multiple sources")
				return false
			}
		}
	}
	if inv.InPlace && inv.Inputs[0].Kind != "file" {
		inv.fail("OPT_CONFLICT", "--in-place requires file-backed input")
		return false
	}
	return true
}
