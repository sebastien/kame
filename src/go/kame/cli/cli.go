// Package cli is the portable command grammar shared by the native CLI and the
// freestanding JavaScript wrapper. It parses argv into a normalized Invocation
// and performs no I/O, so both front-ends accept the same options, defaults,
// conflicts, and usage diagnostics.
package cli

import (
	"kame/lang/definition"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

// Diagnostic is a usage error: the stable code and human message the CLI prints.
type Diagnostic struct {
	Code    string
	Message string
}

// Invocation is the normalized result of parsing one command line. Fields are a
// superset of every command's needs; each command reads the ones it uses.
type Invocation struct {
	Name             string
	Command          string
	File             string
	Directory        string
	Jobs             int
	DryRun           bool
	Watch            bool
	Force            bool
	JSON             bool
	Verbose          bool
	Color            string
	DiagnosticFormat string
	Grants           []eval.Grant
	NoDefaultGrants  bool
	Shell            []string
	Environment      []string
	TimeoutMS        int64
	RetryCount       int
	RetainBytes      int
	CaptureLimit     int
	Lang             string
	Indent           string
	IndentWidth      int
	InPlace          bool
	Check            bool
	Comment          string
	Defines          []string
	ToolOverrides    []string
	Depth            int
	Expand           bool
	Targets          []string
	Files            []string
	Args             []string
	Inputs           []RunInput
	OK               bool
	Error            Diagnostic
}

// Free releases all parser-owned backing arrays. Option values borrow argv
// storage; consumers clone what they retain.
func (inv *Invocation) Free() {
	for i := range inv.Inputs {
		slices.Free(mem.System, inv.Inputs[i].Entries)
	}
	slices.Free(mem.System, inv.Inputs)
	for i := range inv.Grants {
		for j := range inv.Grants[i].Names {
			mem.FreeString(mem.System, inv.Grants[i].Names[j])
		}
		slices.Free(mem.System, inv.Grants[i].Names)
	}
	slices.Free(mem.System, inv.Grants)
	slices.Free(mem.System, inv.Shell)
	slices.Free(mem.System, inv.Environment)
	slices.Free(mem.System, inv.Targets)
	slices.Free(mem.System, inv.Files)
	slices.Free(mem.System, inv.Args)
	slices.Free(mem.System, inv.Defines)
	slices.Free(mem.System, inv.ToolOverrides)
	mem.FreeString(mem.System, inv.Error.Message)
	*inv = Invocation{}
}

// fail records a usage error. The message is copied because callers build it
// by concatenation, whose storage does not outlive the returning call.
func (inv *Invocation) fail(code string, message string) {
	inv.Error = Diagnostic{Code: code, Message: cloneText(message)}
	inv.OK = false
}

// Parse parses one command's arguments. command is "" for the primary
// invocation or the name after "do".
func Parse(command string, args []string) Invocation {
	if message := RemovedCommandMessage(command); message != "" {
		inv := Invocation{Name: command}
		inv.fail("CMD_UNKNOWN", message)
		return inv
	}
	if command == "run" {
		return ParseRun(args)
	}
	inv := Invocation{Name: command, Directory: ".", Jobs: 1, Lang: "script", Indent: "tabs", IndentWidth: 4, Depth: 1}
	if command == "cache" {
		parseCache(&inv, args)
		return inv
	}
	if command == "" && SelectsRun(args) {
		return ParseRun(args)
	}
	if command == "" && AppendsCommands(args) {
		return parseRun(args, true)
	}
	if !isCommand(command) {
		inv.fail("CMD_UNKNOWN", "unknown command: "+command)
		return inv
	}
	if command == "" || command == "build" {
		parseBuild(&inv, args)
		return inv
	}
	if command == "render" {
		parseRender(&inv, args)
		return inv
	}
	if command == "fmt" {
		parseFormat(&inv, args)
		return inv
	}
	if command == "parse" {
		parseParse(&inv, args)
		return inv
	}
	if command == "span" {
		parseGraph(&inv, args, true)
		return inv
	}
	if command == "inputs" || command == "outputs" {
		parseGraph(&inv, args, false)
		return inv
	}
	// plan, cat, tools accept the common build options only.
	parseBuild(&inv, args)
	return inv
}

func isCommand(command string) bool {
	return command == "" || command == "build" || command == "help" || command == "run" || command == "plan" || command == "cat" || command == "inputs" || command == "outputs" || command == "span" || command == "tools" || command == "parse" || command == "fmt" || command == "render" || command == "cache"
}

// RemovedCommandMessage keeps native/WASM migration diagnostics identical.
func RemovedCommandMessage(command string) string {
	if command == "expr" {
		return "do expr was removed; use kame do run --lang expr -c TEXT (or - for stdin)"
	}
	if command == "kash" {
		return "do kash was removed; use kame do run FILE.kash"
	}
	return ""
}

// parseBuild mirrors the primary and do-plan/cat/tools grammar.
func parseBuild(inv *Invocation, args []string) {
	afterOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterOptions {
			inv.Targets = slices.Append(mem.System, inv.Targets, arg)
			continue
		}
		if arg == "--" {
			afterOptions = true
			continue
		}
		if arg == "-n" || arg == "--dry-run" {
			inv.DryRun = true
			continue
		}
		if arg == "--watch" {
			inv.Watch = true
			continue
		}
		if arg == "--force" {
			inv.Force = true
			continue
		}
		if arg == "--json" {
			inv.JSON = true
			continue
		}
		if arg == "--verbose" {
			inv.Verbose = true
			continue
		}
		if isBuildValueOption(arg) {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for "+arg)
				return
			}
			i++
			if !assignBuildOption(inv, arg, args[i]) {
				return
			}
			continue
		}
		if equalsValue(arg, "--tool", inv) || equalsValue(arg, "--define", inv) || equalsValue(arg, "--file", inv) || equalsValue(arg, "--command", inv) || equalsValue(arg, "--directory", inv) || equalsValue(arg, "--jobs", inv) || equalsValue(arg, "--shell", inv) || equalsValue(arg, "--timeout", inv) || equalsValue(arg, "--retry", inv) || equalsValue(arg, "--log-limit", inv) || equalsValue(arg, "--capture-limit", inv) || equalsValue(arg, "--env", inv) || equalsValue(arg, "--color", inv) || equalsValue(arg, "--diagnostic-format", inv) {
			if inv.Error.Code != "" {
				return
			}
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			inv.fail("OPT_UNKNOWN", "unknown option: "+arg)
			return
		}
		inv.Targets = slices.Append(mem.System, inv.Targets, arg)
	}
	if inv.Watch && inv.Name != "" && inv.Name != "build" {
		inv.fail("OPT_CONFLICT", "--watch is supported only for builds")
		return
	}
	if inv.File != "" && inv.Command != "" {
		inv.fail("OPT_CONFLICT", "--file and --command cannot be used together")
		return
	}
	inv.OK = true
}

func isBuildValueOption(arg string) bool {
	if arg == "--define" || arg == "--tool" {
		return true
	}
	if arg == "--capture-limit" {
		return true
	}
	if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" || arg == "-C" || arg == "--directory" || arg == "-j" || arg == "--jobs" || arg == "--shell" || arg == "--timeout" || arg == "--retry" || arg == "--log-limit" || arg == "--env" || arg == "--color" || arg == "--diagnostic-format" {
		return true
	}
	return false
}

// equalsValue handles the "--name=value" spelling and reports whether arg was
// consumed. A parse failure is left on inv.
func equalsValue(arg string, name string, inv *Invocation) bool {
	prefix := name + "="
	if len(arg) < len(prefix) || arg[:len(prefix)] != prefix {
		return false
	}
	assignBuildOption(inv, name, arg[len(prefix):])
	return true
}

func assignBuildOption(inv *Invocation, option string, value string) bool {
	if option == "--tool" {
		equal := -1
		for i := range value {
			if value[i] == '=' {
				equal = i
				break
			}
		}
		if equal <= 0 || equal+1 == len(value) {
			inv.fail("OPT_VALUE_INVALID", "tool must be NAME=PATH with a nonempty path")
			return false
		}
		inv.ToolOverrides = slices.Append(mem.System, inv.ToolOverrides, value)
		return true
	}
	if option == "--define" {
		equal := -1
		for i := range value {
			if value[i] == '=' {
				equal = i
				break
			}
		}
		if equal <= 0 || !definition.ValidName(value[:equal]) {
			inv.fail("OPT_VALUE_INVALID", "define must be NAME=VALUE with a valid name")
			return false
		}
		inv.Defines = slices.Append(mem.System, inv.Defines, value)
		return true
	}
	if value == "" {
		inv.fail("OPT_VALUE_INVALID", "empty value for "+option)
		return false
	}
	if option == "-f" || option == "--file" {
		inv.File = value
		return true
	}
	if option == "-c" || option == "--command" {
		inv.Command = value
		return true
	}
	if option == "-C" || option == "--directory" {
		inv.Directory = value
		return true
	}
	if option == "--shell" {
		inv.Shell = slices.Append(mem.System, inv.Shell, value)
		return true
	}
	if option == "--env" {
		valid := false
		for i := range value {
			if value[i] == '=' && i != 0 {
				valid = true
				break
			}
		}
		if !valid {
			inv.fail("OPT_VALUE_INVALID", "environment entry must be NAME=VALUE")
			return false
		}
		inv.Environment = slices.Append(mem.System, inv.Environment, value)
		return true
	}
	if option == "--color" {
		if value != "auto" && value != "always" && value != "never" {
			inv.fail("OPT_VALUE_INVALID", "color must be auto, always, or never")
			return false
		}
		inv.Color = value
		return true
	}
	if option == "--diagnostic-format" {
		if value != "human" && value != "plain" {
			inv.fail("OPT_VALUE_INVALID", "diagnostic format must be human or plain")
			return false
		}
		inv.DiagnosticFormat = value
		return true
	}
	number, convertErr := strconv.Atoi(value)
	if convertErr != nil {
		inv.fail("OPT_VALUE_INVALID", "invalid numeric option value")
		return false
	}
	if option == "--capture-limit" {
		if number <= 0 {
			inv.fail("OPT_VALUE_INVALID", "capture limit must be positive")
			return false
		}
		inv.CaptureLimit = number
		return true
	}
	if option == "-j" || option == "--jobs" {
		if number <= 0 {
			inv.fail("OPT_VALUE_INVALID", "jobs must be a positive integer")
			return false
		}
		inv.Jobs = number
		return true
	}
	if option == "--timeout" {
		if number < 0 {
			inv.fail("OPT_VALUE_INVALID", "timeout must be nonnegative")
			return false
		}
		inv.TimeoutMS = int64(number)
		return true
	}
	if option == "--retry" {
		if number < 0 {
			inv.fail("OPT_VALUE_INVALID", "retry must be nonnegative")
			return false
		}
		inv.RetryCount = number
		return true
	}
	if number <= 0 {
		inv.fail("OPT_VALUE_INVALID", "log limit must be positive")
		return false
	}
	inv.RetainBytes = number
	return true
}

// parseGraph mirrors do inputs/outputs/span. Graph options are recognized
// anywhere, then the remaining tokens use the build grammar.
func parseGraph(inv *Invocation, args []string, allowExpand bool) {
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--expand" {
			if !allowExpand {
				inv.fail("OPT_UNKNOWN", "unknown option: --expand")
				return
			}
			inv.Expand = true
			continue
		}
		if arg == "--depth" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --depth")
				return
			}
			i++
			depth, valid := parseDepth(args[i])
			if !valid {
				inv.fail("OPT_VALUE_INVALID", "depth must be -1 or a nonnegative integer")
				return
			}
			inv.Depth = depth
			continue
		}
		prefix := "--depth="
		if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
			depth, valid := parseDepth(arg[len(prefix):])
			if !valid {
				inv.fail("OPT_VALUE_INVALID", "depth must be -1 or a nonnegative integer")
				return
			}
			inv.Depth = depth
			continue
		}
		remaining = slices.Append(mem.System, remaining, arg)
	}
	parseBuild(inv, remaining)
	slices.Free(mem.System, remaining)
}

func parseDepth(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	number, convertErr := strconv.Atoi(value)
	if convertErr != nil || number < -1 {
		return 0, false
	}
	return number, true
}

// parseFormat mirrors do fmt.
func parseFormat(inv *Invocation, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-i" {
			inv.InPlace = true
			continue
		}
		if arg == "-n" {
			inv.Check = true
			continue
		}
		if arg == "-l" || arg == "--lang" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --lang")
				return
			}
			i++
			inv.Lang = args[i]
			continue
		}
		if len(arg) > 7 && arg[:7] == "--lang=" {
			inv.Lang = arg[7:]
			continue
		}
		if arg == "--indent" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --indent")
				return
			}
			i++
			inv.Indent = args[i]
			continue
		}
		if arg == "--comment" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --comment")
				return
			}
			i++
			inv.Comment = args[i]
			continue
		}
		if len(arg) > len("--comment=") && arg[:len("--comment=")] == "--comment=" {
			inv.Comment = arg[len("--comment="):]
			continue
		}
		if len(arg) > len("--indent=") && arg[:len("--indent=")] == "--indent=" {
			inv.Indent = arg[len("--indent="):]
			continue
		}
		if arg == "--indent-width" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --indent-width")
				return
			}
			i++
			width, valid := parseIndentWidth(args[i])
			if !valid {
				inv.fail("OPT_VALUE_INVALID", "invalid indent width: "+args[i])
				return
			}
			inv.IndentWidth = width
			continue
		}
		if len(arg) > len("--indent-width=") && arg[:len("--indent-width=")] == "--indent-width=" {
			value := arg[len("--indent-width="):]
			width, valid := parseIndentWidth(value)
			if !valid {
				inv.fail("OPT_VALUE_INVALID", "invalid indent width: "+value)
				return
			}
			inv.IndentWidth = width
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			inv.fail("OPT_UNKNOWN", "unknown option: "+arg)
			return
		}
		inv.Files = slices.Append(mem.System, inv.Files, arg)
	}
	if inv.InPlace && inv.Check {
		inv.fail("OPT_CONFLICT", "-i and -n cannot be used together")
		return
	}
	if inv.Lang != "expr" && inv.Lang != "template" && inv.Lang != "rule" && inv.Lang != "script" && inv.Lang != "kash" {
		inv.fail("OPT_VALUE_INVALID", "invalid language: "+inv.Lang)
		return
	}
	if inv.Indent != "tabs" && inv.Indent != "spaces" {
		inv.fail("OPT_VALUE_INVALID", "invalid indent style: "+inv.Indent)
		return
	}
	if inv.Comment != "" && !isCommentStyle(inv.Comment) {
		inv.fail("OPT_VALUE_INVALID", "invalid comment style: "+inv.Comment)
		return
	}
	inv.OK = true
}

func isCommentStyle(value string) bool {
	switch value {
	case "auto", "plain", "none", "html", "c", "hash", "dash", "semi", "percent", "powershell", "batch":
		return true
	default:
		return false
	}
}

func parseIndentWidth(value string) (int, bool) {
	if len(value) == 0 {
		return 0, false
	}
	width := 0
	for i := range value {
		digit := value[i] - '0'
		if digit > 9 {
			return 0, false
		}
		width = width*10 + int(digit)
		if width > 16 {
			return 0, false
		}
	}
	return width, width > 0
}

// parseParse mirrors do parse. Unlike do fmt, --lang has no default.
func parseParse(inv *Invocation, args []string) {
	inv.Lang = ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+2 != len(args) || inv.File != "" {
				inv.fail("OPT_VALUE_INVALID", "parse accepts at most one file")
				return
			}
			inv.File = args[i+1]
			break
		}
		if arg == "-l" || arg == "--lang" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --lang")
				return
			}
			i++
			inv.Lang = args[i]
			continue
		}
		if len(arg) > 7 && arg[:7] == "--lang=" {
			inv.Lang = arg[7:]
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			inv.fail("OPT_UNKNOWN", "unknown option: "+arg)
			return
		}
		if inv.File != "" {
			inv.fail("OPT_VALUE_INVALID", "parse accepts at most one file")
			return
		}
		inv.File = arg
	}
	if inv.Lang == "" {
		inv.fail("OPT_NO_VALUE", "missing required --lang")
		return
	}
	if inv.Lang != "expr" && inv.Lang != "template" && inv.Lang != "rule" && inv.Lang != "script" && inv.Lang != "kash" {
		inv.fail("OPT_VALUE_INVALID", "invalid language: "+inv.Lang)
		return
	}
	inv.OK = true
}

func appendGrant(grants []eval.Grant, option string, name string) []eval.Grant {
	capability := eval.Read
	if option == "--allow-write" {
		capability = eval.Write
	}
	if option == "--allow-run" {
		capability = eval.Run
	}
	if option == "--allow-env" {
		capability = eval.Env
	}
	grant := eval.Grant{Capability: capability}
	if name != "" {
		grant.Names = slices.Append(mem.System, grant.Names, cloneText(name))
	}
	return slices.Append(mem.System, grants, grant)
}

func cloneText(text string) string {
	if text == "" {
		return ""
	}
	buffer := slices.Make[byte](mem.System, len(text))
	copy(buffer, text)
	return string(buffer)
}
