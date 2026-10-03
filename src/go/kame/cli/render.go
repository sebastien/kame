package cli

import (
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func parseRender(inv *Invocation, args []string) {
	inv.NoDefaultGrants = true
	selected, positional := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positional && arg == "--" {
			positional = true
			continue
		}
		if !positional && runGrant(inv, arg) {
			if inv.Error.Code != "" {
				return
			}
			continue
		}
		if !positional && arg == "--check" {
			inv.Check = true
			continue
		}
		option, value, hasValue := arg, "", false
		if !positional {
			for j := 0; j < len(arg); j++ {
				if arg[j] == '=' {
					option, value, hasValue = arg[:j], arg[j+1:], true
					break
				}
			}
		}
		if !positional && (option == "-c" || option == "--command" || option == "--define" || option == "--comment") {
			if !hasValue {
				if i+1 == len(args) {
					inv.fail("OPT_NO_VALUE", "missing value for "+option)
					return
				}
				i++
				value = args[i]
			}
			if option == "--define" {
				equal := strings.IndexByte(value, '=')
				if equal <= 0 || !renderName(value[:equal]) {
					inv.fail("OPT_VALUE_INVALID", "define must be NAME=VALUE with a valid name")
					return
				}
				inv.Defines = slices.Append(mem.System, inv.Defines, value)
			} else if option == "--comment" {
				_, ok := template.NormalizeStyle(value)
				if !ok {
					inv.fail("OPT_VALUE_INVALID", "unknown comment style")
					return
				}
				inv.Comment = value
			} else {
				if selected {
					inv.fail("OPT_CONFLICT", "render accepts one source")
					return
				}
				inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "command", Value: value, Lang: "template"})
				selected = true
			}
			continue
		}
		if !positional && len(arg) > 0 && arg[0] == '-' && arg != "-" {
			inv.fail("OPT_UNKNOWN", "unknown option: "+arg)
			return
		}
		if selected {
			inv.fail("OPT_CONFLICT", "render accepts one source")
			return
		}
		kind := "file"
		if arg == "-" {
			kind = "stdin"
		}
		inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: kind, Value: arg, Lang: "template"})
		selected = true
	}
	if !selected {
		inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "stdin", Lang: "template"})
	}
	inv.OK = true
}

func renderName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && ((c >= '0' && c <= '9') || c == '-')) {
			continue
		}
		return false
	}
	return len(name) > 0
}
