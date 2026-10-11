package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func parseClean(inv *Invocation, args []string) {
	inv.OK = true
	after := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			inv.fail("OPT_VALUE_INVALID", "clean requires a nonempty resource path")
			return
		}
		if !after && arg == "--" {
			after = true
			continue
		}
		if !after && (arg == "-C" || arg == "--directory") {
			if i+1 == len(args) || args[i+1] == "" {
				inv.fail("OPT_NO_VALUE", "missing directory")
				return
			}
			i++
			inv.Directory = args[i]
			continue
		}
		if !after && len(arg) > 12 && arg[:12] == "--directory=" {
			inv.Directory = arg[12:]
			continue
		}
		if !after && (arg == "-n" || arg == "--dry-run") {
			inv.DryRun = true
			continue
		}
		if !after && (arg == "--keep" || arg == "--release") {
			if inv.Command != "" {
				inv.fail("OPT_VALUE_INVALID", "clean protection modes are mutually exclusive")
				return
			}
			inv.Command = arg
			continue
		}
		if !after && len(arg) > 0 && arg[0] == '-' {
			inv.fail("OPT_UNKNOWN", "unknown clean option: "+arg)
			return
		}
		inv.Args = slices.Append(mem.System, inv.Args, arg)
	}
	if inv.Command == "" && len(inv.Args) != 0 {
		inv.fail("OPT_VALUE_INVALID", "clean takes no target operands")
		return
	}
	if inv.Command != "" && (len(inv.Args) == 0 || inv.DryRun) {
		inv.fail("OPT_VALUE_INVALID", "keep/release require paths and cannot be combined with dry-run")
	}
}
