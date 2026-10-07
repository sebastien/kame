package main

import (
	"kame/core"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/time"
)

type watchResource struct {
	Key    core.ResourceKey
	Stamp  string
	Source bool
	Seen   bool
}

func freeWatchResources(items []watchResource) {
	for i := range items {
		items[i].Key.Free(mem.System)
		mem.FreeString(mem.System, items[i].Stamp)
	}
	slices.Free(mem.System, items)
}

func watchStamp(p *program.Program, key core.ResourceKey, source bool) string {
	if p != nil && !source {
		return p.ResourceFingerprint(key)
	}
	h := posix.New(mem.System)
	info := h.Stat(key.Name)
	h.Free()
	if !info.Exists {
		return cloneCommandText("missing")
	}
	// Source retry also detects content changes with preserved timestamps.
	data, err := posixReadWatchFile(key.Name)
	if err != nil {
		return cloneCommandText("unreadable")
	}
	stamp := cloneCommandText(string(data))
	slices.Free(mem.System, data)
	return stamp
}

func posixReadWatchFile(name string) ([]byte, error) {
	h := posix.New(mem.System)
	data, err := h.ReadFile(mem.System, name)
	h.Free()
	return data, err
}

func addWatchResource(items *[]watchResource, p *program.Program, key core.ResourceKey, source bool) {
	for i := range *items {
		if (*items)[i].Key.Kind == key.Kind && (*items)[i].Key.Name == key.Name {
			(*items)[i].Seen = true
			(*items)[i].Source = (*items)[i].Source || source
			return
		}
	}
	stamp := watchStamp(p, key, source)
	if !source && p != nil {
		mem.FreeString(mem.System, stamp)
		stamp = p.ResourceAcceptedFingerprint(key)
	}
	if !source && p != nil && key.Kind == core.ResourceFile {
		if node := p.Engine.Lookup(key); node != nil && node.Current && node.Latest.Kind == core.Nil {
			mem.FreeString(mem.System, stamp)
			stamp = cloneCommandText("missing")
		}
	}
	*items = slices.Append(mem.System, *items, watchResource{Key: key.Clone(mem.System), Stamp: stamp, Source: source, Seen: true})
}

func refreshWatchResources(items *[]watchResource, session *buildSession, options buildArguments) {
	for i := range *items {
		(*items)[i].Seen = (*items)[i].Source
	}
	// Keep failed included sources until a successful compile replaces them.
	if session.Status == 0 {
		for i := range *items {
			if (*items)[i].Source {
				(*items)[i].Seen = false
			}
		}
	}
	if options.File != "" {
		key := core.ResourceKey{Kind: core.ResourceFile, Name: options.File}
		addWatchResource(items, session.Program, key, true)
	} else if len(session.Source.Files) == 0 {
		candidates := []string{"Makefile.kmk", "make.kmk", "src/kmk/main.kmk"}
		for i := range candidates {
			name := path.Join(mem.System, options.Directory, candidates[i])
			addWatchResource(items, session.Program, core.ResourceKey{Kind: core.ResourceFile, Name: name}, true)
			mem.FreeString(mem.System, name)
		}
	}
	for i := range session.Source.Files {
		name := session.Source.Files[i].Name
		if name != "<command>" {
			addWatchResource(items, session.Program, core.ResourceKey{Kind: core.ResourceFile, Name: name}, true)
		}
	}
	if session.Program != nil {
		resources := session.Program.FilesystemResources()
		for i := range resources {
			addWatchResource(items, session.Program, resources[i], false)
			resources[i].Free(mem.System)
		}
		slices.Free(mem.System, resources)
	}
	var current []watchResource
	for i := range *items {
		if (*items)[i].Seen {
			current = slices.Append(mem.System, current, (*items)[i])
		} else {
			(*items)[i].Key.Free(mem.System)
			mem.FreeString(mem.System, (*items)[i].Stamp)
		}
	}
	slices.Free(mem.System, *items)
	*items = current
}

func freeWatchHandles(handles []*program.Handle) {
	for i := range handles {
		handles[i].Free()
	}
	slices.Free(mem.System, handles)
}

func startWatchHandles(session *buildSession, options buildArguments, out io.Writer, errOut io.Writer) []*program.Handle {
	if session.Program == nil {
		return nil
	}
	targets := selectTargets(session.Program, slices.Clone(mem.System, options.Targets))
	if len(targets) != 0 {
		setDashboardSubject(targets[0])
	}
	if len(targets) == 0 {
		reportNoDefault(session.Program, out, errOut, options.JSON)
	}
	var handles []*program.Handle
	for i := range targets {
		started := session.Program.Start(targets[i])
		if started.Diagnostic.Code != "" {
			emitDiagnostic(diagnosticWriter(out, errOut, options.JSON), started.Diagnostic, options.JSON, session.Program.Parsed.Source)
			started.Diagnostic.Free(mem.System)
		} else {
			handles = slices.Append(mem.System, handles, started.Handle)
		}
	}
	program.FreeStrings(mem.System, targets)
	return handles
}

// One host loop scans while recipes run. It queues changes until the current
// build settles, then invalidates the retained roots' affected dependencies.
func runWatch(options buildArguments, out io.Writer, errOut io.Writer) int {
	cycle, cycleStarted := 1, time.Now()
	dashboardCycle = cycle
	if options.JSON {
		program.WriteWatchTransition(out, "watch-cycle-started", cycle, "", 0, 0, 0, 0)
	}
	session := openBuildSession(options, errOut, true)
	handles := startWatchHandles(&session, options, out, errOut)
	var resources []watchResource
	var pending []core.ResourceKey
	refreshWatchResources(&resources, &session, options)
	progress := buildProgress{}
	lastScan, lastChange := int64(0), int64(0)
	recompile, reported := false, false
	if !options.JSON {
		io.WriteString(errOut, "Watching filesystem resources (200ms polling; 100ms debounce)\n")
	}
	status := 0
	cycleFinished, cycleFailed := false, session.Status != 0 || invocationHadDiagnostic
	for {
		signal := posix.TakeSignal()
		if signal != 0 {
			if signal < 0 {
				status = 128 - signal
			} else {
				status = 128 + signal
			}
			break
		}
		busy := false
		if session.Program != nil {
			tickCLIProgram(session.Program, 10, errOut, options.JSON)
			drainEvents(session.Program, out, errOut, options.JSON, &progress)
			for i := range handles {
				if handles[i].Definition && handles[i].Node.Current {
					continue
				}
				polled := handles[i].PollRetained()
				if !polled.Done {
					busy = true
				}
				if polled.Done && polled.Result.Diagnostic.Code != "" && !reported {
					cycleFailed = true
					emitDiagnostic(diagnosticWriter(out, errOut, options.JSON), polled.Result.Diagnostic, options.JSON, session.Program.Parsed.Source)
				}
				polled.Result.Free(mem.System)
			}
			if !busy {
				reported = true
			}
		}
		if !busy && !cycleFinished {
			clearDashboard(errOut)
			invocationCounts = progress
			outcome := "success"
			if cycleFailed || progress.Failed != 0 {
				outcome = "failure"
			}
			elapsed := int64(time.Since(cycleStarted)) / 1000000
			dashboardCycleStatus, dashboardCycleElapsed = outcome, elapsed
			if options.JSON {
				program.WriteWatchTransition(out, "watch-cycle-finished", cycle, outcome, elapsed, progress.Completed, progress.Failed, progress.Cancelled)
				program.WriteWatchTransition(out, "watch-idle", cycle, outcome, 0, 0, 0, 0)
			} else {
				cycleStatus := 0
				if outcome == "failure" {
					cycleStatus = 1
				}
				var digits [24]byte
				writeHumanSummary(errOut, "watch cycle "+strconv.Itoa(digits[:], cycle), cycleStatus, elapsed, progress)
				io.WriteString(errOut, "watch · waiting for changes · last cycle "+outcome+" · Ctrl+C to stop\n")
			}
			cycleFinished, dashboardIdle = true, true
			flushCLIOutput(out)
			flushCLIOutput(errOut)
		}
		now := time.Now().UnixNano() / 1000000
		if now-lastScan >= 200 {
			refreshWatchResources(&resources, &session, options)
			for i := range resources {
				stamp := watchStamp(session.Program, resources[i].Key, resources[i].Source)
				if stamp != resources[i].Stamp {
					clearDashboard(errOut)
					if !options.JSON {
						io.WriteString(errOut, "info watch: change queued: "+resources[i].Key.Name+"\n")
					}
					pending = slices.Append(mem.System, pending, resources[i].Key.Clone(mem.System))
					recompile = recompile || resources[i].Source
					mem.FreeString(mem.System, resources[i].Stamp)
					resources[i].Stamp = stamp
					lastChange = now
				} else {
					mem.FreeString(mem.System, stamp)
				}
			}
			lastScan = now
		}
		if len(pending) != 0 && !busy && now-lastChange >= 100 {
			cycle++
			dashboardCycle = cycle
			cycleStarted = time.Now()
			cycleFinished, cycleFailed, dashboardIdle = false, false, false
			invocationHadDiagnostic = false
			progress = buildProgress{}
			freeOutcomes()
			dashboardStarted = presentationNow()
			if options.JSON {
				program.WriteWatchTransition(out, "watch-cycle-started", cycle, "", 0, 0, 0, 0)
			}
			if recompile || session.Program == nil {
				freeWatchHandles(handles)
				session.Free()
				session = openBuildSession(options, errOut, true)
				handles = startWatchHandles(&session, options, out, errOut)
				cycleFailed = session.Status != 0 || invocationHadDiagnostic
			} else {
				for i := range pending {
					session.Program.InvalidateResource(pending[i])
				}
			}
			for i := range pending {
				pending[i].Free(mem.System)
			}
			slices.Free(mem.System, pending)
			pending = nil
			recompile, reported = false, false
		}
		time.Sleep(10 * time.Millisecond)
	}
	freeWatchHandles(handles)
	if session.Program != nil {
		for session.Program.Host != nil && session.Program.Host.Active() != 0 {
			tickCLIProgram(session.Program, 10, errOut, options.JSON)
			drainEvents(session.Program, out, errOut, options.JSON, &progress)
		}
	}
	session.Free()
	freeWatchResources(resources)
	for i := range pending {
		pending[i].Free(mem.System)
	}
	slices.Free(mem.System, pending)
	return status
}
