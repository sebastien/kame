package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"kame/cli"
	"kame/core"
	"kame/host"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/program"
	"os"
	"path"
	"slices"
	"strconv"
	"time"
)

type watchResource struct {
	Key        core.ResourceKey
	Stamp      string
	Directory  string
	Probe      string
	Metadata   bool
	Source     bool
	Seen       bool
	Registered bool
}

func freeWatchResources(items []watchResource) {
	for i := range items {
		items[i].Key.Free(mem.System)
		mem.FreeString(mem.System, items[i].Stamp)
		mem.FreeString(mem.System, items[i].Probe)
		mem.FreeString(mem.System, items[i].Directory)
	}
	mem.FreeSlice(nil, items)
}

func watchSourceStamp(key core.ResourceKey) string {
	// Compare source bytes even on hosts without reliable change timestamps.
	file, err := os.Open(key.Name)
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		return "unreadable"
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func watchProbe(p *program.Program, key core.ResourceKey) string {
	name := key.Name
	owned := false
	if !core.IsResourceURIName(name) && !path.IsAbs(name) {
		name = path.Join(p.Options.Directory, name)
		owned = true
	}
	h := posix.New(mem.System)
	stamp := h.WatchStamp(name)
	h.Free()
	if owned {
		mem.FreeString(mem.System, name)
	}
	return stamp
}

func scanWatchStamp(p *program.Program, observations program.WatchObservations, item *watchResource) string {
	if isDeclarationWatchResource(item.Key.Kind) {
		h := posix.New(mem.System)
		defer h.Free()
		return program.DeclarationFingerprint(mem.System, h, item.Directory, item.Key)
	}
	if p == nil || item.Source {
		return watchSourceStamp(item.Key)
	}
	if item.Key.Kind != core.ResourceFile {
		return observations.Fingerprint(item.Key)
	}
	probe := watchProbe(p, item.Key)
	metadata := observations.MetadataObserved(item.Key)
	if probe != "" && probe == item.Probe && metadata == item.Metadata {
		mem.FreeString(mem.System, probe)
		return item.Stamp
	}
	stamp := observations.Fingerprint(item.Key)
	// An edit during the read must force another content check next scan.
	after := watchProbe(p, item.Key)
	mem.FreeString(mem.System, item.Probe)
	item.Probe, item.Metadata = "", metadata
	if probe != "" && probe == after {
		item.Probe = cloneCommandText(probe)
	}
	mem.FreeString(mem.System, probe)
	mem.FreeString(mem.System, after)
	return stamp
}

func isDeclarationWatchResource(kind core.ResourceKind) bool {
	return kind == core.ResourceDiscovery || kind == core.ResourceDeclarationSource
}

func queueWatchChange(pending *[]core.ResourceKey, key core.ResourceKey) bool {
	if slices.Contains(*pending, key) {
		return false
	}
	*pending = append(*pending, key.Clone(mem.System))
	return true
}

func addWatchResource(items *[]watchResource, lookup map[core.ResourceKey]int, p *program.Program, observations program.WatchObservations, key core.ResourceKey, source bool) {
	if i, known := lookup[key]; known {
		(*items)[i].Seen = true
		(*items)[i].Source = (*items)[i].Source || source
		return
	}
	stamp := ""
	if !source && p != nil {
		stamp = observations.AcceptedFingerprint(key)
	} else {
		stamp = watchSourceStamp(key)
	}
	if !source && p != nil && key.Kind == core.ResourceFile {
		if node := p.Engine.Lookup(key); node != nil && node.Current && node.Latest.Kind == core.Nil {
			mem.FreeString(mem.System, stamp)
			stamp = cloneCommandText("missing")
		}
	}
	lookup[key] = len(*items)
	*items = append(*items, watchResource{Key: key.Clone(mem.System), Stamp: stamp, Source: source, Seen: true})
}

func refreshWatchResources(items *[]watchResource, session *buildSession, options buildArguments) program.WatchObservations {
	observations := session.Program.WatchObservations()
	lookup := make(map[core.ResourceKey]int, len(*items))
	for i := range *items {
		item := &(*items)[i]
		if _, known := lookup[item.Key]; !known {
			lookup[item.Key] = i
		}
		item.Seen = item.Source || (session.Status != 0 && isDeclarationWatchResource(item.Key.Kind))
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
		addWatchResource(items, lookup, session.Program, observations, key, true)
	} else if len(session.Source.Files) == 0 {
		candidates := cli.BuildSourceCandidates()
		for i := range candidates {
			name := path.Join(options.Directory, candidates[i])
			addWatchResource(items, lookup, session.Program, observations, core.ResourceKey{Kind: core.ResourceFile, Name: name}, true)
			mem.FreeString(mem.System, name)
		}
	}
	for i := range session.Source.Files {
		name := session.Source.Files[i].Name
		if session.Source.Files[i].FileBacked {
			addWatchResource(items, lookup, session.Program, observations, core.ResourceKey{Kind: core.ResourceFile, Name: name}, true)
		}
	}
	for _, discovery := range session.Discoveries {
		known := slices.IndexFunc(*items, func(item watchResource) bool {
			return item.Key.Kind == discovery.Key.Kind && item.Key.Name == discovery.Key.Name && item.Directory == discovery.Directory
		})
		if known >= 0 {
			(*items)[known].Seen = true
		} else {
			*items = append(*items, watchResource{Key: discovery.Key.Clone(mem.System), Directory: cloneCommandText(discovery.Directory), Stamp: cloneCommandText(discovery.Fingerprint), Seen: true})
		}
	}
	if session.Program != nil {
		resources := session.Program.FilesystemResources()
		for i := range resources {
			addWatchResource(items, lookup, session.Program, observations, resources[i], false)
			resources[i].Free(mem.System)
		}
		mem.FreeSlice(nil, resources)
	}
	var current []watchResource
	for i := range *items {
		if (*items)[i].Seen {
			current = append(current, (*items)[i])
		} else {
			if session.Program != nil && !(*items)[i].Source {
				session.Program.RetireWatchResource((*items)[i].Key)
			}
			(*items)[i].Key.Free(mem.System)
			mem.FreeString(mem.System, (*items)[i].Stamp)
			mem.FreeString(mem.System, (*items)[i].Probe)
			mem.FreeString(mem.System, (*items)[i].Directory)
		}
	}
	mem.FreeSlice(nil, *items)
	*items = current
	return observations
}

func freeWatchHandles(handles []*program.Handle) {
	for i := range handles {
		handles[i].Free()
	}
	mem.FreeSlice(nil, handles)
}

func startWatchHandles(session *buildSession, options buildArguments, out io.Writer, errOut io.Writer) []*program.Handle {
	if session.Program == nil {
		return nil
	}
	targets := selectTargets(session.Program, slices.Clone(options.Targets))
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
			handles = append(handles, started.Handle)
		}
	}
	program.FreeStrings(mem.System, targets)
	return handles
}

func syncWatchNotifications(w *posix.Watcher, p *program.Program, items []watchResource) bool {
	w.Begin()
	h := posix.New(mem.System)
	complete := true
	for i := range items {
		name := items[i].Key.Name
		glob := items[i].Key.Kind == core.ResourceGlob || items[i].Key.Kind == core.ResourceDiscovery
		if items[i].Key.Kind == core.ResourceDiscovery {
			name, _ = host.DeclarationDiscovery(name)
		}
		if glob {
			name = host.DiscoveryScan(name)
		}
		owned := false
		directory := items[i].Directory
		if directory == "" && p != nil && !items[i].Source {
			directory = p.Options.Directory
		}
		if directory != "" && !core.IsResourceURIName(name) && !path.IsAbs(name) {
			name = path.Join(directory, name)
			owned = true
		}
		if !h.AddWatch(w, name, glob) {
			complete = false
		}
		if owned {
			mem.FreeString(mem.System, name)
		}
		items[i].Registered = true
	}
	h.Free()
	return w.Commit() && complete
}

// One host loop scans while recipes run. It queues changes until the current
// build settles, then invalidates the retained roots' affected dependencies.
func runWatch(options buildArguments, out io.Writer, errOut io.Writer) int {
	cycle, cycleStarted := 1, time.Now()
	dashboardCycle = cycle
	if options.JSON {
		cli.WriteWatchTransition(out, "watch-cycle-started", cycle, "", 0, 0, 0, 0)
	}
	session := openBuildSession(options, errOut, true)
	handles := startWatchHandles(&session, options, out, errOut)
	var resources []watchResource
	var pending []core.ResourceKey
	refreshWatchResources(&resources, &session, options)
	watcher := posix.NewWatcher()
	notifications := syncWatchNotifications(watcher, session.Program, resources)
	progress := buildProgress{}
	lastScan, lastChange := int64(0), int64(0)
	lastRefresh := int64(0)
	dirty := true
	recompile, reported := false, false
	cancelledForChange := false
	validationQueued := false

	if !options.JSON {
		if notifications {
			io.WriteString(errOut, "Watching filesystem resources (OS notifications; 10s reconciliation; "+strconv.Itoa(options.WatchDebounceMS)+"ms debounce)\n")
		} else {
			io.WriteString(errOut, "Watching filesystem resources (200ms polling fallback; "+strconv.Itoa(options.WatchDebounceMS)+"ms debounce)\n")
		}
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
				cancelledDiagnostic := cancelledForChange && polled.Result.Diagnostic.Code == "EXEC_CANCELLED"
				if polled.Done && polled.Result.Diagnostic.Code != "" && !reported && !cancelledDiagnostic {
					cycleFailed = true
					emitDiagnostic(diagnosticWriter(out, errOut, options.JSON), polled.Result.Diagnostic, options.JSON, session.Program.Parsed.Source)
				}
				polled.Result.Free(mem.System)
			}
			if cancelledForChange && session.Program.Host != nil && session.Program.Host.Active() != 0 {
				busy = true
			}
			if !busy {
				reported = true
			}
		}
		if !busy && !cycleFinished {
			lastRefresh = 0 // Observe dependencies published by the final build tick.
			clearDashboard(errOut)
			invocationCounts = progress
			outcome := "success"
			if cycleFailed || progress.Failed != 0 {
				outcome = "failure"
			} else if cancelledForChange {
				outcome = "cancelled"
			}
			elapsed := int64(time.Since(cycleStarted)) / 1000000
			dashboardCycleElapsed = elapsed
			if options.JSON {
				cli.WriteWatchTransition(out, "watch-cycle-finished", cycle, outcome, elapsed, progress.Completed, progress.Failed, progress.Cancelled)
				cli.WriteWatchTransition(out, "watch-idle", cycle, outcome, 0, 0, 0, 0)
			} else {
				cycleStatus := 0
				if outcome == "failure" {
					cycleStatus = 1
				} else if outcome == "cancelled" {
					cycleStatus = 130
				}

				writeHumanSummary(errOut, "watch cycle "+strconv.Itoa(cycle), cycleStatus, elapsed, progress)
				io.WriteString(errOut, "watch · waiting for changes · last cycle "+outcome+" · Ctrl+C to stop\n")
			}
			cycleFinished, dashboardIdle = true, true
			cycleStatus := 0
			if outcome != "success" {
				cycleStatus = 1
			}
			finishTaskbar(errOut, cycleStatus)
			flushCLIOutput(out)
			flushCLIOutput(errOut)
		}
		now := time.Now().UnixNano() / 1000000
		if !busy && !validationQueued && session.Program != nil {
			for _, key := range session.Program.ChangedProductionInputs() {
				if queueWatchChange(&pending, key) {
					lastChange = now
				}
			}
			validationQueued = true
		}
		if !cancelledForChange && ((busy && now-lastRefresh >= 200) || (!busy && lastRefresh == 0)) {
			count := len(resources)
			refreshWatchResources(&resources, &session, options)
			dirty = dirty || count != len(resources)
			for i := range resources {
				dirty = dirty || !resources[i].Registered
			}
			lastRefresh = now
		}
		interval := int64(10000)
		if !notifications {
			interval = 200
		}
		if dirty || now-lastScan >= interval {
			var observations program.WatchObservations
			if !cancelledForChange {
				observations = refreshWatchResources(&resources, &session, options)
				notifications = syncWatchNotifications(watcher, session.Program, resources)
			} else {
				observations = session.Program.WatchObservations()
			}
			dirty = false
			for i := range resources {
				stamp := scanWatchStamp(session.Program, observations, &resources[i])
				if stamp != resources[i].Stamp {
					session.Program.NoteProductionInputChange(resources[i].Key)
					clearDashboard(errOut)
					if !options.JSON {
						io.WriteString(errOut, "info watch: change queued: "+host.DiscoveryPattern(resources[i].Key.Name)+"\n")
					}
					queueWatchChange(&pending, resources[i].Key)
					recompile = recompile || resources[i].Source || isDeclarationWatchResource(resources[i].Key.Kind)
					mem.FreeString(mem.System, resources[i].Stamp)
					resources[i].Stamp = stamp
					lastChange = now
				} else {
					mem.FreeString(mem.System, stamp)
				}
			}
			lastScan = now
		}
		if options.CancelOnChange && busy && len(pending) != 0 && !cancelledForChange {
			session.Program.ReportProductionCancellation(pending)
			for i := range handles {
				handles[i].Cancel()
			}
			// Async processes retain independent roots and their launcher's interest.
			session.Program.Eval.CancelProcesses()
			cancelledForChange = true
		}
		if len(pending) != 0 && !busy && now-lastChange >= int64(options.WatchDebounceMS) {
			cycle++
			dashboardCycle = cycle
			cycleStarted = time.Now()
			cycleFinished, cycleFailed, dashboardIdle = false, false, false
			invocationHadDiagnostic = false
			progress = buildProgress{}
			freeOutcomes()
			startTaskbar(errOut)
			dashboardStarted = presentationNow()
			if options.JSON {
				cli.WriteWatchTransition(out, "watch-cycle-started", cycle, "", 0, 0, 0, 0)
			}
			// Cancelled definitions retain terminal state; replace the session
			// so every root and resource is evaluated against the latest inputs.
			if cancelledForChange || recompile || session.Program == nil {
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
			mem.FreeSlice(nil, pending)
			pending = nil
			recompile, reported, cancelledForChange = false, false, false
			validationQueued = false
			dirty = true
		}
		wait := 1000
		if busy || dirty {
			wait = 10
		}
		if !notifications && wait > 200 {
			wait = 200
		}
		if len(pending) != 0 && !busy {
			remaining := int(int64(options.WatchDebounceMS) - (now - lastChange))
			if remaining > 0 && remaining < wait {
				wait = remaining
			}
		}
		if watcher.Wait(wait) != 0 {
			dirty = true
		}
	}
	freeWatchHandles(handles)
	if session.Program != nil {
		for session.Program.Host != nil && session.Program.Host.Active() != 0 {
			tickCLIProgram(session.Program, 10, errOut, options.JSON)
			drainEvents(session.Program, out, errOut, options.JSON, &progress)
		}
	}
	session.Free()
	watcher.Free()
	freeWatchResources(resources)
	for i := range pending {
		pending[i].Free(mem.System)
	}
	mem.FreeSlice(nil, pending)
	return status
}
