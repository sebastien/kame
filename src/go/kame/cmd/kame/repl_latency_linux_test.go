//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// latencyShell drives one interactive shell for the keystroke probe.
type latencyShell struct {
	name  string
	start func(t *testing.T, directory string) *terminalSession
	// ready reports whether output since a prompt request shows a fresh
	// prompt that reads keys.
	ready func(frame string) bool
	// shown reports whether output since a key shows want on the line.
	shown func(frame, want string) bool
	// reset discards the typed line; cleared reports that it was discarded.
	reset   string
	cleared func(frame string) bool
}

func kashLatencyShell(binary string) latencyShell {
	shell := latencyShell{
		name: "kash",
		start: func(t *testing.T, directory string) *terminalSession {
			return startTerminalSession(t, binary, directory, []string{"TERM=xterm-256color", "NO_COLOR="}, "do", "run", "-i")
		},
		ready: func(frame string) bool {
			i := strings.LastIndex(frame, "\x1b[?25h")
			return i >= 0 && strings.Contains(stripControls(frame), "kash ") && !strings.ContainsAny(frame[i:], "\r\n")
		},
		// A frame is complete once the cursor is shown again.
		shown: func(frame, want string) bool {
			return strings.HasSuffix(frame, "\x1b[?25h") && strings.Contains(stripControls(frame), want)
		},
		// Ctrl-C starts a fresh prompt, which rescans PATH for highlighting.
		reset: "\x03",
	}
	shell.cleared = shell.ready
	return shell
}

func bashLatencyShell(binary string) latencyShell {
	return latencyShell{
		name: "bash",
		start: func(t *testing.T, directory string) *terminalSession {
			return startTerminalSession(t, binary, directory, []string{"TERM=xterm-256color", "PS1=bash$ ", "HISTFILE=/dev/null"}, "--norc", "--noprofile", "-i")
		},
		ready: func(frame string) bool { return strings.HasSuffix(stripControls(frame), "bash$ ") },
		shown: func(frame, want string) bool { return strings.Contains(stripControls(frame), want) },
		// Bash's prompt after Ctrl-C can race the interrupt; kill the line.
		reset: "\x15",
		// Trailing cursor moves of a wrapped line hold no text, so the
		// next key's echo is still told apart.
		cleared: func(frame string) bool { return strings.Contains(frame, "\x1b[K") },
	}
}

func stripControls(text string) string { return terminalControls.ReplaceAllString(text, "") }

// await waits until the output written since before satisfies accept.
func (s *terminalSession) await(before int, accept func(string) bool) {
	s.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		frame := string(s.output.Bytes()[before:])
		s.mu.Unlock()
		if accept(frame) {
			return
		}
		select {
		case <-s.changed:
		case <-deadline:
			s.command.Process.Kill()
			s.t.Fatalf("terminal output did not settle: %q", frame)
		}
	}
}

func (s *terminalSession) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.Len()
}

// latencies summarises key-to-frame durations.
type latencies []time.Duration

func (l latencies) String() string {
	if len(l) == 0 {
		return "-"
	}
	p50, p99 := l.percentiles()
	return fmt.Sprintf("n=%d p50=%s p99=%s max=%s", len(l), p50.Round(time.Microsecond), p99.Round(time.Microsecond), slices.Max(l).Round(time.Microsecond))
}

func (l latencies) percentiles() (p50, p99 time.Duration) {
	sorted := slices.Clone(l)
	slices.Sort(sorted)
	// Nearest rank: at fewer than 100 samples, p99 is the maximum.
	return sorted[(len(sorted)-1)/2], sorted[len(sorted)-1-len(sorted)/100]
}

type latencyReport struct {
	firstKey, typing, background latencies
	burst, paste                 time.Duration
	bracketed                    bool
}

// probeLatency types like a user, one key at a time waiting for each echo,
// on several fresh prompts, then with a background job running, then as a
// typed-ahead burst and as a paste.
func probeLatency(t *testing.T, shell latencyShell) latencyReport {
	s := shell.start(t, t.TempDir())
	var report latencyReport
	prompt := func(keys string) {
		t.Helper()
		before := s.size()
		s.send(keys)
		s.await(before, shell.ready)
	}
	reset := func() {
		t.Helper()
		before := s.size()
		s.send(shell.reset)
		s.await(before, shell.cleared)
	}
	typeLine := func(line string, first, rest *latencies) {
		t.Helper()
		for i, key := range line {
			before := s.size()
			start := time.Now()
			s.send(string(key))
			// Bash echoes only the new key; kash redraws the whole line.
			s.await(before, func(frame string) bool { return shell.shown(frame, string(key)) })
			if i == 0 && first != nil {
				*first = append(*first, time.Since(start))
			} else {
				*rest = append(*rest, time.Since(start))
			}
		}
		reset()
	}
	timed := func(text, marker string) time.Duration {
		t.Helper()
		before := s.size()
		start := time.Now()
		s.send(text)
		s.await(before, func(frame string) bool { return shell.shown(frame, marker) })
		elapsed := time.Since(start)
		reset()
		return elapsed
	}

	s.await(0, shell.ready)
	line := "echo hello-latency-probe-world"
	for range 5 {
		typeLine(line, &report.firstKey, &report.typing)
	}
	burst := "echo " + strings.Repeat("abcdefghij ", 18) + "ZQBURST"
	report.burst = timed(burst, "ZQBURST")
	// Real terminals bracket a paste only when the shell enabled it.
	pasted := "echo " + strings.Repeat("0123456789", 50) + "ZQPASTE"
	report.bracketed = strings.Contains(s.raw(), "\x1b[?2004h")
	if report.bracketed {
		pasted = "\x1b[200~" + pasted + "\x1b[201~"
	}
	report.paste = timed(pasted, "ZQPASTE")
	prompt("sleep 20 &\r")
	for range 3 {
		typeLine(line, nil, &report.background)
	}
	return report
}

// TestNativeInteractiveKashKeystrokeLatency reports how long a keystroke
// takes to reach the screen, for kash and, when installed, for bash on the
// same pseudo-terminal. It reports without a machine-dependent threshold.
func TestNativeInteractiveKashKeystrokeLatency(t *testing.T) {
	shells := []latencyShell{kashLatencyShell(nativeCLIBinary(t))}
	if bash, err := exec.LookPath("bash"); err == nil {
		shells = append(shells, bashLatencyShell(bash))
	}
	for _, shell := range shells {
		r := probeLatency(t, shell)
		t.Logf("%s first key:  %s", shell.name, r.firstKey)
		t.Logf("%s typing:     %s", shell.name, r.typing)
		t.Logf("%s background: %s", shell.name, r.background)
		t.Logf("%s burst of %d keys: %s", shell.name, 5+18*11+7, r.burst.Round(time.Microsecond))
		t.Logf("%s paste of %d chars (bracketed=%t): %s", shell.name, 5+500+7, r.bracketed, r.paste.Round(time.Microsecond))
	}
}
