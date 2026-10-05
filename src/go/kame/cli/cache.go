package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func parseCache(inv *Invocation, args []string) {
	if len(args) == 0 || (args[0] != "list" && args[0] != "clean") {
		inv.fail("OPT_VALUE_INVALID", "cache requires the list or clean action")
		return
	}
	inv.OK = true
	inv.Args = slices.Append(mem.System, inv.Args, args[0])
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-C" || arg == "--directory" {
			if i+1 >= len(args) || args[i+1] == "" {
				inv.fail("OPT_NO_VALUE", "missing value for "+arg)
				return
			}
			i++
			inv.Directory = args[i]
			continue
		}
		if len(arg) > len("--directory=") && arg[:len("--directory=")] == "--directory=" {
			inv.Directory = arg[len("--directory="):]
			continue
		}
		inv.fail("OPT_VALUE_INVALID", "unexpected cache argument: "+arg)
		return
	}
}
