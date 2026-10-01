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
	compiled := program.CompileMany(mem.System, sources, session.Registry, program.Options{Host: posix.New(mem.System), Directory: options.Directory, Shell: options.Shell, Jobs: options.Jobs, DryRun: options.DryRun, Force: options.Force, CacheDisabled: options.Force, Environment: environment, TimeoutMS: options.TimeoutMS, RetryCount: options.RetryCount, RetainBytes: options.RetainBytes, Verbose: options.Verbose, Grants: grants, ResolveTool: resolveBuildTool})
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

func resolveTool(name, cwd string, environment []string) string {
	if name == "" {
		return ""
	}
	if name[0] == '/' {
		if executableFile(name) {
			return cloneCommandText(name)
		}
		return ""
	}
	pathValue := environmentValue(environment, "PATH")
	for start := 0; start <= len(pathValue); {
		end := start
		for end < len(pathValue) && pathValue[end] != ':' {
			end++
		}
		directory := pathValue[start:end]
		candidate := ""
		if path.IsAbs(directory) {
			candidate = path.Join(mem.System, directory, name)
		} else {
			candidate = path.Join(mem.System, cwd, directory, name)
		}
		if executableFile(candidate) {
			return candidate
		}
		mem.FreeString(mem.System, candidate)
		if end == len(pathValue) {
			break
		}
		start = end + 1
	}
	return ""
}

func resolveBuildTool(a mem.Allocator, name string, cwd string, environment []string) string {
	_ = a           // The native CLI and its Program both use mem.System.
	_ = environment // Tool references use startup PATH, not recipe --env overrides.
	startup := posix.Environment(mem.System)
	resolved := resolveTool(name, cwd, startup)
	posix.FreeEnvironment(mem.System, startup)
	return resolved
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
	inv := cli.Parse("build", args)
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
