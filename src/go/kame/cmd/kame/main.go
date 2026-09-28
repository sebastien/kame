// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/template"
	"kame/operations"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

type buildArguments struct {
	File             string
	Command          string
	Directory        string
	Jobs             int
	DryRun           bool
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
	Targets          []string
	OK               bool
}

	// Free releases the parser-owned backing arrays. Option values themselves
// borrow argv storage; Program.CompileMany clones values it retains.
func (options *buildArguments) Free() {
	if len(options.Grants) != 0 { slices.Free(mem.System, options.Grants) }
	if len(options.Shell) != 0 { slices.Free(mem.System, options.Shell) }
	if len(options.Environment) != 0 { slices.Free(mem.System, options.Environment) }
	if len(options.Targets) != 0 { slices.Free(mem.System, options.Targets) }
	*options = buildArguments{}
}

func runBuild(args []string, out io.Writer, errOut io.Writer, toolRun bool) int {
	parsed := parseBuildArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	// A bare primary invocation with no build source is a discoverability
	// opportunity: present the overview instead of a terse diagnostic.
	bare := len(args) == 0 && !toolRun
	session := openBuildSession(parsed, errOut, !bare)
	defer session.Free()
	if session.Status != 0 {
		if bare && session.Source.Missing {
			writeTopHelp(out)
			return 0
		}
		return session.Status
	}
	targets := selectTargets(session.Program, parsed.Targets)
	parsed.Targets = targets
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	return materializeTargets(session.Program, targets, out, errOut, parsed.JSON)
}

// selectTargets applies the uniform target selection shared by the primary
// invocation and every target-taking command: explicit targets pass through,
// and otherwise "default" is selected when the program defines it. An empty
// result means no target was selected because no default is defined.
func selectTargets(p *program.Program, targets []string) []string {
	if len(targets) != 0 {
		return targets
	}
	if p.HasTarget("default") {
		// Append through the allocator: a Go slice literal here would transpile
		// to a block-scoped C compound literal that dies before the caller reads
		// the result.
		return slices.Append(mem.System, targets, "default")
	}
	return nil
}

// reportNoDefault reports TGT_NO_DEFAULT when no target was requested and the
// program defines no "default". The available literal named targets are offered
// as a note so the user can choose one. It returns the CLI failure status.
func reportNoDefault(p *program.Program, out io.Writer, errOut io.Writer, json bool) int {
	note := "available targets: (none)"
	if names := p.NamedTargets(); len(names) != 0 {
		joined := strings.Join(mem.System, names, ", ")
		note = "available targets: " + joined
		mem.FreeString(mem.System, joined)
		program.FreeStrings(mem.System, names)
	}
	d := diagnostic.Diagnostic{
		Code:     cloneCommandText("TGT_NO_DEFAULT"),
		Severity: diagnostic.Error,
		Message:  cloneCommandText("no target was requested and no default target is defined"),
		Owned:    true,
	}
	d.Notes = slices.Append(mem.System, d.Notes, cloneCommandText(note))
	d.Tips = slices.Append(mem.System, d.Tips, cloneCommandText("define a default target or name a target explicitly"))
	emitDiagnostic(diagnosticWriter(out, errOut, json), d, json, nil)
	d.Free(mem.System)
	return 1
}

type buildSession struct {
	Source   buildSource
	Registry *eval.Registry
	Parsed   *script.Script
	ParsedBorrowed bool
	Program  *program.Program
	Status   int
}

func openBuildSession(options buildArguments, errOut io.Writer, reportMissing bool) buildSession {
	return openBuildSessionForTools(options, errOut, reportMissing, false)
}

func openBuildSessionForTools(options buildArguments, errOut io.Writer, reportMissing bool, allowMissingTools bool) buildSession {
	configureDiagnosticPresentation(options)
	session := buildSession{Source: loadBuildSource(options, errOut, reportMissing)}
	if session.Source.Status != 0 {
		session.Status = session.Source.Status
		return session
	}
	// Capability roots and canonical file keys compare against an absolute
	// working directory; discovery above already resolved source names.
	directoryOwned := false
	if !path.IsAbs(options.Directory) {
		buffer := mem.AllocSlice[byte](mem.System, os.MaxPathLen, os.MaxPathLen)
		working, workingErr := os.Getwd(buffer)
		if workingErr == nil && working != "" {
			options.Directory = path.Join(mem.System, working, options.Directory)
			directoryOwned = true
		}
		mem.FreeSlice(mem.System, buffer)
	}
	session.Registry = eval.NewRegistry(mem.System)
	if !operations.Register(session.Registry) {
		cliError(errOut, "HOST_FAIL", "cannot register standard operations")
		if directoryOwned {
			mem.FreeString(mem.System, options.Directory)
		}
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
	sources := session.Source.compileSources()
	if len(session.Source.Files) != 0 {
		session.Parsed = script.Parse(mem.System, session.Source.Files[0].Name, session.Source.Files[0].Text)
	}
	compiled := program.CompileMany(mem.System, sources, session.Registry, program.Options{Host: posix.New(mem.System), Directory: options.Directory, Shell: options.Shell, Jobs: options.Jobs, DryRun: options.DryRun, Force: options.Force, CacheDisabled: options.Force, Environment: environment, TimeoutMS: options.TimeoutMS, RetryCount: options.RetryCount, RetainBytes: options.RetainBytes, Verbose: options.Verbose, Grants: grants})
	slices.Free(mem.System, sources)
	posix.FreeEnvironment(mem.System, environment)
	if compiled.Program == nil {
		if directoryOwned {
			mem.FreeString(mem.System, options.Directory)
		}
		for i := range compiled.Diagnostics {
			emitDiagnostic(diagnosticWriter(cliDiagnosticOut, errOut, cliDiagnosticJSON), compiled.Diagnostics[i], cliDiagnosticJSON, session.Parsed.Source)
		}
		compiled.Free(mem.System)
		session.Free()
		session.Status = 1
		return session
	}
	toolEnvironment := posix.Environment(mem.System)
	for i := range compiled.Program.Tools {
		name := compiled.Program.Tools[i].Name
		resolved := resolveTool(name, options.Directory, toolEnvironment)
		if resolved == "" {
			if allowMissingTools { continue }
			cliError(errOut, "TOOL_MISSING", "required tool not found or not executable: "+name)
			posix.FreeEnvironment(mem.System, toolEnvironment)
			if directoryOwned {
				mem.FreeString(mem.System, options.Directory)
			}
			compiled.Program.Free()
			compiled.Free(mem.System)
			session.Free()
			session.Status = 1
			return session
		}
		compiled.Program.SetToolPath(name, resolved)
		mem.FreeString(mem.System, resolved)
	}
	posix.FreeEnvironment(mem.System, toolEnvironment)
	if directoryOwned {
		mem.FreeString(mem.System, options.Directory)
	}
	for i := range compiled.Diagnostics {
		if compiled.Diagnostics[i].Severity == diagnostic.Warning {
			emitDiagnostic(diagnosticWriter(cliDiagnosticOut, errOut, cliDiagnosticJSON), compiled.Diagnostics[i], cliDiagnosticJSON, session.Parsed.Source)
		}
	}
	if session.Parsed != nil { session.Parsed.Free() }
	session.Program = compiled.Program
	session.Parsed = compiled.Program.Parsed
	session.ParsedBorrowed = true
	compiled.Free(mem.System)
	return session
}

func resolveTool(name, cwd string, environment []string) string {
	if name == "" { return "" }
	if name[0] == '/' {
		if executableFile(name) { return cloneCommandText(name) }
		return ""
	}
	pathValue := environmentValue(environment, "PATH")
	for start := 0; start <= len(pathValue); {
		end := start
		for end < len(pathValue) && pathValue[end] != ':' { end++ }
		directory := pathValue[start:end]
		if directory == "" { directory = cwd }
		candidate := path.Join(mem.System, directory, name)
		if executableFile(candidate) { return candidate }
		mem.FreeString(mem.System, candidate)
		if end == len(pathValue) { break }
		start = end + 1
	}
	return ""
}

func executableFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0
}

func (s *buildSession) Free() {
	if s == nil {
		return
	}
	if s.Program != nil {
		s.Program.Free()
	}
	if s.Parsed != nil && !s.ParsedBorrowed {
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
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" || arg == "-C" || arg == "--directory" || arg == "-j" || arg == "--jobs" || arg == "--shell" || arg == "--timeout" || arg == "--retry" || arg == "--log-limit" || arg == "--env" || arg == "--color" || arg == "--diagnostic-format" {
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
		if len(arg) >= 8 && arg[:8] == "--color=" {
			if !assignBuildOption(&result, "--color", arg[8:], errOut) {
				return buildArguments{}
			}
			continue
		}
		if len(arg) >= 20 && arg[:20] == "--diagnostic-format=" {
			if !assignBuildOption(&result, "--diagnostic-format", arg[20:], errOut) {
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
	if option == "--color" {
		if value != "auto" && value != "always" && value != "never" {
			cliError(errOut, "OPT_VALUE_INVALID", "color must be auto, always, or never")
			return false
		}
		result.Color = value
		return true
	}
	if option == "--diagnostic-format" {
		if value != "human" && value != "plain" {
			cliError(errOut, "OPT_VALUE_INVALID", "diagnostic format must be human or plain")
			return false
		}
		result.DiagnosticFormat = value
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
