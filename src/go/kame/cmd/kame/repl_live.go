package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"kame/lang/usage"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	liveBudget   = 500 * time.Millisecond
	maxLiveBytes = 1 << 20
)

// liveCommand lists or registers live protocol executables.
func (c *completionSpecs) liveCommand(args []string, out, errOut io.Writer, usageError func() int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case len(args) == 0:
		paths := make([]string, 0, len(c.live))
		for path := range c.live {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		for _, path := range paths {
			fmt.Fprintf(out, "complete --live %s %s\n", c.live[path], path)
		}
		return 0
	case len(args) == 2 && (args[0] == "cobra" || args[0] == "clap"):
		if !filepath.IsAbs(args[1]) || !executableFile(args[1]) {
			fmt.Fprintf(errOut, "complete: %s is not an absolute path to an executable\n", args[1])
			return 2
		}
		c.live[filepath.Clean(args[1])] = args[0]
		return 0
	}
	return usageError()
}

func (c *completionSpecs) protocol(executable string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live[executable]
}

// liveComplete asks a registered executable for candidates: Cobra's
// __complete command or clap's COMPLETE=fish protocol. ok is false on any
// failure, so completion falls back to the cached spec.
func liveComplete(protocol, executable, typed string, args []string, current string, ctx completionContext) ([]usage.Candidate, usage.Files, bool) {
	argv := append([]string{executable, "__complete"}, args...)
	if protocol == "clap" {
		argv = append([]string{executable, "--", typed}, args...)
	}
	argv = append(argv, current)
	run, cancel := context.WithTimeout(context.Background(), liveBudget)
	defer cancel()
	cmd := exec.CommandContext(run, executable)
	cmd.Args, cmd.Dir = argv, ctx.Directory
	cmd.Env = append(os.Environ(), "PATH="+ctx.Path)
	if ctx.Home != "" {
		cmd.Env = append(cmd.Env, "HOME="+ctx.Home)
	}
	if protocol == "clap" {
		cmd.Env = append(cmd.Env, "COMPLETE=fish")
	}
	output := &limitedBuffer{limit: maxLiveBytes}
	cmd.Stdout, cmd.SysProcAttr = output, &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 50 * time.Millisecond
	if err := cmd.Run(); err != nil || output.full {
		return nil, usage.NoFiles, false
	}
	var candidates []usage.Candidate
	directive, directed := 0, false
	scanner := bufio.NewScanner(&output.Buffer)
	for scanner.Scan() {
		line := scanner.Text()
		if protocol == "cobra" && strings.HasPrefix(line, ":") {
			n, err := strconv.Atoi(line[1:])
			if err != nil {
				return nil, usage.NoFiles, false
			}
			directive, directed = n, true
			continue
		}
		if line == "" || directed {
			continue
		}
		text, help, _ := strings.Cut(line, "\t")
		kind := "value"
		if strings.HasPrefix(text, "-") {
			kind = "flag"
		}
		candidates = append(candidates, usage.Candidate{Text: text, Help: help, Kind: kind})
	}
	if scanner.Err() != nil {
		return nil, usage.NoFiles, false
	}
	if protocol == "clap" {
		return candidates, usage.NoFiles, true // clap lists paths itself
	}
	const (
		cobraError      = 1
		cobraNoFiles    = 4
		cobraFilterExt  = 8
		cobraFilterDirs = 16
	)
	switch {
	case !directed || directive&cobraError != 0:
		return nil, usage.NoFiles, false
	case directive&cobraFilterDirs != 0:
		return nil, usage.DirFiles, true
	case directive&cobraFilterExt != 0:
		// ponytail: lists every file; filter by the returned extensions if wanted.
		return nil, usage.AnyFiles, true
	case directive&cobraNoFiles != 0 || len(candidates) != 0:
		return candidates, usage.NoFiles, true
	}
	return candidates, usage.AnyFiles, true
}

// limitedBuffer keeps at most limit bytes and fails writes beyond them.
type limitedBuffer struct {
	bytes.Buffer
	limit int
	full  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.full = true
		return 0, errors.New("output limit exceeded")
	}
	return b.Buffer.Write(p)
}
