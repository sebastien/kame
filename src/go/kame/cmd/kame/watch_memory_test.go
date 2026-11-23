package main

import (
	"bytes"
	"fmt"
	"io"
	"kame/core"
	"kame/internal/mem"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	write("a", "first")
	write("b", "second")
	write("selection", "./a")
	s := openBuildSession(options, io.Discard, true)
	defer s.Free()
	if s.Status != 0 {
		t.Fatal("compile failed")
	}
	root := s.Program.Start("default").Handle
	defer root.Free()
	var resources []watchResource
	defer func() { freeWatchResources(resources) }()
	for i, want := range []string{"first", "second", "updated"} {
		if i > 0 {
			next := "./b"
			if i == 2 {
				next = "./a"
				write("a", want)
			}
			write("selection", next)
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
		}
		refreshWatchResources(&resources, &s, options)
		count := len(s.Program.Engine.TrackedNodes())
		if root.Node.Latest.Text != want || count != 3 {
			t.Fatalf("cycle %d: expected %q and only its live dependencies; got %q, %d nodes", i, want, root.Node.Latest.Text, count)
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
