package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// RunInput retains command-line order and the language selected when an input
// was encountered. Text and filenames borrow argv; entry arrays belong to inv.
type RunInput struct {
	Kind string // file, command, stdin, discover
	Value string
	Lang string
	Entries []string
}

// ParseRun is the unified runner grammar. Keep it separate from build parsing:
// '--' starts program arguments, not targets, and '-c' may be repeated/mixed.
func ParseRun(args []string) Invocation {
	return parseRun(args, false)
}

// AppendsCommands identifies discovered-build invocations with trailing inline
// work. Source/option values and operands after -- are never inspected as flags.
func AppendsCommands(args []string) bool {
	if selectsWatch(args) { return false }
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { return false }
		if arg == "-c" || arg == "--command" || (len(arg) >= 10 && arg[:10] == "--command=") { return true }
		if arg == "-l" || arg == "--lang" || arg == "--entry" || isBuildValueOption(arg) { i++ }
	}
	return false
}

func parseRun(args []string, discover bool) Invocation {
	inv := Invocation{Name: "run", Directory: ".", Jobs: 1}
	if discover { inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "discover", Lang: "kmk"}) }
	language := ""
	stdin := false
	ruleOptions, processOptions := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for j := i+1; j < len(args); j++ {
				if discover { inv.Inputs[0].Entries = slices.Append(mem.System, inv.Inputs[0].Entries, args[j]) } else { inv.Args = slices.Append(mem.System, inv.Args, args[j]) }
			}
			break
		}
		if runGrant(&inv, arg) {
			if inv.Error.Code != "" { return inv }
			continue
		}
		option, value := arg, ""
		hasValue := false
		for j := 0; j < len(arg); j++ {
			if arg[j] == '=' && len(arg) > 2 && arg[:2] == "--" {
				option, value, hasValue = arg[:j], arg[j+1:], true
				break
			}
		}
		if option == "-l" || option == "--lang" || option == "--entry" || isBuildValueOption(option) {
			if !hasValue {
				if i+1 == len(args) { inv.fail("OPT_NO_VALUE", "missing value for "+option); return inv }
				i++
				value = args[i]
			}
			if option == "-l" || option == "--lang" {
				if value != "km" && value != "kmk" && value != "kash" && value != "expr" { inv.fail("OPT_VALUE_INVALID", "invalid language: "+value); return inv }
				language = value
				continue
			}
			if option == "--entry" {
				if !runEntry(&inv, value) { return inv }
				continue
			}
			if option == "-c" || option == "--command" {
				lang := language
				if lang == "" { lang = "km" }
				inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: "command", Value: value, Lang: lang})
				continue
			}
			if option == "-f" || option == "--file" {
				if !runFile(&inv, value, language, &stdin) { return inv }
				continue
			}
			if option == "-j" || option == "--jobs" || option == "--retry" || option == "--shell" || option == "--log-limit" { ruleOptions = true }
			if option == "--env" { processOptions = true }
			if !assignBuildOption(&inv, option, value) { return inv }
			continue
		}
		if arg == "-n" || arg == "--dry-run" { inv.DryRun, ruleOptions = true, true; continue }
		if arg == "--force" { inv.Force, ruleOptions = true, true; continue }
		if arg == "--json" { inv.JSON = true; continue }
		if arg == "--verbose" { inv.Verbose = true; continue }
		if !discover && (arg == "-" || runLanguage(arg) != "") {
			if !runFile(&inv, arg, language, &stdin) { return inv }
			continue
		}
		if len(arg) != 0 && arg[0] == '-' { inv.fail("OPT_UNKNOWN", "unknown option: "+arg); return inv }
		if discover {
			inv.Inputs[0].Entries = slices.Append(mem.System, inv.Inputs[0].Entries, arg)
			continue
		}
		if len(inv.Inputs) == 0 {
			if !runFile(&inv, arg, language, &stdin) { return inv }
			continue
		}
		if !runEntry(&inv, arg) { return inv }
	}
	if len(inv.Inputs) == 0 { inv.fail("OPT_NO_VALUE", "run requires a source input"); return inv }
	// A later file/parser cannot broaden the first fragment's authority.
	inv.NoDefaultGrants = !runProcessLanguage(inv.Inputs[0].Lang)
	rules, processes := false, false
	for i := range inv.Inputs {
		if inv.Inputs[i].Lang == "kmk" { rules = true }
		if runProcessLanguage(inv.Inputs[i].Lang) { processes = true }
	}
	if (ruleOptions && !rules) || (processOptions && !processes) { inv.fail("OPT_CONFLICT", "option requires rule or process source work"); return inv }
	inv.OK = true
	return inv
}

func runLanguage(name string) string {
	if len(name) >= 5 && name[len(name)-5:] == ".kash" { return "kash" }
	if len(name) >= 4 && name[len(name)-4:] == ".ksh" { return "kash" }
	if len(name) >= 4 && name[len(name)-4:] == ".kmk" { return "kmk" }
	if len(name) >= 3 && name[len(name)-3:] == ".km" { return "km" }
	return ""
}

// SelectsRun distinguishes explicit source execution from ordinary build
// targets, without consulting the filesystem. Options never become operands.
func SelectsRun(args []string) bool {
	if selectsWatch(args) { return false }
	language := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { return false }
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" { return true }
		if len(arg) >= 7 && arg[:7] == "--file=" { return true }
		if len(arg) >= 10 && arg[:10] == "--command=" { return true }
		if arg == "-l" || arg == "--lang" { language = true; i++; continue }
		if len(arg) > 7 && arg[:7] == "--lang=" { language = true; continue }
		if arg == "-" { return true }
		if arg == "--entry" || isBuildValueOption(arg) { i++; continue }
		if len(arg) > 0 && arg[0] == '-' { continue }
		return language || runLanguage(arg) != ""
	}
	return language
}

// Watch owns one rule source through the build grammar, even with -f or -c.
func selectsWatch(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { return false }
		if arg == "--watch" { return true }
		if arg == "-l" || arg == "--lang" || arg == "--entry" || isBuildValueOption(arg) { i++ }
	}
	return false
}

func runFile(inv *Invocation, name string, language string, stdin *bool) bool {
	kind := "file"
	if name == "-" {
		if *stdin { inv.fail("OPT_CONFLICT", "stdin may be selected only once"); return false }
		*stdin, kind = true, "stdin"
		if language == "" { language = "km" }
	}
	if language == "" { language = runLanguage(name) }
	if language == "" || name == "" { inv.fail("OPT_VALUE_INVALID", "source requires a recognized suffix or explicit language"); return false }
	inv.Inputs = slices.Append(mem.System, inv.Inputs, RunInput{Kind: kind, Value: name, Lang: language})
	return true
}

func runAcceptsEntries(language string) bool { return language == "km" || language == "kmk" }

func runProcessLanguage(language string) bool { return language == "kmk" || language == "kash" }

func runEntry(inv *Invocation, name string) bool {
	if len(inv.Inputs) == 0 { inv.fail("OPT_VALUE_INVALID", "entry requires a preceding source"); return false }
	input := &inv.Inputs[len(inv.Inputs)-1]
	if name == "" || !runAcceptsEntries(input.Lang) { inv.fail("OPT_VALUE_INVALID", "entry is invalid for this source language"); return false }
	input.Entries = slices.Append(mem.System, input.Entries, name)
	return true
}

func runGrant(inv *Invocation, arg string) bool {
	option, name := arg, ""
	for i := 0; i < len(arg); i++ {
		if arg[i] == '=' { option, name = arg[:i], arg[i+1:]; break }
	}
	if option != "--allow-read" && option != "--allow-write" && option != "--allow-run" && option != "--allow-env" { return false }
	if len(option) != len(arg) && name == "" { inv.fail("OPT_VALUE_INVALID", "empty value for "+option); return true }
	inv.Grants = appendGrant(inv.Grants, option, name)
	return true
}
