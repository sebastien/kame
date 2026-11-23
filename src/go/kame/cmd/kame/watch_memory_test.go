package main

import (
	"bytes"
	"fmt"
	"io"
	"kame/core"
	"kame/internal/mem"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func watchTestOptions(t *testing.T) buildArguments {
	previous := diagnosticSources
	diagnosticSources = nil
	t.Cleanup(func() { restoreDiagnosticSources(previous) })
	return buildArguments{Directory: t.TempDir(), Watch: true}
}

func TestWatchReplacesDiagnosticSources(t *testing.T) {
	options := watchTestOptions(t)
	for i := range 20 {
		name := fmt.Sprintf("part%d.km", i)
		text := "default = 1\n"
		if i%2 != 0 {
			text = "broken = (\n"
		}
		nativeCLIWrite(t, filepath.Join(options.Directory, "Makefile.km"), "include ./"+name+"\n")
		nativeCLIWrite(t, filepath.Join(options.Directory, name), text)
		var diagnostics bytes.Buffer
		s := openBuildSession(options, &diagnostics, true)
		status := s.Status
		s.Free()
		if (status == 0) != (i%2 == 0) || len(diagnosticSources) != 2 {
			t.Fatalf("load %d: status %d, retained %d sources: %s", i, status, len(diagnosticSources), &diagnostics)
		}
		if status != 0 && (!strings.Contains(diagnostics.String(), name+":") || !strings.Contains(diagnostics.String(), "broken = (")) {
			t.Fatalf("missing current excerpt: %s", &diagnostics)
		}
	}
}

func TestWatchSourceFingerprintAndPendingChanges(t *testing.T) {
	key := core.ResourceKey{Kind: core.ResourceFile, Name: filepath.Join(t.TempDir(), "source")}
	missing := watchSourceStamp(key)
	nativeCLIWrite(t, key.Name, "missing")
	if watchSourceStamp(key) == missing {
		t.Fatal("source bytes confused with missing file")
	}
	nativeCLIWrite(t, key.Name, strings.Repeat("x", 1<<20))
	if stamp := watchSourceStamp(key); len(stamp) > 64 || stamp == missing {
		t.Fatalf("unexpected source stamp length %d", len(stamp))
	}
	var pending []core.ResourceKey
	for range 100 {
		queueWatchChange(&pending, key)
	}
	glob := core.ResourceKey{Kind: core.ResourceGlob, Name: key.Name}
	queueWatchChange(&pending, glob)
	if !slices.Equal(pending, []core.ResourceKey{key, glob}) {
		t.Fatalf("expected one pending invalidation per resource, got %v", pending)
	}
}

func TestWatchRetiresDetachedInputAndRediscoversIt(t *testing.T) {
	options := watchTestOptions(t)
	write := func(name, text string) { nativeCLIWrite(t, filepath.Join(options.Directory, name), text) }
	write("Makefile.km", "default = text (read (text (read ./selection)))\n")
	write("item-00000", "0")
	write("selection", "./item-00000")
	s := openBuildSession(options, io.Discard, true)
	defer s.Free()
	if s.Status != 0 {
		t.Fatal("compile failed")
	}
	root := s.Program.Start("default").Handle
	defer root.Free()
	var resources []watchResource
	defer func() { freeWatchResources(resources) }()
	cycles := 32
	if os.Getenv("KAME_WATCH_SOAK") == "1" {
		cycles = 10000
	}
	var warm runtime.MemStats
	if cycles == 10000 {
		runtime.ReadMemStats(&warm)
	}
	windowAlloc := warm.TotalAlloc
	var latency time.Duration
	previous := ""
	for i := range cycles {
		want := fmt.Sprint(i)
		// First exercise a fixed working set, then introduce a fresh identity
		// every other cycle while still checking rediscovery of item zero.
		id := i % 2
		if i >= cycles/2 && i%2 == 1 {
			id = i
		}
		next := fmt.Sprintf("item-%05d", id)
		if i > 0 {
			if err := os.Remove(filepath.Join(options.Directory, previous)); err != nil {
				t.Fatal(err)
			}
			write(next, want)
			write("selection", "./"+next)
		}
		start := time.Now()
		if i > 0 {
			for _, r := range resources {
				if filepath.Base(r.Key.Name) == "selection" {
					s.Program.InvalidateResource(r.Key)
				}
			}
		}
		for tick := 0; tick < 100; tick++ {
			s.Program.Tick(0)
			result := root.PollRetained()
			done := result.Done && result.Result.Value.Text == want
			result.Result.Free(mem.System)
			if done {
				break
			}
			if tick == 99 {
				t.Fatalf("cycle %d did not settle", i)
			}
		}
		refreshWatchResources(&resources, &s, options)
		latency += time.Since(start)
		for event := s.Program.NextEvent(); event.OK; event = s.Program.NextEvent() {
			event.Event.Free(mem.System)
		}
		count := len(s.Program.Engine.TrackedNodes())
		if root.Node.Latest.Text != want || count != 3 {
			t.Fatalf("cycle %d: expected %q and only its live dependencies; got %q, %d nodes", i, want, root.Node.Latest.Text, count)
		}
		for _, r := range resources {
			if previous != "" && filepath.Base(r.Key.Name) == previous {
				t.Fatalf("cycle %d still watching %s", i, previous)
			}
		}
		previous = next
		if cycles == 10000 && (i+1)%1000 == 0 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if i == 999 {
				warm = m
			}
			t.Logf("cycles=%d heap_B=%d objects=%d delta_from_warm_B=%d mean_update_ns=%d allocated_B_per_cycle=%d",
				i+1, m.HeapAlloc, m.HeapObjects, int64(m.HeapAlloc)-int64(warm.HeapAlloc), latency.Nanoseconds()/1000,
				(m.TotalAlloc-windowAlloc)/1000)
			windowAlloc = m.TotalAlloc
			latency = 0
		}
	}
}

func TestPlainGlobWatchStartsFromAcceptedMembership(t *testing.T) {
	options := watchTestOptions(t)
	nativeCLIWrite(t, filepath.Join(options.Directory, "Makefile.km"), "default = wildcard ./*.txt\n")
	nativeCLIWrite(t, filepath.Join(options.Directory, "a.txt"), "")
	s := openBuildSession(options, io.Discard, true)
	defer s.Free()
	if s.Status != 0 {
		t.Fatal("compile failed")
	}
	root := s.Program.Start("default").Handle
	defer root.Free()
	for tick := 0; !root.Node.Current && tick < 100; tick++ {
		s.Program.Tick(0)
	}
	if !root.Node.Current || len(root.Node.Latest.List) != 1 {
		t.Fatal("glob did not settle")
	}
	for _, key := range s.Program.FilesystemResources() {
		if key.Kind != core.ResourceGlob {
			continue
		}
		accepted := s.Program.ResourceAcceptedFingerprint(key)
		if len(accepted) != 64 || s.Program.ResourceFingerprint(key) != accepted {
			t.Fatal("live and accepted glob digests disagree")
		}
		nativeCLIWrite(t, filepath.Join(options.Directory, "b.txt"), "")
		if s.Program.ResourceAcceptedFingerprint(key) != accepted || s.Program.ResourceFingerprint(key) == accepted {
			t.Fatal("swallowed membership change before first watch scan")
		}
		return
	}
	t.Fatal("no glob resource")
}

func TestWatchGraphShrinksAfterRepeatedPeaks(t *testing.T) {
	options := watchTestOptions(t)
	nativeCLIWrite(t, filepath.Join(options.Directory, "Makefile.km"),
		"default = map (p => text (read p)) (files ./*.txt)\n")
	nativeCLIWrite(t, filepath.Join(options.Directory, "keep.txt"), "keep")
	s := openBuildSession(options, io.Discard, true)
	defer s.Free()
	if s.Status != 0 {
		t.Fatal("compile failed")
	}
	root := s.Program.Start("default").Handle
	defer root.Free()
	var resources []watchResource
	defer func() { freeWatchResources(resources) }()
	var baseline [3]int
	for cycle := range 9 {
		wide := cycle%2 == 1
		if cycle > 0 {
			for i := range 16 {
				path := filepath.Join(options.Directory, fmt.Sprintf("peak-%d-%d.txt", (cycle-1)/2, i))
				if wide {
					nativeCLIWrite(t, path, "peak")
				} else if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range resources {
				if r.Key.Kind == core.ResourceGlob {
					s.Program.InvalidateResource(r.Key)
				}
			}
		}
		want := 1
		if wide {
			want += 16
		}
		for tick := 0; ; tick++ {
			s.Program.Tick(0)
			result := root.PollRetained()
			done := result.Done && len(result.Result.Value.List) == want
			result.Result.Free(mem.System)
			if done {
				break
			}
			if tick > 256 {
				t.Fatalf("cycle %d did not settle to %d inputs", cycle, want)
			}
		}
		refreshWatchResources(&resources, &s, options)
		nodes := s.Program.Engine.TrackedNodes()
		counts := [3]int{len(nodes), 0, len(resources)}
		for _, n := range nodes {
			counts[1] += len(n.Dependents)
		}
		if cycle == 0 {
			baseline = counts
		} else if !wide && counts != baseline {
			t.Fatalf("cycle %d: retained nodes/edges/watches %v, baseline %v", cycle, counts, baseline)
		}
	}
}
