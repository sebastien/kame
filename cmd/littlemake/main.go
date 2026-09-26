// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
	"littlemake/operations"
	"littlemake/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

type buildArguments struct {
	File            string
	Command         string
	Directory       string
	Jobs            int
	DryRun          bool
	Force           bool
	JSON            bool
	Verbose         bool
	Grants          []eval.Grant
	NoDefaultGrants bool
	Shell           []string
	Environment     []string
	TimeoutMS       int64
	RetryCount      int
	RetainBytes     int
	Targets         []string
	OK              bool
}

func runBuild(args []string, out io.Writer, errOut io.Writer, toolRun bool) int {
	parsed := parseBuildArguments(args, errOut)
	if !parsed.OK {
		return 2
	}
	// A bare primary invocation with no build source is a discoverability
	// opportunity: present the overview instead of a terse diagnostic.
	bare := len(args) == 0 && !toolRun
	session := openBuildSession(parsed, errOut, !bare)
	if session.Status != 0 {
		if bare && session.Source.Missing {
			writeTopHelp(out)
			session.Free()
			return 0
		}
		return session.Status
	}
	defer session.Free()
	targets := parsed.Targets
	if len(targets) == 0 {
		if session.Program.HasTarget("default") {
			// Append through the allocator: a Go slice literal here would
			// transpile to a block-scoped C compound literal that dies before
			// materializeTargets reads it.
			targets = slices.Append(mem.System, targets, "default")
		} else {
			names := session.Program.NamedTargets()
			for i := range names {
				io.WriteString(out, names[i])
				io.WriteString(out, "\n")
			}
			program.FreeStrings(mem.System, names)
			return 0
		}
	}
	return materializeTargets(session.Program, targets, out, errOut, parsed.JSON)
}

type buildSession struct {
	Source   buildSource
	Registry *eval.Registry
	Parsed   *script.Script
	Program  *program.Program
	Status   int
}

func openBuildSession(options buildArguments, errOut io.Writer, reportMissing bool) buildSession {
	session := buildSession{Source: loadBuildSource(options, errOut, reportMissing)}
	if session.Source.Status != 0 {
		session.Status = session.Source.Status
		return session
	}
	// Capability roots and canonical file keys compare against an absolute
	// working directory; discovery above already resolved source names.
	if !path.IsAbs(options.Directory) {
		buffer := mem.AllocSlice[byte](mem.System, os.MaxPathLen, os.MaxPathLen)
		working, workingErr := os.Getwd(buffer)
		if workingErr == nil && working != "" {
			options.Directory = path.Join(mem.System, working, options.Directory)
		}
		mem.FreeSlice(mem.System, buffer)
	}
	session.Registry = eval.NewRegistry(mem.System)
	if !operations.Register(session.Registry) {
		cliError(errOut, "HOST_FAIL", "cannot register standard operations")
		session.Free()
		session.Status = 1
		return session
	}
	defaultGrants := []eval.Grant{{Capability: eval.Read, Names: []string{options.Directory}}, {Capability: eval.Write, Names: []string{options.Directory}}, {Capability: eval.Run}}
	grants := options.Grants
	if len(grants) == 0 && !options.NoDefaultGrants {
		grants = defaultGrants
	}
	environment := posix.Environment(mem.System)
	environment = mergeEnvironment(environment, options.Environment)
	session.Parsed = script.Parse(mem.System, session.Source.Name, session.Source.Text)
	compiled := program.Compile(mem.System, session.Parsed, session.Registry, program.Options{Host: posix.New(mem.System), Directory: options.Directory, Shell: options.Shell, Jobs: options.Jobs, DryRun: options.DryRun, Force: options.Force, CacheDisabled: options.Force, Environment: environment, TimeoutMS: options.TimeoutMS, RetryCount: options.RetryCount, RetainBytes: options.RetainBytes, Verbose: options.Verbose, Grants: grants})
	posix.FreeEnvironment(mem.System, environment)
	if compiled.Program == nil {
		for i := range compiled.Diagnostics {
			cliDiagnostic(errOut, compiled.Diagnostics[i])
		}
		compiled.Free(mem.System)
		session.Free()
		session.Status = 1
		return session
	}
	session.Program = compiled.Program
	compiled.Free(mem.System)
	return session
}

func (s *buildSession) Free() {
	if s == nil {
		return
	}
	if s.Program != nil {
		s.Program.Free()
	}
	if s.Parsed != nil {
		s.Parsed.Free()
	}
	if s.Registry != nil {
		s.Registry.Free()
	}
	freeBuildSource(&s.Source)
	*s = buildSession{}
}

func parseBuildArguments(args []string, errOut io.Writer) buildArguments {
	result := buildArguments{Directory: ".", Jobs: 1}
	afterOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterOptions {
			result.Targets = slices.Append(mem.System, result.Targets, arg)
			continue
		}
		if arg == "--" {
			afterOptions = true
			continue
		}
		if arg == "-n" || arg == "--dry-run" {
			result.DryRun = true
			continue
		}
		if arg == "--force" {
			result.Force = true
			continue
		}
		if arg == "--json" {
			result.JSON = true
			continue
		}
		if arg == "--verbose" {
			result.Verbose = true
			continue
		}
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" || arg == "-C" || arg == "--directory" || arg == "-j" || arg == "--jobs" || arg == "--shell" || arg == "--timeout" || arg == "--retry" || arg == "--log-limit" || arg == "--env" {
			if i+1 == len(args) {
				cliError(errOut, "OPT_NO_VALUE", "missing value for "+arg)
				return buildArguments{}
			}
			i++
			if !assignBuildOption(&result, arg, args[i], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 7 && arg[:7] == "--file=" {
			if !assignBuildOption(&result, "--file", arg[7:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 10 && arg[:10] == "--command=" {
			if !assignBuildOption(&result, "--command", arg[10:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 12 && arg[:12] == "--directory=" {
			if !assignBuildOption(&result, "--directory", arg[12:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 7 && arg[:7] == "--jobs=" {
			if !assignBuildOption(&result, "--jobs", arg[7:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 8 && arg[:8] == "--shell=" {
			if !assignBuildOption(&result, "--shell", arg[8:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 10 && arg[:10] == "--timeout=" {
			if !assignBuildOption(&result, "--timeout", arg[10:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 8 && arg[:8] == "--retry=" {
			if !assignBuildOption(&result, "--retry", arg[8:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 12 && arg[:12] == "--log-limit=" {
			if !assignBuildOption(&result, "--log-limit", arg[12:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 6 && arg[:6] == "--env=" {
			if !assignBuildOption(&result, "--env", arg[6:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			cliError(errOut, "OPT_UNKNOWN", "unknown option: "+arg)
			return buildArguments{}
		}
		result.Targets = slices.Append(mem.System, result.Targets, arg)
	}
	if result.File != "" && result.Command != "" {
		cliError(errOut, "OPT_CONFLICT", "--file and --command cannot be used together")
		return buildArguments{}
	}
	result.OK = true
	return result
}

func assignBuildOption(result *buildArguments, option string, value string, errOut io.Writer) bool {
	if value == "" {
		cliError(errOut, "OPT_VALUE_INVALID", "empty value for "+option)
		return false
	}
	if option == "-f" || option == "--file" {
		result.File = value
		return true
	}
	if option == "-c" || option == "--command" {
		result.Command = value
		return true
	}
	if option == "-C" || option == "--directory" {
		result.Directory = value
		return true
	}
	if option == "--shell" {
		result.Shell = slices.Append(mem.System, result.Shell, value)
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
			cliError(errOut, "OPT_VALUE_INVALID", "environment entry must be NAME=VALUE")
			return false
		}
		result.Environment = slices.Append(mem.System, result.Environment, value)
		return true
	}
	number, convertErr := strconv.Atoi(value)
	if convertErr != nil {
		cliError(errOut, "OPT_VALUE_INVALID", "invalid numeric option value")
		return false
	}
	if option == "-j" || option == "--jobs" {
		if number <= 0 {
			cliError(errOut, "OPT_VALUE_INVALID", "jobs must be a positive integer")
			return false
		}
		result.Jobs = number
		return true
	}
	if option == "--timeout" {
		if number < 0 {
			cliError(errOut, "OPT_VALUE_INVALID", "timeout must be nonnegative")
			return false
		}
		result.TimeoutMS = int64(number)
		return true
	}
	if option == "--retry" {
		if number < 0 {
			cliError(errOut, "OPT_VALUE_INVALID", "retry must be nonnegative")
			return false
		}
		result.RetryCount = number
		return true
	}
	if number <= 0 {
		cliError(errOut, "OPT_VALUE_INVALID", "log limit must be positive")
		return false
	}
	result.RetainBytes = number
	return true
}

func writeAST(out io.Writer, lang string, name string, text string) int {
	enc := newASTEncoder(out, lang, name)
	failed := false
	if lang == "expr" {
		result := expr.Parse(mem.System, name, text)
		enc.expr(result.Expr)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "template" {
		result := template.ParseString(mem.System, name, text)
		enc.template(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "rule" {
		result := rule.ParseRule(mem.System, name, text)
		enc.rule(result.Rule)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else {
		result := script.Parse(mem.System, name, text)
		enc.script(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	}
	enc.finish()
	if enc.err() != nil {
		return 1
	}
	if failed {
		return 1
	}
	return 0
}
