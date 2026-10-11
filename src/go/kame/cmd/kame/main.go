// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/ast"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/ownership"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// buildArguments is the shared, portable CLI grammar. The native CLI and the
// freestanding JavaScript wrapper parse argv with the same code.
type buildArguments = cli.Invocation

func runBuild(args []string, out io.Writer, errOut io.Writer, toolRun bool) int {
	parsed := parseBuildArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	if parsed.Watch {
		return runWatch(parsed, out, errOut)
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
	if len(parsed.Targets) == 1 && parsed.Targets[0] == "clean" && !session.Program.HasTarget("clean") {
		cleanArgs := []string{"-C", parsed.Directory}
		if parsed.DryRun { cleanArgs = slices.Append(mem.System, slices.Clone(mem.System, cleanArgs), "--dry-run") } else { cleanArgs = slices.Clone(mem.System, cleanArgs) }
		if parsed.JSON { cleanArgs = slices.Append(mem.System, cleanArgs, "--json") }
		status := runClean(cleanArgs, out, errOut)
		slices.Free(mem.System, cleanArgs)
		return status
	}
	targets := selectTargets(session.Program, parsed.Targets)
	parsed.Targets = nil
	defer program.FreeStrings(mem.System, targets)
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	return materializeTargets(session.Program, targets, out, errOut, parsed.JSON)
}

// selectTargets consumes the borrowed-operand slice and returns owned strings
// after shared assignment normalization and implicit default selection.
func selectTargets(p *program.Program, targets []string) []string {
	joined := p.NormalizeTargetOperands(targets)
	slices.Free(mem.System, targets)
	return joined
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
	Ledger         *outputLedger
	Source         buildSource
	Registry       *eval.Registry
	Parsed         *script.Script
	ParsedBorrowed bool
	Program        *program.Program
	Status         int
}

func openBuildSession(options buildArguments, errOut io.Writer, reportMissing bool) buildSession {
	return openBuildSessionForTools(options, errOut, reportMissing, false)
}

func openBuildSessionForTools(options buildArguments, errOut io.Writer, reportMissing bool, listTools bool) buildSession {
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
	programOptions := program.Options{Host: posix.New(mem.System), Directory: options.Directory, Shell: options.Shell, Jobs: options.Jobs, DryRun: options.DryRun, Force: options.Force, CacheDisabled: options.Force, Environment: environment, Defines: options.Defines, Parameters: options.Parameters, ToolOverrides: options.ToolOverrides, TimeoutMS: options.TimeoutMS, RetryCount: options.RetryCount, RetainBytes: options.RetainBytes, CaptureLimit: options.CaptureLimit, Verbose: options.Verbose, Grants: grants, ResolveTool: resolveBuildTool}
	if ownership.Supported() {
		session.Ledger = mem.Alloc[outputLedger](mem.System)
		session.Ledger.Source = cleanupSource(&session.Source)
		session.Ledger.Store = ownership.New(mem.System, options.Directory)
		session.Ledger.Out, session.Ledger.Err, session.Ledger.JSON = cliDiagnosticOut, errOut, options.JSON
		programOptions.Ownership, programOptions.OwnershipState = recordProduction, session.Ledger
		programOptions.AutomaticClean, programOptions.AutomaticCleanState = automaticClean, session.Ledger
	}
	compiled := program.CompileMany(mem.System, sources, session.Registry, programOptions)
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
	if listTools {
		for i := range compiled.Program.Tools {
			compiled.Program.ResolveTool(compiled.Program.Tools[i].Name)
		}
	}
	if directoryOwned {
		mem.FreeString(mem.System, options.Directory)
	}
	for i := range compiled.Diagnostics {
		if compiled.Diagnostics[i].Severity == diagnostic.Warning {
			emitDiagnostic(diagnosticWriter(cliDiagnosticOut, errOut, cliDiagnosticJSON), compiled.Diagnostics[i], cliDiagnosticJSON, session.Parsed.Source)
		}
	}
	if session.Parsed != nil {
		session.Parsed.Free()
	}
	session.Program = compiled.Program
	session.Parsed = compiled.Program.Parsed
	session.ParsedBorrowed = true
	compiled.Free(mem.System)
	return session
}

func resolveBuildTool(a mem.Allocator, name string, cwd string, environment []string) string {
	_ = environment // Tool references use startup PATH, not recipe --env overrides.
	return posix.ResolveTool(a, name, cwd, nil)
}

func (s *buildSession) Free() {
	if s == nil {
		return
	}
	if s.Program != nil {
		s.Program.Free()
	}
	if s.Ledger != nil { s.Ledger.Store.Free(); mem.Free(mem.System, s.Ledger) }
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
	inv := cli.Parse("build", args)
	applyInvocationPresentation(&inv)
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		inv.Free()
		return buildArguments{}
	}
	return inv
}

func writeAST(out io.Writer, lang string, name string, text string) int {
	return ast.WriteAST(out, lang, name, text)
}
