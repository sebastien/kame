// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lib"
	"littlemake/runtime"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

func main() { posix.InstallSignals(); os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Run executes a command with explicit streams so the command behavior remains
// independently testable from process startup.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if handled, status := handleHelpAndVersion(args, out); handled { return status }
	if len(args) != 0 && args[0] == "do" {
		if len(args) == 1 { writeDoHelp(out); return 0 }
		if args[1] == "help" { return runHelpCommand(args[2:], out, errOut) }
		if args[1] == "parse" { return runParse(args[2:], in, out, errOut) }
		if args[1] == "fmt" { return runFormat(args[2:], in, out, errOut) }
		if args[1] == "plan" { return runPlan(args[2:], out, errOut) }
		if args[1] == "cat" { return runCat(args[2:], out, errOut) }
		if args[1] == "inputs" { return runGraph(args[2:], out, errOut, "inputs") }
		if args[1] == "outputs" { return runGraph(args[2:], out, errOut, "outputs") }
		if args[1] == "span" { return runGraph(args[2:], out, errOut, "span") }
		if args[1] == "expr" { return runExpr(args[2:], in, out, errOut) }
		if args[1] == "run" { return runBuild(args[2:], out, errOut, true) }
		cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[1])
		io.WriteString(errOut, "run 'littlemake do --help' to list commands\n")
		return 2
	}
	return runBuild(args, out, errOut, false)
}

type buildArguments struct {
	File string
	Command string
	Directory string
	Jobs int
	DryRun bool
	Force bool
	JSON bool
	Verbose bool
	Grants []eval.Grant
	NoDefaultGrants bool
	Shell []string
	Environment []string
	TimeoutMS int64
	RetryCount int
	RetainBytes int
	Targets []string
	OK bool
}

func runBuild(args []string, out io.Writer, errOut io.Writer, toolRun bool) int {
	parsed := parseBuildArguments(args, errOut)
	if !parsed.OK { return 2 }
	// A bare primary invocation with no build source is a discoverability
	// opportunity: present the overview instead of a terse diagnostic.
	bare := len(args) == 0 && !toolRun
	session := openBuildSession(parsed, errOut, !bare)
	if session.Status != 0 {
		if bare && session.Source.Missing { writeTopHelp(out); session.Free(); return 0 }
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
			for i := range names { io.WriteString(out, names[i]); io.WriteString(out, "\n") }
			program.FreeStrings(mem.System, names)
			return 0
		}
	}
	return materializeTargets(session.Program, targets, out, errOut, parsed.JSON)
}

type buildSession struct { Source buildSource; Registry *eval.Registry; Parsed *script.Script; Program *program.Program; Status int }

func openBuildSession(options buildArguments, errOut io.Writer, reportMissing bool) buildSession {
	session := buildSession{Source: loadBuildSource(options, errOut, reportMissing)}
	if session.Source.Status != 0 { session.Status = session.Source.Status; return session }
	// Capability roots and canonical file keys compare against an absolute
	// working directory; discovery above already resolved source names.
	if !path.IsAbs(options.Directory) {
		buffer := mem.AllocSlice[byte](mem.System, os.MaxPathLen, os.MaxPathLen)
		working, workingErr := os.Getwd(buffer)
		if workingErr == nil && working != "" { options.Directory = path.Join(mem.System, working, options.Directory) }
		mem.FreeSlice(mem.System, buffer)
	}
	session.Registry = eval.NewRegistry(mem.System)
	if !lib.Register(session.Registry) { cliError(errOut, "HOST_FAIL", "cannot register standard operations"); session.Free(); session.Status = 1; return session }
	defaultGrants := []eval.Grant{{Capability: eval.Read, Names: []string{options.Directory}}, {Capability: eval.Write, Names: []string{options.Directory}}, {Capability: eval.Run}}
	grants := options.Grants
	if len(grants) == 0 && !options.NoDefaultGrants { grants = defaultGrants }
	environment := posix.Environment(mem.System)
	environment = mergeEnvironment(environment, options.Environment)
	session.Parsed = script.Parse(mem.System, session.Source.Name, session.Source.Text)
	compiled := program.Compile(mem.System, session.Parsed, session.Registry, program.Options{Directory: options.Directory, Shell: options.Shell, Jobs: options.Jobs, DryRun: options.DryRun, Force: options.Force, CacheDisabled: options.Force, Environment: environment, TimeoutMS: options.TimeoutMS, RetryCount: options.RetryCount, RetainBytes: options.RetainBytes, Verbose: options.Verbose, Grants: grants})
	posix.FreeEnvironment(mem.System, environment)
	if compiled.Program == nil {
		for i := range compiled.Diagnostics { cliDiagnostic(errOut, compiled.Diagnostics[i]) }
		compiled.Free(mem.System); session.Free(); session.Status = 1; return session
	}
	session.Program = compiled.Program
	compiled.Free(mem.System)
	return session
}

func (s *buildSession) Free() {
	if s == nil { return }
	if s.Program != nil { s.Program.Free() }
	if s.Parsed != nil { s.Parsed.Free() }
	if s.Registry != nil { s.Registry.Free() }
	freeBuildSource(&s.Source)
	*s = buildSession{}
}

func parseBuildArguments(args []string, errOut io.Writer) buildArguments {
	result := buildArguments{Directory: ".", Jobs: 1}
	afterOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterOptions { result.Targets = slices.Append(mem.System, result.Targets, arg); continue }
		if arg == "--" { afterOptions = true; continue }
		if arg == "-n" || arg == "--dry-run" { result.DryRun = true; continue }
		if arg == "--force" { result.Force = true; continue }
		if arg == "--json" { result.JSON = true; continue }
		if arg == "--verbose" { result.Verbose = true; continue }
		if arg == "-f" || arg == "--file" || arg == "-c" || arg == "--command" || arg == "-C" || arg == "--directory" || arg == "-j" || arg == "--jobs" || arg == "--shell" || arg == "--timeout" || arg == "--retry" || arg == "--log-limit" || arg == "--env" {
			if i+1 == len(args) { cliError(errOut, "OPT_NO_VALUE", "missing value for "+arg); return buildArguments{} }
			i++
			if !assignBuildOption(&result, arg, args[i], errOut) { return buildArguments{} }
			continue
		}
		if len(arg) >= 7 && arg[:7] == "--file=" { if !assignBuildOption(&result, "--file", arg[7:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 10 && arg[:10] == "--command=" { if !assignBuildOption(&result, "--command", arg[10:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 12 && arg[:12] == "--directory=" { if !assignBuildOption(&result, "--directory", arg[12:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 7 && arg[:7] == "--jobs=" { if !assignBuildOption(&result, "--jobs", arg[7:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 8 && arg[:8] == "--shell=" { if !assignBuildOption(&result, "--shell", arg[8:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 10 && arg[:10] == "--timeout=" { if !assignBuildOption(&result, "--timeout", arg[10:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 8 && arg[:8] == "--retry=" { if !assignBuildOption(&result, "--retry", arg[8:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 12 && arg[:12] == "--log-limit=" { if !assignBuildOption(&result, "--log-limit", arg[12:], errOut) { return buildArguments{} }; continue }
		if len(arg) >= 6 && arg[:6] == "--env=" { if !assignBuildOption(&result, "--env", arg[6:], errOut) { return buildArguments{} }; continue }
		if len(arg) != 0 && arg[0] == '-' { cliError(errOut, "OPT_UNKNOWN", "unknown option: "+arg); return buildArguments{} }
		result.Targets = slices.Append(mem.System, result.Targets, arg)
	}
	if result.File != "" && result.Command != "" { cliError(errOut, "OPT_CONFLICT", "--file and --command cannot be used together"); return buildArguments{} }
	result.OK = true
	return result
}

func assignBuildOption(result *buildArguments, option string, value string, errOut io.Writer) bool {
	if value == "" { cliError(errOut, "OPT_VALUE_INVALID", "empty value for "+option); return false }
	if option == "-f" || option == "--file" { result.File = value; return true }
	if option == "-c" || option == "--command" { result.Command = value; return true }
	if option == "-C" || option == "--directory" { result.Directory = value; return true }
	if option == "--shell" { result.Shell = slices.Append(mem.System, result.Shell, value); return true }
	if option == "--env" {
		valid := false
		for i := range value { if value[i] == '=' && i != 0 { valid = true; break } }
		if !valid { cliError(errOut, "OPT_VALUE_INVALID", "environment entry must be NAME=VALUE"); return false }
		result.Environment = slices.Append(mem.System, result.Environment, value); return true
	}
	number, convertErr := strconv.Atoi(value)
	if convertErr != nil { cliError(errOut, "OPT_VALUE_INVALID", "invalid numeric option value"); return false }
	if option == "-j" || option == "--jobs" { if number <= 0 { cliError(errOut, "OPT_VALUE_INVALID", "jobs must be a positive integer"); return false }; result.Jobs = number; return true }
	if option == "--timeout" { if number < 0 { cliError(errOut, "OPT_VALUE_INVALID", "timeout must be nonnegative"); return false }; result.TimeoutMS = int64(number); return true }
	if option == "--retry" { if number < 0 { cliError(errOut, "OPT_VALUE_INVALID", "retry must be nonnegative"); return false }; result.RetryCount = number; return true }
	if number <= 0 { cliError(errOut, "OPT_VALUE_INVALID", "log limit must be positive"); return false }
	result.RetainBytes = number
	return true
}

type buildSource struct { Name string; Text string; Data []byte; OwnedName bool; Missing bool; Status int }

func freeBuildSource(source *buildSource) {
	if source == nil { return }
	if len(source.Data) != 0 { mem.FreeSlice(mem.System, source.Data) }
	if source.OwnedName && source.Name != "" { mem.FreeString(mem.System, source.Name) }
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	if options.Command != "" { return buildSource{Name: "<command>", Text: options.Command} }
	if options.File != "" { return readBuildSource(options.File, errOut) }
	candidates := []string{"Makefile.lmk", "make.lmk", "src/lmk/main.lmk"}
	for i := range candidates {
		candidate := path.Join(mem.System, options.Directory, candidates[i])
		_, statErr := os.Stat(candidate)
		if statErr == nil { result := readBuildSource(candidate, errOut); result.Name = cloneCommandText(candidate); result.OwnedName = true; mem.FreeString(mem.System, candidate); return result }
		mem.FreeString(mem.System, candidate)
	}
	if reportMissing { cliError(errOut, "BUILD_NO_SOURCE", "no build source found (tried Makefile.lmk, make.lmk, src/lmk/main.lmk)") }
	return buildSource{Missing: true, Status: 1}
}

func cloneCommandText(text string) string {
	if text == "" { return "" }
	data := mem.AllocSlice[byte](mem.System, len(text), len(text))
	copy(data, []byte(text))
	return string(data)
}

// mergeEnvironment gives explicit CLI entries precedence without relying on
// duplicate-variable behavior in execve implementations.
func mergeEnvironment(current []string, overrides []string) []string {
	for i := range overrides {
		entry := overrides[i]
		nameEnd := 0
		for nameEnd < len(entry) && entry[nameEnd] != '=' { nameEnd++ }
		replaced := false
		for j := range current {
			if len(current[j]) <= nameEnd || current[j][nameEnd] != '=' { continue }
			if current[j][:nameEnd] != entry[:nameEnd] { continue }
			mem.FreeString(mem.System, current[j])
			current[j], replaced = cloneCommandText(entry), true
			break
		}
		if !replaced { current = slices.Append(mem.System, current, cloneCommandText(entry)) }
	}
	return current
}

func readBuildSource(name string, errOut io.Writer) buildSource {
	data, readErr := os.ReadFile(mem.System, name)
	if readErr != nil { cliError(errOut, "FS_ERR", "cannot read source: "+name); return buildSource{Status: 1} }
	return buildSource{Name: name, Text: string(data), Data: data}
}

func materializeTargets(p *program.Program, targets []string, out io.Writer, errOut io.Writer, json bool) int {
	var handles []*program.Handle
	failed := false
	for i := range targets {
		started := p.Start(targets[i])
		if started.Diagnostic.Code != "" { annotateTargetDiagnostic(&started.Diagnostic, targets[i]); emitDiagnostic(diagnosticWriter(out, errOut, json), started.Diagnostic, json); started.Diagnostic.Free(mem.System); failed = true; continue }
		handles = slices.Append(mem.System, handles, started.Handle)
	}
	remaining := len(handles)
	cancelling := false
	for remaining != 0 {
		signal := posix.TakeSignal()
		if signal < 0 { return 128 - signal }
		if signal > 0 && !cancelling {
			cancelling = true
			for i := range handles { if handles[i] != nil { handles[i].Cancel() } }
		}
		p.Tick(10)
		drainEvents(p, out, errOut, json)
		for i := range handles {
			if handles[i] == nil { continue }
			// Definitions are reactive nodes: publishing their first value leaves
			// the node open for future invalidations. A CLI target request consumes
			// that first value rather than waiting for a terminal state.
			if handles[i].Definition && handles[i].Node.Current {
				writeValue(out, handles[i].Node.Latest)
				handles[i].Free(); handles[i] = nil; remaining--
				continue
			}
			polled := handles[i].Poll()
			if !polled.Done { continue }
			if polled.Result.Diagnostic.Code != "" { emitDiagnostic(diagnosticWriter(out, errOut, json), polled.Result.Diagnostic, json); failed = true
			} else if polled.Result.Value.Kind != core.Nil { writeValue(out, polled.Result.Value) }
			polled.Result.Free(mem.System)
			handles[i].Free(); handles[i] = nil; remaining--
		}
	}
	drainEvents(p, out, errOut, json)
	slices.Free(mem.System, handles)
	if failed || cancelling { return 1 }
	return 0
}

func drainEvents(p *program.Program, out io.Writer, errOut io.Writer, json bool) {
	for {
		next := p.NextEvent()
		if !next.OK { return }
		event := next.Event
		if json { writeJSONEvent(out, event) } else if event.Kind == program.Stdout { out.Write(event.Data)
		} else if event.Kind == program.Stderr { errOut.Write(event.Data)
		} else if event.Kind == program.TargetStarted { fmt.Fprintf(errOut, "[%s] started\n", event.Target)
		} else if event.Kind == program.TargetCompleted { fmt.Fprintf(errOut, "[%s] complete\n", event.Target)
		} else if event.Kind == program.TargetFailed || event.Kind == program.TargetCancelled { fmt.Fprintf(errOut, "[%s] failed\n", event.Target)
		} else if event.Kind == program.CacheWarning { fmt.Fprintf(errOut, "warning %s: %s\n", event.Diagnostic.Code, event.Diagnostic.Message) }
		event.Free(mem.System)
	}
}

func emitDiagnostic(out io.Writer, d diagnostic.Diagnostic, json bool) {
	if json { writeJSONDiagnostic(out, d); return }
	cliDiagnostic(out, d)
}

// diagnosticWriter picks the stream for command diagnostics: JSON events are
// stdout-only by specification; human diagnostics stay on stderr.
func diagnosticWriter(out io.Writer, errOut io.Writer, json bool) io.Writer {
	if json { return out }
	return errOut
}

func cliDiagnostic(out io.Writer, d diagnostic.Diagnostic) {
	if d.Source != "" { fmt.Fprintf(out, "%s:1:1: error %s: %s\n", d.Source, d.Code, d.Message)
	} else { cliError(out, d.Code, d.Message) }
	for i := range d.Notes { io.WriteString(out, "note: "); io.WriteString(out, d.Notes[i]); io.WriteString(out, "\n") }
}

// annotateTargetDiagnostic appends deterministic suggestions to unknown-target
// diagnostics. Path-like targets written without an explicit ./ prefix suggest
// the explicit form that file rules require.
func annotateTargetDiagnostic(d *diagnostic.Diagnostic, target string) {
	if d.Code != "TGT_NO_RULE" || len(target) == 0 { return }
	if target[0] == '/' { return }
	if len(target) >= 2 && target[0] == '.' && target[1] == '/' { return }
	pathLike := false
	for i := 0; i < len(target); i++ { if target[i] == '.' || target[i] == '/' { pathLike = true; break } }
	if !pathLike { return }
	note := "did you mean ./" + target + "?"
	for i := range d.Notes { if d.Notes[i] == note { return } }
	d.Notes = slices.Append(mem.System, d.Notes, cloneCommandText(note))
}

func writeValue(out io.Writer, value core.Value) {
	if value.Kind == core.String { io.WriteString(out, value.Text); return }
	if value.Kind == core.Bytes { out.Write(value.Bytes); return }
	if value.Kind == core.Bool { if value.Bool { io.WriteString(out, "true") } else { io.WriteString(out, "false") }; return }
	if value.Kind == core.Int { fmt.Fprintf(out, "%d", value.Int); return }
	if value.Kind == core.Float { fmt.Fprintf(out, "%g", value.Float); return }
	if value.Kind == core.Nil { io.WriteString(out, "nil"); return }
	if value.Kind == core.Resource { io.WriteString(out, value.Resource.Name); return }
	if value.Kind == core.List {
		io.WriteString(out, "[")
		for i := range value.List { if i != 0 { io.WriteString(out, " ") }; writeValue(out, value.List[i]) }
		io.WriteString(out, "]")
		return
	}
	if value.Kind == core.Record {
		io.WriteString(out, "[")
		for i := range value.Record { if i != 0 { io.WriteString(out, " ") }; io.WriteString(out, value.Record[i].Key); io.WriteString(out, ": "); writeValue(out, value.Record[i].Value) }
		io.WriteString(out, "]")
	}
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
