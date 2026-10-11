package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/ownership"
	"kame/program"
	"solod.dev/so/encoding/json"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

type outputLedger struct {
	Store  *ownership.Store
	Source core.Signature
	Out    io.Writer
	Err    io.Writer
	JSON   bool
}

func cleanupCancelled(state any) bool {
	_ = state
	return posix.TakeSignal() != 0
}

func freeOutputLedger(ledger *outputLedger) {
	if ledger != nil {
		ledger.Store.Free()
		mem.Free(mem.System, ledger)
	}
}

func cleanupSource(source *buildSource) core.Signature {
	digest := core.NewDigest()
	// Authored Data retains edits to include/conditional directives that expanded
	// texts might hide. Name/membership are part of the conservative guard.
	for i := range source.Files {
		name := cleanRoot(source.Files[i].Name)
		digest.Write([]byte(name))
		mem.FreeString(mem.System, name)
		digest.Write([]byte{0})
		if source.Files[i].Data != nil {
			digest.Write(source.Files[i].Data)
		} else {
			digest.Write([]byte(source.Files[i].Text))
		}
		digest.Write([]byte{0})
	}
	signature := core.Signature{Mode: core.SignatureContent}
	digest.Sum(signature.Digest[:])
	return signature
}

func ledgerContext(ledger *outputLedger, p *program.Program) core.Signature {
	context := p.CleanupContext()
	if context.Mode != core.SignatureContent || ledger.Source.Mode != core.SignatureContent {
		return core.Signature{}
	}
	digest := core.NewDigest()
	digest.Write(context.Digest[:])
	digest.Write(ledger.Source.Digest[:])
	digest.Sum(context.Digest[:])
	return context
}

func recordProduction(state any, p *program.Program, paths []string, producer string, committed bool) diagnostic.Diagnostic {
	ledger := state.(*outputLedger)
	context := ledgerContext(ledger, p)
	ok := false
	if len(paths) == 0 {
		ok = ledger.Store.Acquire()
	} else if committed {
		ok = ledger.Store.Commit(paths, producer, context.Digest)
	} else {
		ok = ledger.Store.Begin(paths, producer, context.Digest)
	}
	if ok {
		return diagnostic.Diagnostic{}
	}
	if committed && ledger.Store.Status == 2 {
		p.WarnOwnership("output ownership is uncertain; automatic cleanup will preserve this production")
		return diagnostic.Diagnostic{}
	}
	code, message := "CLEAN_STATE", "cannot persist output ownership; production remains uncertain"
	if ledger.Store.Status == -2 {
		code, message = "CLEAN_BUSY", "project is busy with another producer or cleanup"
	}
	return diagnostic.Diagnostic{Code: cloneCommandText(code), Message: cloneCommandText(message), Severity: diagnostic.Error, Owned: true}
}

func cleanRoot(directory string) string {
	if path.IsAbs(directory) {
		return path.Clean(mem.System, directory)
	}
	buffer := slices.Make[byte](mem.System, os.MaxPathLen)
	wd, err := os.Getwd(buffer)
	result := ""
	if err == nil {
		result = path.Join(mem.System, wd, directory)
	}
	slices.Free(mem.System, buffer)
	return result
}

func runClean(args []string, out io.Writer, errOut io.Writer) int {
	inv := cli.Parse("clean", args)
	applyInvocationPresentation(&inv)
	defer inv.Free()
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		return 2
	}
	root := cleanRoot(inv.Directory)
	defer mem.FreeString(mem.System, root)
	store := ownership.New(mem.System, root)
	defer store.Free()
	store.PollCancel = cleanupCancelled
	if inv.Command != "" {
		for i := range inv.Args {
			if !store.Protect(inv.Args[i], inv.Command == "--release") {
				cliError(errOut, "CLEAN_STATE", "cannot safely update output protection or release ownership")
				return 1
			}
		}
		return 0
	}
	return performClean(inv.Directory, store, inv.DryRun, inv.JSON, out, errOut)
}

func automaticClean(state any, p *program.Program) diagnostic.Diagnostic {
	ledger := state.(*outputLedger)
	policy := eval.Context{Run: mem.System, Cwd: p.Options.Directory, Grants: p.Options.Grants}
	if !policy.Allows(eval.Read, p.Options.Directory) || (!p.Options.DryRun && !policy.Allows(eval.Write, p.Options.Directory)) {
		return diagnostic.Diagnostic{Code: "CAP_DENIED", Message: "automatic clean requires project read and write authority", Severity: diagnostic.Error}
	}
	if p.Host != nil && p.Host.Active() != 0 {
		return diagnostic.Diagnostic{Code: "CLEAN_BUSY", Message: "cannot clean while project recipes or services are active", Severity: diagnostic.Error}
	}
	if performClean(p.Options.Directory, ledger.Store, p.Options.DryRun, ledger.JSON, ledger.Out, ledger.Err) != 0 {
		return diagnostic.Diagnostic{Code: "CLEAN_STATE", Message: "automatic cleanup withheld unsafe removal; inspect clean results", Severity: diagnostic.Error}
	}
	return diagnostic.Diagnostic{}
}

func performClean(directory string, store *ownership.Store, dry bool, jsonMode bool, out io.Writer, errOut io.Writer) int {
	store.PollCancel = cleanupCancelled
	// A missing/invalid build source is a validation boundary, not permission to
	// delete historical files. Source loading remains read-only and emits no work.
	options := cli.Parse("build", nil)
	options.Directory = directory
	// Keep validation diagnostics off the single clean-result stdout document.
	diagnosticOut := cliDiagnosticOut
	cliDiagnosticOut = errOut
	session := openBuildSession(options, errOut, false)
	cliDiagnosticOut = diagnosticOut
	defer session.Free()
	defer options.Free()
	var context core.Signature
	var protected []string
	if session.Status == 0 && session.Program != nil {
		ledger := outputLedger{Source: cleanupSource(&session.Source)}
		context = ledgerContext(&ledger, session.Program)
		inputs := session.Program.CleanupExternalInputs()
		for i := range inputs {
			member := store.Member(inputs[i])
			if member != "" {
				protected = slices.Append(mem.System, protected, member)
			}
		}
		program.FreeStrings(mem.System, inputs)
		for i := range session.Source.Files {
			physical := cleanRoot(session.Source.Files[i].Name)
			member := store.Member(physical)
			mem.FreeString(mem.System, physical)
			if member != "" {
				protected = slices.Append(mem.System, protected, member)
			}
		}
	}
	defer program.FreeStrings(mem.System, protected)
	items := store.Clean(context.Digest, context.Mode == core.SignatureContent, dry, protected)
	if session.Status != 0 && !session.Source.Missing {
		items = slices.Append(mem.System, items, ownership.Decision{State: "error", Reason: "build-context-invalid"})
	}
	defer ownership.FreeDecisions(mem.System, items)
	status, removed, preserved, missing, wouldRemove := 0, 0, 0, 0, 0
	for i := range items {
		if items[i].State == "error" {
			status = 1
		}
		if items[i].State == "removed" {
			removed++
		}
		if items[i].State == "preserved" {
			preserved++
		}
		if items[i].State == "missing" {
			missing++
		}
		if items[i].State == "would-remove" {
			wouldRemove++
		}
	}
	if jsonMode {
		e := json.NewEncoder(out)
		e.BeginObject()
		e.Str("schema")
		e.Int(1)
		e.Str("type")
		e.Str("clean-result")
		e.Str("entries")
		e.BeginArray()
		for i := range items {
			e.BeginObject()
			e.Str("path")
			e.Str(items[i].Path)
			e.Str("producer")
			e.Str(items[i].Producer)
			e.Str("state")
			e.Str(items[i].State)
			e.Str("reason")
			e.Str(items[i].Reason)
			if items[i].Recovery != "" {
				e.Str("recovery")
				e.Str(items[i].Recovery)
			}
			e.EndObject()
		}
		e.EndArray()
		e.Str("removed")
		e.Int(int64(removed))
		e.Str("preserved")
		e.Int(int64(preserved))
		e.Str("missing")
		e.Int(int64(missing))
		e.Str("wouldRemove")
		e.Int(int64(wouldRemove))
		e.Str("status")
		e.Int(int64(status))
		e.EndObject()
		e.Flush()
		io.WriteString(out, "\n")
	} else {
		for i := range items {
			io.WriteString(errOut, items[i].State+" "+items[i].Path+" · "+items[i].Reason+"\n")
			if items[i].Recovery != "" {
				io.WriteString(errOut, "recovery: "+items[i].Recovery+"\n")
			}
		}
		if preserved != 0 {
			io.WriteString(errOut, "clean: preserved historical outputs require review\n")
		}
		fmt.Fprintf(errOut, "clean: %d removed · %d preserved · %d missing · %d eligible in dry-run\n", removed, preserved, missing, wouldRemove)
	}
	return status
}
