package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func parseCache(inv *Invocation, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 == len(args) {
				break
			}
			inv.fail("OPT_VALUE_INVALID", "unexpected cache operands")
			return
		}
		if arg == "-n" || arg == "--dry-run" {
			inv.DryRun = true
			continue
		}
		if arg == "list" || arg == "clean" {
			if len(inv.Args) != 0 {
				inv.fail("OPT_CONFLICT", "cache accepts one action")
				return
			}
			inv.Args = slices.Append(mem.System, inv.Args, arg)
			continue
		}
		if arg == "-C" || arg == "--directory" {
			if i+1 >= len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for "+arg)
				return
			}
			i++
			if !assignBuildOption(inv, arg, args[i]) {
				return
			}
			continue
		}
		if equalsValue(arg, "--directory", inv) {
			if inv.Error.Code != "" {
				return
			}
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			inv.fail("OPT_UNKNOWN", "unknown cache option: "+arg)
			return
		}
		inv.fail("OPT_VALUE_INVALID", "unexpected cache argument: "+arg)
		return
	}
	if len(inv.Args) == 0 {
		inv.fail("OPT_NO_VALUE", "cache requires the list or clean action")
		return
	}
	if inv.DryRun && inv.Args[0] != "clean" {
		inv.fail("OPT_CONFLICT", "--dry-run requires cache clean")
		return
	}
	inv.OK = true
}
